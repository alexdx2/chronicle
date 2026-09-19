package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ErrNotFound is returned when a requested record does not exist.
var ErrNotFound = errors.New("not found")

// Revision represents a row in graph_revisions.
type Revision struct {
	RevisionID   int64  `json:"revision_id"`
	DomainKey    string `json:"domain_key"`
	GitBeforeSHA string `json:"git_before_sha"`
	GitAfterSHA  string `json:"git_after_sha"`
	TriggerKind  string `json:"trigger_kind"`
	Mode         string `json:"mode"`
	CreatedAt    string `json:"created_at"`
	Metadata     string `json:"metadata"`
}

// CreateRevision inserts a new revision and returns the revision_id.
func (s *Store) CreateRevision(domainKey, beforeSHA, afterSHA, triggerKind, mode, metadata string) (int64, error) {
	const q = `
		INSERT INTO graph_revisions (domain_key, git_before_sha, git_after_sha, trigger_kind, mode, metadata)
		VALUES (?, ?, ?, ?, ?, ?)
	`
	res, err := s.db.Exec(q, domainKey, beforeSHA, afterSHA, triggerKind, mode, metadata)
	if err != nil {
		return 0, fmt.Errorf("CreateRevision: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("CreateRevision last insert id: %w", err)
	}
	if err := s.emitRevisionOpen(domainKey, id, afterSHA, mode, triggerKind); err != nil {
		return 0, err
	}
	return id, nil
}

// emitRevisionOpen journals the start of a revision (exactly one event per Create*Revision call).
func (s *Store) emitRevisionOpen(domainKey string, id int64, afterSHA, mode, triggerKind string) error {
	return s.appendEvent(journalEvent{
		DomainKey: domainKey, RevisionID: id, Kind: EvRevisionOpen,
		Key:    fmt.Sprintf("revision:%s:%d", domainKey, id),
		Fields: map[string]any{"after_sha": afterSHA, "mode": mode, "trigger": triggerKind},
	})
}

// CreateRevisionWithContext inserts a new revision linked to a knowledge context and returns the revision_id.
func (s *Store) CreateRevisionWithContext(domainKey, beforeSHA, afterSHA, triggerKind, mode, metadata string, contextID int64) (int64, error) {
	res, err := s.db.Exec(`
		INSERT INTO graph_revisions (domain_key, git_before_sha, git_after_sha, trigger_kind, mode, metadata, context_id)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, domainKey, beforeSHA, afterSHA, triggerKind, mode, metadata, contextID)
	if err != nil {
		return 0, fmt.Errorf("CreateRevisionWithContext: %w", err)
	}
	id, _ := res.LastInsertId()
	if err := s.emitRevisionOpen(domainKey, id, afterSHA, mode, triggerKind); err != nil {
		return 0, err
	}
	return id, nil
}

// revisionCols is the column list every revision query selects, in Revision
// field order.
const revisionCols = `revision_id, domain_key, COALESCE(git_before_sha,''), git_after_sha,
		       trigger_kind, mode, created_at, metadata`

// revisionLayerExpr reads metadata.layer defensively. json_extract raises on
// malformed JSON, which would fail the whole query because of one bad row;
// CASE is documented to short-circuit, so json_extract never sees invalid
// input and a non-JSON metadata simply reads as "no layer".
const revisionLayerExpr = `CASE WHEN json_valid(metadata) THEN json_extract(metadata,'$.layer') ELSE NULL END`

// oneRevision runs a single-row revision query and maps no rows to ErrNotFound.
func (s *Store) oneRevision(what, q string, args ...any) (*Revision, error) {
	r := &Revision{}
	err := s.db.QueryRow(q, args...).Scan(
		&r.RevisionID, &r.DomainKey, &r.GitBeforeSHA, &r.GitAfterSHA,
		&r.TriggerKind, &r.Mode, &r.CreatedAt, &r.Metadata,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%s: %w", what, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", what, err)
	}
	return r, nil
}

// GetLatestRevision returns the most recent revision for a domain.
func (s *Store) GetLatestRevision(domainKey string) (*Revision, error) {
	return s.oneRevision(fmt.Sprintf("GetLatestRevision %q", domainKey),
		`SELECT `+revisionCols+` FROM graph_revisions
		 WHERE domain_key = ? ORDER BY revision_id DESC LIMIT 1`, domainKey)
}

// LatestRevisionAnyDomain returns the most recent revision across all domains
// (GetLatestRevision is domain-scoped via WHERE domain_key = ?, which does not
// match rows with a real domain key when called with "").
func (s *Store) LatestRevisionAnyDomain() (*Revision, error) {
	return s.oneRevision("LatestRevisionAnyDomain",
		`SELECT `+revisionCols+` FROM graph_revisions ORDER BY revision_id DESC LIMIT 1`)
}

// GetRevision returns the revision with the given id, or ErrNotFound if absent.
func (s *Store) GetRevision(id int64) (*Revision, error) {
	return s.oneRevision(fmt.Sprintf("GetRevision %d", id),
		`SELECT `+revisionCols+` FROM graph_revisions WHERE revision_id = ?`, id)
}

// LatestScanRevision is the newest non-refresh, non-layer revision of
// domainKey (empty domainKey = any domain) — the revision that says how old
// the code knowledge is. Refresh revisions (trigger_kind='git_hook') only
// re-verify existing knowledge and layer imports (metadata.layer) only carry
// one layer, so neither may move the scanned SHA. ErrNotFound when none.
func (s *Store) LatestScanRevision(domainKey string) (*Revision, error) {
	q, args := scanRevisionQuery(domainKey)
	q += ` LIMIT 1`
	return s.oneRevision("LatestScanRevision", q, args...)
}

// scanRevisionQuery is the one definition of "a scan revision", newest first.
// LatestScanRevision and ListScanRevisions must not drift apart: the list is
// what a reader falls back through when the newest scan turns out to be
// unusable, so a row missing from one and present in the other would make the
// fallback disagree with the pointer it is meant to replace.
func scanRevisionQuery(domainKey string) (string, []any) {
	q := `SELECT ` + revisionCols + ` FROM graph_revisions
	      WHERE trigger_kind != 'git_hook' AND ` + revisionLayerExpr + ` IS NULL`
	var args []any
	if domainKey != "" {
		q += ` AND domain_key = ?`
		args = append(args, domainKey)
	}
	return q + ` ORDER BY revision_id DESC`, args
}

// ListScanRevisions returns the newest scan revisions of domainKey, newest
// first, at most limit of them (limit <= 0 means a default of 20). Unlike
// LatestScanRevision it returns an empty slice rather than ErrNotFound when
// the domain has never been scanned: "which scans exist" is a question with a
// legitimate empty answer.
func (s *Store) ListScanRevisions(domainKey string, limit int) ([]*Revision, error) {
	if limit <= 0 {
		limit = 20
	}
	q, args := scanRevisionQuery(domainKey)
	q += ` LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("ListScanRevisions: %w", err)
	}
	defer rows.Close()

	var out []*Revision
	for rows.Next() {
		r := &Revision{}
		if err := rows.Scan(
			&r.RevisionID, &r.DomainKey, &r.GitBeforeSHA, &r.GitAfterSHA,
			&r.TriggerKind, &r.Mode, &r.CreatedAt, &r.Metadata,
		); err != nil {
			return nil, fmt.Errorf("ListScanRevisions: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("ListScanRevisions: %w", err)
	}
	return out, nil
}

// revisionKindExpr reads metadata.kind defensively, like revisionLayerExpr.
const revisionKindExpr = `CASE WHEN json_valid(metadata) THEN json_extract(metadata,'$.kind') ELSE NULL END`

// notStructural excludes the structural phase's own revisions. The structural
// phase is hook-driven too (trigger_kind='git_hook'), but it advances a
// different pointer: verification and structure succeed and fail
// independently, and diffing re-verification from a structural revision's SHA
// would silently skip every file between the two.
const notStructural = ` AND (` + revisionKindExpr + ` IS NULL OR ` + revisionKindExpr + ` != 'structural')`

// revisionRefreshExpr reads metadata.refresh the same defensive way
// revisionLayerExpr reads metadata.layer.
const revisionRefreshExpr = `CASE WHEN json_valid(metadata) THEN json_extract(metadata,'$.refresh') ELSE NULL END`

// LatestRefreshRevision is the newest revision that records a re-verification —
// the last time knowledge was checked against a commit without a rescan.
// ErrNotFound when none.
//
// A refresh takes EITHER of two shapes, for the same reason a surface import
// does. A refresh that had to create its own row owns it outright
// (trigger_kind='git_hook', and not the structural phase, which is hook-driven
// too but advances a different pointer). A refresh that found the commit
// already named — by a scan, or by a surface import that got there first —
// cannot own the row, so it merges metadata.refresh instead. Reading only the
// first shape silently drops the second, and the graph reports a commit it did
// re-verify as merely stale.
func (s *Store) LatestRefreshRevision(domainKey string) (*Revision, error) {
	q := `SELECT ` + revisionCols + ` FROM graph_revisions
	      WHERE ((trigger_kind = 'git_hook'` + notStructural + `)
	             OR ` + revisionRefreshExpr + ` IS NOT NULL)`
	var args []any
	if domainKey != "" {
		q += ` AND domain_key = ?`
		args = append(args, domainKey)
	}
	q += ` ORDER BY revision_id DESC LIMIT 1`
	return s.oneRevision("LatestRefreshRevision", q, args...)
}

// revisionLayerExtraExpr reads metadata.layer_extra — "this row also carries
// that layer". A layer marks itself here rather than in metadata.layer whenever
// it does not own the row, either because a code writer got to the commit
// first or because one later took the row over: metadata.layer hides a row from
// LatestScanRevision, so it may only mark a row nobody outranks.
const revisionLayerExtraExpr = `CASE WHEN json_valid(metadata) THEN json_extract(metadata,'$.layer_extra') ELSE NULL END`

// LatestLayerRevision is the newest revision carrying a layer (e.g. a "ui"
// surface import), in EITHER of the two shapes a layer can take. ErrNotFound
// when none.
func (s *Store) LatestLayerRevision(domainKey, layer string) (*Revision, error) {
	q := `SELECT ` + revisionCols + ` FROM graph_revisions
	      WHERE (` + revisionLayerExpr + ` = ? OR ` + revisionLayerExtraExpr + ` = ?)`
	args := []any{layer, layer}
	if domainKey != "" {
		q += ` AND domain_key = ?`
		args = append(args, domainKey)
	}
	q += ` ORDER BY revision_id DESC LIMIT 1`
	return s.oneRevision("LatestLayerRevision", q, args...)
}

// revisionSurfaceExpr reads metadata.surface the same defensive way
// revisionLayerExpr reads metadata.layer.
const revisionSurfaceExpr = `CASE WHEN json_valid(metadata) THEN json_extract(metadata,'$.surface') ELSE NULL END`

// LatestSurfaceRevision is the newest revision that carries a surface import,
// in EITHER of the two shapes an import can take.
//
// An import that had to create its own revision stamps metadata.layer="ui".
// An import onto a commit a scan already named cannot: graph_revisions is
// UNIQUE(domain_key, git_after_sha), so it rides along on the scan's row — and
// stamping layer="ui" there would make LatestScanRevision skip that row and
// lose the code layer's own commit. It merges metadata.surface instead, and
// this is what finds it either way. ErrNotFound when the domain has no
// surface import.
//
// layer_extra is the same fact arrived at from the other direction: an import
// that DID own its row until a scan reached the same commit and took it over.
// Missing it would lose an import purely because the code caught up with it.
func (s *Store) LatestSurfaceRevision(domainKey string) (*Revision, error) {
	q := `SELECT ` + revisionCols + ` FROM graph_revisions
	      WHERE (` + revisionLayerExpr + ` = 'ui'
	             OR ` + revisionLayerExtraExpr + ` = 'ui'
	             OR ` + revisionSurfaceExpr + ` IS NOT NULL)`
	var args []any
	if domainKey != "" {
		q += ` AND domain_key = ?`
		args = append(args, domainKey)
	}
	q += ` ORDER BY revision_id DESC LIMIT 1`
	return s.oneRevision("LatestSurfaceRevision", q, args...)
}

// UpdateRevisionMetadata merges keys into a revision's metadata JSON, leaving
// every key it does not name alone. A revision is written once and then
// described by whoever else lands on the same commit — a surface import onto a
// scan's row must add what it knows without erasing what the scan recorded.
func (s *Store) UpdateRevisionMetadata(revisionID int64, merge map[string]any) error {
	if len(merge) == 0 {
		return nil
	}
	var raw string
	if err := s.db.QueryRow(`SELECT COALESCE(metadata,'{}') FROM graph_revisions WHERE revision_id = ?`,
		revisionID).Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("UpdateRevisionMetadata %d: %w", revisionID, ErrNotFound)
		}
		return fmt.Errorf("UpdateRevisionMetadata %d: %w", revisionID, err)
	}
	md := map[string]any{}
	// Unparseable metadata is replaced rather than propagated: it carries no
	// keys worth preserving, and refusing would block the import over a row
	// somebody else wrote badly.
	_ = json.Unmarshal([]byte(raw), &md)
	for k, v := range merge {
		md[k] = v
	}
	out, err := json.Marshal(md)
	if err != nil {
		return fmt.Errorf("UpdateRevisionMetadata %d: %w", revisionID, err)
	}
	if _, err := s.db.Exec(`UPDATE graph_revisions SET metadata = ? WHERE revision_id = ?`,
		string(out), revisionID); err != nil {
		return fmt.Errorf("UpdateRevisionMetadata %d: %w", revisionID, err)
	}
	return nil
}

// NewestRevisionDomain returns the domain of the newest revision in the
// store — the caller's answer to "which domain is this project" when no
// domain was passed. ErrNotFound when the store holds no revisions.
func (s *Store) NewestRevisionDomain() (string, error) {
	rev, err := s.oneRevision("NewestRevisionDomain",
		`SELECT `+revisionCols+` FROM graph_revisions ORDER BY revision_id DESC LIMIT 1`)
	if err != nil {
		return "", err
	}
	return rev.DomainKey, nil
}

// GetRevisionBySHA returns the revision a domain recorded for one git SHA, or
// ErrNotFound. There is at most one (UNIQUE(domain_key, git_after_sha)), which
// is what lets an importer ask "have I already been told about this commit?"
// before it writes anything.
func (s *Store) GetRevisionBySHA(domainKey, sha string) (*Revision, error) {
	q := `SELECT ` + revisionCols + ` FROM graph_revisions WHERE domain_key = ? AND git_after_sha = ?`
	return s.oneRevision(fmt.Sprintf("GetRevisionBySHA %q %q", domainKey, sha), q, domainKey, sha)
}

// How strongly a writer claims to speak for a commit's CODE knowledge. One
// commit has one row (UNIQUE(domain_key, git_after_sha)) and trigger_kind can
// name only one writer, so the rank decides who that is and everybody else
// describes itself in metadata.
//
// A scan outranks the hook: LatestScanRevision reads trigger_kind, so a scan
// that let a refresh row stand would leave the graph calling a commit it has
// fully read "never scanned". A layer import ranks below both — it speaks for
// one slice of the graph, and promoting it would make a ui import read as the
// commit's code scan.
const (
	revisionRankLayer = 0
	revisionRankHook  = 1
	revisionRankScan  = 2
)

// RevisionClaim is one writer's claim on the revision row naming a commit.
type RevisionClaim struct {
	DomainKey   string
	BeforeSHA   string
	AfterSHA    string
	TriggerKind string
	Mode        string

	// Metadata is the row's metadata when this writer CREATES it.
	Metadata string

	// Merge is what this writer adds when the row already exists, key by key.
	//
	// It is deliberately separate from Metadata. `kind` names the row's
	// primary writer, and merging it onto somebody else's row hides that row
	// from its own reader: a kind="structural" stamp on a refresh row makes
	// LatestRefreshRevision skip it, losing a re-verification that really
	// happened. A writer riding along names itself under its own key instead.
	Merge map[string]any

	// Layer names the single layer this writer imported, when it imported one
	// ("ui" for a surface import). It sets the claim's rank and nothing else.
	Layer string

	// Branch is the branch HEAD was on when this writer ran, recorded as
	// metadata.branch and used only as a label — "this scan was taken on
	// main". It is stored because it cannot be recovered later: the commit
	// alone does not say which branch was checked out, and by the time anyone
	// reads the row the branch may have moved on or been deleted.
	//
	// Only a scan-rank claim records it. A hook refresh or a layer import
	// lands on whatever commit it finds, so its branch says nothing about
	// where the graph's knowledge came from.
	Branch string
}

// branchLabel is the branch this claim may record: only a scan speaks for
// where the graph's code knowledge was read, so only a scan-rank claim
// contributes one.
func (c RevisionClaim) branchLabel() string {
	if c.rank() != revisionRankScan {
		return ""
	}
	return c.Branch
}

// hasBranch reports whether a metadata document already names a branch.
// Unreadable metadata counts as "already named": we do not overwrite what we
// cannot read.
func hasBranch(meta string) bool {
	var m map[string]any
	if err := json.Unmarshal([]byte(meta), &m); err != nil {
		return true
	}
	b, _ := m["branch"].(string)
	return b != ""
}

// withBranch adds metadata.branch to a metadata JSON document, leaving it
// untouched when there is no branch to add or when the document is not JSON we
// can safely rewrite — a metadata string we cannot parse is somebody else's
// record, and dropping it to add a label would be the worse trade.
func withBranch(meta, branch string) string {
	if branch == "" {
		return meta
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(meta), &m); err != nil || m == nil {
		return meta
	}
	if existing, ok := m["branch"].(string); ok && existing != "" {
		return meta
	}
	m["branch"] = branch
	out, err := json.Marshal(m)
	if err != nil {
		return meta
	}
	return string(out)
}

func (c RevisionClaim) rank() int {
	switch {
	case c.Layer != "":
		return revisionRankLayer
	case c.TriggerKind == "git_hook":
		return revisionRankHook
	default:
		return revisionRankScan
	}
}

// revisionRowRank reads an existing row's rank the same way, from what is
// actually stored: metadata.layer is what makes a row a layer row, because it
// is what LatestScanRevision keys on.
func revisionRowRank(r *Revision) int {
	var md struct {
		Layer string `json:"layer"`
	}
	if err := json.Unmarshal([]byte(r.Metadata), &md); err == nil && md.Layer != "" {
		return revisionRankLayer
	}
	if r.TriggerKind == "git_hook" {
		return revisionRankHook
	}
	return revisionRankScan
}

// ClaimRevision returns the revision naming a commit for a domain, creating it
// only when no writer has claimed that commit yet. The bool reports whether the
// row was created.
//
// Every writer that reaches a commit shares one row: a scan, the refresh's
// verification, the structural phase and a surface import can all legitimately
// land on the same SHA, and UNIQUE(domain_key, git_after_sha) means whoever
// calls CreateRevision second gets a constraint error instead of a graph.
// Removing that failure is the point of this method — a writer that finds the
// commit already named joins the row rather than dying on it.
//
// Joining has one rule: trigger_kind and mode carry the strongest claim on the
// commit, and every weaker writer describes itself in metadata. A stronger
// claim also demotes a layer marker to layer_extra, the shape a surface import
// already uses when it rides along on a scan's row — metadata.layer hides a row
// from LatestScanRevision, so it may only mark a row nobody outranks.
func (s *Store) ClaimRevision(c RevisionClaim) (int64, bool, error) {
	if c.AfterSHA == "" {
		return 0, false, fmt.Errorf("ClaimRevision: after SHA is required")
	}

	existing, err := s.GetRevisionBySHA(c.DomainKey, c.AfterSHA)
	switch {
	case errors.Is(err, ErrNotFound):
		meta := c.Metadata
		if strings.TrimSpace(meta) == "" {
			meta = "{}"
		}
		meta = withBranch(meta, c.branchLabel())
		id, cerr := s.CreateRevision(c.DomainKey, c.BeforeSHA, c.AfterSHA, c.TriggerKind, c.Mode, meta)
		if cerr != nil {
			return 0, false, cerr
		}
		return id, true, nil
	case err != nil:
		return 0, false, fmt.Errorf("ClaimRevision: %w", err)
	}

	merge := map[string]any{}
	for k, v := range c.Merge {
		merge[k] = v
	}
	// A scan that joins a row the hook created still has to record where it
	// was read: the commonest flow is a post-commit refresh naming the commit
	// first and the scan arriving second, and a branch label written only on
	// the create path would be missing in exactly that case.
	if b := c.branchLabel(); b != "" && !hasBranch(existing.Metadata) {
		merge["branch"] = b
	}

	if c.rank() > revisionRowRank(existing) {
		if err := s.promoteRevisionClaim(existing, c, merge); err != nil {
			return 0, false, err
		}
	}

	if err := s.UpdateRevisionMetadata(existing.RevisionID, merge); err != nil {
		return 0, false, fmt.Errorf("ClaimRevision: %w", err)
	}
	return existing.RevisionID, false, nil
}

// MergeableRevisionMetadata reads a writer's create-metadata into the keys it
// may also add to a row somebody else created.
//
// Everything survives except `kind`, which names the row's primary writer:
// merging it would rename a row this writer did not create, and the readers
// that select on it (LatestRefreshRevision skipping kind="structural") would
// answer about the wrong writer.
func MergeableRevisionMetadata(metadata string) map[string]any {
	if strings.TrimSpace(metadata) == "" {
		return nil
	}
	md := map[string]any{}
	if err := json.Unmarshal([]byte(metadata), &md); err != nil {
		return nil
	}
	delete(md, "kind")
	if len(md) == 0 {
		return nil
	}
	return md
}

// promoteRevisionClaim hands a commit's row to a stronger writer: it records
// the new trigger_kind/mode, and moves a layer marker out of metadata.layer so
// the promoted claim becomes visible to the reader that asks for it. The layer
// is not forgotten — layer_extra plus the import's own key is exactly how
// LatestSurfaceRevision finds an import that rode along on somebody's row.
func (s *Store) promoteRevisionClaim(existing *Revision, c RevisionClaim, merge map[string]any) error {
	var md struct {
		Layer string `json:"layer"`
	}
	if err := json.Unmarshal([]byte(existing.Metadata), &md); err == nil && md.Layer != "" {
		if _, taken := merge["layer_extra"]; !taken {
			merge["layer_extra"] = md.Layer
		}
		// A JSON null reads back as SQL NULL through json_extract, which is
		// what revisionLayerExpr tests — so this really does clear the marker.
		merge["layer"] = nil
	}

	if _, err := s.db.Exec(
		`UPDATE graph_revisions SET trigger_kind = ?, mode = ? WHERE revision_id = ?`,
		c.TriggerKind, c.Mode, existing.RevisionID); err != nil {
		return fmt.Errorf("ClaimRevision: promote revision %d: %w", existing.RevisionID, err)
	}
	return nil
}
