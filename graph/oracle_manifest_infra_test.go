package graph

import (
	"testing"

	"github.com/alexdx2/chronicle-core/manifest"
	"github.com/alexdx2/chronicle-core/store"
	"github.com/alexdx2/chronicle-core/validate"
)

// Task 6 field defect #1: InfraNodeKey emitted a 3-segment key
// ("infra:{type}:{address}") — the host sits in the domain slot, so the key
// does not parse as validate.NormalizeNodeKey's layer:type:domain:name shape
// and passes through import_all's validation untouched, invisible to edge
// validation. InfraNodeKey now takes the scan's domainKey and always
// produces 4 segments. A ':' inside the address/name (host:port, e.g.
// "kafka:9092") is replaced with '-' in the name segment — SplitN(key, ":",
// 4) would otherwise fold the port into the domain segment's tail and the
// key would not survive NormalizeNodeKey unchanged.
func TestInfraNodeKey_FixedPointsOfNormalizeNodeKey(t *testing.T) {
	cases := []struct {
		name    string
		entry   manifest.InfraEntry
		domain  string
		wantKey string
	}{
		{
			name:    "fly-app type, dash-only name, no address",
			entry:   manifest.InfraEntry{Name: "moj-serwis", Type: "fly-app"},
			domain:  "auto",
			wantKey: "infra:fly-app:auto:moj-serwis",
		},
		{
			name:    "broker with host:port address",
			entry:   manifest.InfraEntry{Name: "kafka", Type: "broker", Address: "kafka:9092"},
			domain:  "testapp",
			wantKey: "infra:broker:testapp:kafka-9092",
		},
		{
			name:    "no address, name itself carries host:port",
			entry:   manifest.InfraEntry{Name: "kafka:9092", Type: "message_broker"},
			domain:  "test-domain",
			wantKey: "infra:message_broker:test-domain:kafka-9092",
		},
		{
			name:    "plain type/name, nothing to fold",
			entry:   manifest.InfraEntry{Name: "redis", Type: "cache"},
			domain:  "orders",
			wantKey: "infra:cache:orders:redis",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			key := c.entry.InfraNodeKey(c.domain)
			if key != c.wantKey {
				t.Fatalf("InfraNodeKey(%q) = %q, want %q", c.domain, key, c.wantKey)
			}
			norm, err := validate.NormalizeNodeKey(key)
			if err != nil {
				t.Fatalf("InfraNodeKey(%q) = %q does not parse as a valid node key: %v", c.domain, key, err)
			}
			if norm != key {
				t.Errorf("InfraNodeKey(%q) = %q is not a fixed point of NormalizeNodeKey; normalized = %q", c.domain, key, norm)
			}
		})
	}
}

