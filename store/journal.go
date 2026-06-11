package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

// Journal event kinds (closed set — replay rejects unknown kinds).
const (
	EvNodeUpsert     = "node_upsert"
	EvNodeStatus     = "node_status"
	EvEdgeUpsert     = "edge_upsert"
	EvEdgeStatus     = "edge_status"
	EvEvidenceAdd    = "evidence_add"
	EvEvidenceStatus = "evidence_status"
	EvAliasAdd       = "alias_add"
	EvAliasDel       = "alias_del"
	EvGlossarySet    = "glossary_set"
	EvGlossaryDel    = "glossary_del"
	EvSnapshot       = "snapshot"
	EvRevisionOpen   = "revision_open"
	EvRevisionClose  = "revision_close"
	// Post-resolve correction events: the resolver's merge/hygiene passes
	// rewrite identity (keys), endpoints, type, and metadata of existing
	// facts instead of upserting new ones — each rewrite is its own kind so
	// replay can re-apply it by natural key.
	EvNodeRekey = "node_rekey" // stem-merge: node re-keyed + its edge keys rewritten
	EvEdgeRekey = "edge_rekey" // edge re-pointed (from/to/type changed) + new edge_key
	EvEdgeMeta  = "edge_meta"  // edge metadata replaced (e.g. unmatched_calls)
)

// journalEvent is one semantic mutation bound for the outbox.
// Key is the subject's natural key; OwnerKey (evidence/alias only) is the
// owning node/edge key. Fields exclude derived values (confidence/freshness/
// trust on nodes and edges).
type journalEvent struct {
	DomainKey  string
	RevisionID int64
	Kind       string
	Key        string
	OwnerKey   string
	Fields     map[string]any
}

// SetJournalActor sets the actor recorded on journal events.
// Callers resolve it once (git user.email, fallback hostname) at startup.
func (s *Store) SetJournalActor(actor string) {
	s.journalActor = actor
}

func (s *Store) actorOrDefault() string {
	if s.journalActor != "" {
		return s.journalActor
	}
	host, err := os.Hostname()
	if err != nil || host == "" {
		return "unknown"
	}
	return host
}

// nextLamport increments and returns the actor's lamport counter.
// Increment-and-read is a single atomic statement (RETURNING) so two
// connections can never observe the same value.
//
// Lamport rule: the next tick must exceed EVERYTHING this db has observed,
// not just this actor's own counter — after a journal merge the MAX-merged
// floor may live on a different actor row (sync-on-open records it under the
// default/hostname actor before the CLI sets the git identity, and merged
// foreign events floor their own actors). The VALUES subquery reads the
// global max at execution time; that value also wins on conflict (it is
// always > the actor's own row), giving MAX semantics in one statement.
func (s *Store) nextLamport(actor string) (int64, error) {
	var l int64
	err := s.db.QueryRow(`
		INSERT INTO journal_state (actor, last_lamport)
		VALUES (?, 1 + COALESCE((SELECT MAX(last_lamport) FROM journal_state), 0))
		ON CONFLICT(actor) DO UPDATE SET last_lamport = excluded.last_lamport
		RETURNING last_lamport`, actor).Scan(&l)
	if err != nil {
		return 0, fmt.Errorf("nextLamport: %w", err)
	}
	return l, nil
}

// appendEvent records a semantic mutation in journal_outbox and graph_changelog,
// in the caller's transaction. A journal failure fails the mutation — the
// journal is a source of truth, not telemetry.
func (s *Store) appendEvent(ev journalEvent) error {
	if s.suppressJournal {
		return nil
	}
	if ev.DomainKey == "" {
		ev.DomainKey = "_unknown"
	}
	actor := s.actorOrDefault()
	lamport, err := s.nextLamport(actor)
	if err != nil {
		return err
	}
	eventID, err := uuid.NewV7()
	if err != nil {
		return fmt.Errorf("appendEvent uuid: %w", err)
	}
	fieldsJSON := "{}"
	if len(ev.Fields) > 0 {
		b, err := json.Marshal(ev.Fields)
		if err != nil {
			return fmt.Errorf("appendEvent fields: %w", err)
		}
		fieldsJSON = string(b)
	}
	_, err = s.db.Exec(`
		INSERT INTO journal_outbox
		  (event_id, lamport, actor, ts, revision_id, domain_key, kind, subject_key, owner_key, fields)
		VALUES (?, ?, ?, strftime('%Y-%m-%dT%H:%M:%SZ','now'), ?, ?, ?, ?, ?, ?)`,
		eventID.String(), lamport, actor,
		nullableInt64(ev.RevisionID), ev.DomainKey, ev.Kind, ev.Key,
		nullableStr(ev.OwnerKey), fieldsJSON,
	)
	if err != nil {
		return fmt.Errorf("appendEvent outbox: %w", err)
	}
	_, err = s.AppendChangelog(ChangelogRow{
		RevisionID:   ev.RevisionID,
		EntityType:   changelogEntityType(ev.Kind),
		EntityKey:    ev.Key,
		ChangeType:   ev.Kind,
		FieldChanges: fieldsJSON,
	})
	if err != nil {
		return fmt.Errorf("appendEvent changelog: %w", err)
	}
	return nil
}

func changelogEntityType(kind string) string {
	switch kind {
	case EvNodeUpsert, EvNodeStatus, EvNodeRekey:
		return "node"
	case EvEdgeUpsert, EvEdgeStatus, EvEdgeRekey, EvEdgeMeta:
		return "edge"
	case EvEvidenceAdd, EvEvidenceStatus:
		return "evidence"
	default:
		return "meta"
	}
}

// EvidenceKey is the stable natural key for an evidence row in journal events.
// It mirrors the AddEvidence dedup tuple (store/evidence.go) and deliberately
// excludes revision: re-observation at a new revision reuses the row.
func EvidenceKey(targetKind, ownerKey, sourceKind, repoName, filePath string, lineStart int, extractorID, polarity string) string {
	h := sha256.Sum256([]byte(strings.Join([]string{
		targetKind, ownerKey, sourceKind, repoName, filePath,
		strconv.Itoa(lineStart), extractorID, polarity,
	}, "\x1f")))
	return "evidence:" + hex.EncodeToString(h[:16])
}

// AppendRevisionClose journals the end of a scan revision.
func (s *Store) AppendRevisionClose(domainKey string, revisionID int64, scanStatus string) error {
	return s.appendEvent(journalEvent{
		DomainKey: domainKey, RevisionID: revisionID, Kind: EvRevisionClose,
		Key:    fmt.Sprintf("revision:%s:%d", domainKey, revisionID),
		Fields: map[string]any{"scan_status": scanStatus},
	})
}

// DomainFromNodeKey extracts the domain segment from layer:type:domain:qualified_name.
func DomainFromNodeKey(nodeKey string) string {
	parts := strings.SplitN(nodeKey, ":", 4)
	if len(parts) == 4 {
		return parts[2]
	}
	return ""
}

// domainFromEdgeKey extracts the source node's domain from from->to:TYPE edge keys.
func domainFromEdgeKey(edgeKey string) string {
	if i := strings.Index(edgeKey, "->"); i > 0 {
		return DomainFromNodeKey(edgeKey[:i])
	}
	return ""
}

// journalSuppressed returns a view of the store whose mutations do not emit
// journal events (used by replay — the events already exist).
func (s *Store) journalSuppressed() *Store {
	c := *s
	c.suppressJournal = true
	return &c
}