// Task 6 field defect #2: manifest infra nodes were created with
// FirstSeenRevisionID/LastSeenRevisionID left at their zero value. Since
// MarkStaleNodes considers a node stale when last_seen_revision_id <
// revisionID, and 0 < any real revision, the FIRST chronicle_stale_mark call
// after a scan marked every manifest infra node stale forever. This oracle
// covers the two paths that must both hold once discover.go stamps the scan
// revision on infra nodes:
//   - a node survives MarkStaleNodes called AT its own creation revision
//     (fresh: last_seen == revID, not < revID);
//   - a node still declared in the manifest survives MarkStaleNodes at the
//     NEXT revision, because rediscovery re-upserts it and last_seen moves
//     forward to the new revision.
//
// A third case rules the test non-vacuous: a node NOT rediscovered at the
// next revision (dropped from the manifest) must still go stale — proving
// survival above is really driven by the revision stamp, not some blanket
// exemption for infra nodes.
func TestOracleManifestInfra_SurvivesStaleMarkAcrossRevisions(t *testing.T) {
	g := setupGraphDefaults(t)
	tmpDir := gitFixtureRepo(t)
	domainKey := "test-domain"

	fullManifest := &manifest.Manifest{
		Domains: []manifest.DomainEntry{{Name: domainKey, Scan: manifest.ScanConfig{Include: []string{"src/**"}}}},
		Infrastructure: []manifest.InfraEntry{
			{Name: "kafka", Type: "broker", Address: "kafka:9092"},
			{Name: "redis", Type: "cache", Address: "redis:6379"},
		},
	}

	revID1, err := g.store.CreateRevision(domainKey, "", "sha1", "manual", "full", "{}")
	if err != nil {
		t.Fatalf("CreateRevision rev1: %v", err)
	}
	if _, err := g.DiscoverFilesOpts(tmpDir, domainKey, revID1, fullManifest, DiscoverOpts{VotesNeeded: 1}); err != nil {
		t.Fatalf("DiscoverFilesOpts rev1: %v", err)
	}

	infraRows := func() []store.NodeRow {
		t.Helper()
		rows, err := g.store.ListNodes(store.NodeFilter{Layer: "infra", Domain: domainKey})
		if err != nil {
			t.Fatalf("ListNodes: %v", err)
		}
		return rows
	}

	rows := infraRows()
	if len(rows) != 2 {
		t.Fatalf("infra nodes after rev1 discovery = %d, want 2", len(rows))
	}
	for _, n := range rows {
		if n.FirstSeenRevisionID != revID1 {
			t.Errorf("node %q first_seen_revision_id = %d, want %d (the scan revision, not 0)", n.NodeKey, n.FirstSeenRevisionID, revID1)
		}
		if n.LastSeenRevisionID != revID1 {
			t.Errorf("node %q last_seen_revision_id = %d, want %d (the scan revision, not 0)", n.NodeKey, n.LastSeenRevisionID, revID1)
		}
	}

	// Case 1: stale_mark AT the creation revision — must not stale a node
	// that was just seen this same revision.
	if _, err := g.store.MarkStaleNodes(domainKey, revID1); err != nil {
		t.Fatalf("MarkStaleNodes at rev1: %v", err)
	}
	for _, n := range infraRows() {
		if n.Status != "active" {
			t.Errorf("node %q status = %q after stale_mark at its OWN revision %d, want active", n.NodeKey, n.Status, revID1)
		}
	}

	// Case 2: a SECOND revision that still declares both infra entries
	// (rediscovery) must re-upsert them so last_seen moves to revID2, and a
	// stale_mark at revID2 must not stale them either.
	revID2, err := g.store.CreateRevision(domainKey, "sha1", "sha2", "manual", "full", "{}")
	if err != nil {
		t.Fatalf("CreateRevision rev2: %v", err)
	}
	if _, err := g.DiscoverFilesOpts(tmpDir, domainKey, revID2, fullManifest, DiscoverOpts{VotesNeeded: 1}); err != nil {
		t.Fatalf("DiscoverFilesOpts rev2: %v", err)
	}
	for _, n := range infraRows() {
		if n.LastSeenRevisionID != revID2 {
			t.Errorf("node %q last_seen_revision_id after rev2 rediscovery = %d, want %d — UpsertNode must bump last_seen on re-upsert", n.NodeKey, n.LastSeenRevisionID, revID2)
		}
	}
	if _, err := g.store.MarkStaleNodes(domainKey, revID2); err != nil {
		t.Fatalf("MarkStaleNodes at rev2: %v", err)
	}
	for _, n := range infraRows() {
		if n.Status != "active" {
			t.Errorf("node %q status = %q after stale_mark at rev2 following rediscovery, want active", n.NodeKey, n.Status)
		}
	}

	// Case 3 (non-vacuous control): a THIRD revision that drops the manifest
	// down to a single infra entry (kafka only, redis removed) must let
	// redis go stale at that revision — proving cases 1/2 pass because of the
	// revision stamp, not because infra nodes are exempt from staleness.
	droppedManifest := &manifest.Manifest{
		Domains: []manifest.DomainEntry{{Name: domainKey, Scan: manifest.ScanConfig{Include: []string{"src/**"}}}},
		Infrastructure: []manifest.InfraEntry{
			{Name: "kafka", Type: "broker", Address: "kafka:9092"},
		},
	}
	revID3, err := g.store.CreateRevision(domainKey, "sha2", "sha3", "manual", "full", "{}")
	if err != nil {
		t.Fatalf("CreateRevision rev3: %v", err)
	}
	if _, err := g.DiscoverFilesOpts(tmpDir, domainKey, revID3, droppedManifest, DiscoverOpts{VotesNeeded: 1}); err != nil {
		t.Fatalf("DiscoverFilesOpts rev3: %v", err)
	}
	staleCount, err := g.store.MarkStaleNodes(domainKey, revID3)
	if err != nil {
		t.Fatalf("MarkStaleNodes at rev3: %v", err)
	}
	if staleCount == 0 {
		t.Fatal("expected the dropped redis infra node to go stale at rev3 — control would pass vacuously otherwise")
	}
	var kafkaStatus, redisStatus string
	for _, n := range infraRows() {
		switch {
		case n.Name == "kafka":
			kafkaStatus = n.Status
		case n.Name == "redis":
			redisStatus = n.Status
		}
	}
	if kafkaStatus != "active" {
		t.Errorf("kafka (still declared at rev3) status = %q, want active", kafkaStatus)
	}
	if redisStatus != "stale" {
		t.Errorf("redis (dropped at rev3) status = %q, want stale", redisStatus)
	}
}

// Self-review finding while implementing the 4-segment InfraNodeKey above:
// ownHostsForDomain used to recover a bare hostname by re-parsing NodeKey —
// a trick that only worked by accident on the OLD 3-segment key, because the
// address happened to land untouched in the domain slot (only lowercased,
// never dot/dash-folded). The new key's name segment folds the port colon
// to a dash and then gets dot/case-folded by validate.NormalizeNodeKey, so
// it can no longer be reverse-parsed back into "kafka-events.internal" —
// re-parsing it would yield the wrong, port-fused string and silently break
// isExternalHost's internal/external classification for any infra address
// with a dotted host or a port. Fixed by stamping the raw address on the
// node's QualifiedName column (manifest.InfraEntry.AddressOrName) and
// reading THAT instead of NodeKey.
func TestOwnHostsForDomain_RecoversDottedPortedInfraAddress(t *testing.T) {
	g := setupGraphDefaults(t)
	revID, err := g.store.CreateRevision("testapp", "", "sha1", "manual", "full", "{}")
	if err != nil {
		t.Fatalf("CreateRevision: %v", err)
	}

	entry := manifest.InfraEntry{Name: "events-kafka", Type: "broker", Address: "kafka-events.internal:9092"}
	validType := registryValidInfraType(g.reg, entry.Type)
	keyEntry := entry
	keyEntry.Type = validType
	if _, err := g.store.UpsertNode(store.NodeRow{
		NodeKey:             canonicalNodeKey(keyEntry.InfraNodeKey("testapp")),
		Layer:               "infra",
		NodeType:            validType,
		QualifiedName:       entry.AddressOrName(),
		DomainKey:           "testapp",
		Name:                entry.Name,
		Status:              "active",
		FirstSeenRevisionID: revID,
		LastSeenRevisionID:  revID,
	}); err != nil {
		t.Fatalf("UpsertNode infra: %v", err)
	}

	own := g.ownHostsForDomain("testapp")
	if !own["kafka-events.internal"] {
		t.Errorf("ownHostsForDomain(%q) = %v, missing the infra address's bare host %q", "testapp", own, "kafka-events.internal")
	}
	if isExternalHost(own, "https://kafka-events.internal:9092/publish") {
		t.Error("isExternalHost classified the domain's own infra address as external — regression in ownHostsForDomain's address recovery")
	}
}
