package store

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

// DBTX is the interface satisfied by both *sql.DB and *sql.Tx.
type DBTX interface {
	Exec(query string, args ...any) (sql.Result, error)
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
}

type Store struct {
	conn   *sql.DB // the underlying connection, nil for tx-based stores
	db     DBTX    // the active executor (db or tx)
	dbPath string  // path to the database file (for reconnect on reset)
}

func Open(dbPath string) (*Store, error) {
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("creating db directory: %w", err)
	}

	db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_foreign_keys=on&_busy_timeout=60000")
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}

	// SQLite WAL allows concurrent reads but single writer.
	// Multiple connections fine — busy_timeout handles write contention.
	db.SetMaxOpenConns(0) // unlimited (default)

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("pinging database: %w", err)
	}

	s := &Store{conn: db, db: db, dbPath: dbPath}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrating database: %w", err)
	}

	return s, nil
}

// Dir returns the .depbot directory containing the database file.
func (s *Store) Dir() string {
	return filepath.Dir(s.dbPath)
}

func (s *Store) Close() error {
	if s.conn != nil {
		return s.conn.Close()
	}
	return nil
}

// reconnectIfNeeded checks if the DB connection is dead and reopens it.
func (s *Store) reconnectIfNeeded(err error) {
	if err == nil || s.dbPath == "" {
		return
	}
	errStr := err.Error()
	if errStr == "sql: database is closed" || errStr == "database is locked (5) (SQLITE_BUSY)" {
		if s.conn != nil {
			s.conn.Close()
		}
		db, openErr := sql.Open("sqlite", s.dbPath+"?_journal_mode=WAL&_foreign_keys=on&_busy_timeout=60000")
		if openErr == nil {
			s.conn = db
			s.db = db
		}
	}
}

// Exec runs a raw SQL statement. Used for post-resolve fixes and migrations.
func (s *Store) Exec(query string, args ...any) error {
	_, err := s.db.Exec(query, args...)
	return err
}

// WithTx runs fn inside a database transaction. If fn returns an error, the tx is rolled back.
func (s *Store) WithTx(fn func(tx *Store) error) error {
	if s.conn == nil {
		return fmt.Errorf("cannot start transaction: no connection (already in tx?)")
	}
	sqlTx, err := s.conn.Begin()
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}

	txStore := &Store{conn: nil, db: sqlTx}

	if err := fn(txStore); err != nil {
		sqlTx.Rollback()
		return err
	}
	return sqlTx.Commit()
}

func (s *Store) migrate() error {
	_, err := s.db.Exec(schema)
	if err != nil {
		return err
	}
	// Best-effort column additions for existing databases.
	// SQLite doesn't support IF NOT EXISTS for ALTER TABLE,
	// so we attempt each and ignore "duplicate column" errors.
	alters := []string{
		`ALTER TABLE graph_evidence ADD COLUMN evidence_status TEXT NOT NULL DEFAULT 'valid'`,
		`ALTER TABLE graph_evidence ADD COLUMN evidence_polarity TEXT NOT NULL DEFAULT 'positive'`,
		`ALTER TABLE graph_evidence ADD COLUMN valid_from_revision_id INTEGER`,
		`ALTER TABLE graph_evidence ADD COLUMN valid_to_revision_id INTEGER`,
		`ALTER TABLE graph_evidence ADD COLUMN last_verified_revision_id INTEGER`,
		`ALTER TABLE graph_evidence ADD COLUMN invalidated_by_revision_id INTEGER`,
		`ALTER TABLE graph_evidence ADD COLUMN invalidated_reason TEXT`,
		`ALTER TABLE graph_edges ADD COLUMN freshness REAL NOT NULL DEFAULT 1.0`,
		`ALTER TABLE graph_edges ADD COLUMN trust_score REAL NOT NULL DEFAULT 1.0`,
		`ALTER TABLE graph_nodes ADD COLUMN freshness REAL NOT NULL DEFAULT 1.0`,
		`ALTER TABLE graph_nodes ADD COLUMN trust_score REAL NOT NULL DEFAULT 1.0`,
		// Revisions get context
		`ALTER TABLE graph_revisions ADD COLUMN context_id INTEGER REFERENCES knowledge_contexts(context_id)`,
		// Nodes get temporal versioning
		`ALTER TABLE graph_nodes ADD COLUMN valid_from_revision_id INTEGER`,
		`ALTER TABLE graph_nodes ADD COLUMN valid_to_revision_id INTEGER`,
		`ALTER TABLE graph_nodes ADD COLUMN context_id INTEGER`,
		// Edges get temporal versioning + stable node_key FKs
		`ALTER TABLE graph_edges ADD COLUMN valid_from_revision_id INTEGER`,
		`ALTER TABLE graph_edges ADD COLUMN valid_to_revision_id INTEGER`,
		`ALTER TABLE graph_edges ADD COLUMN context_id INTEGER`,
		`ALTER TABLE graph_edges ADD COLUMN from_node_key TEXT`,
		`ALTER TABLE graph_edges ADD COLUMN to_node_key TEXT`,
		`ALTER TABLE graph_edges ADD COLUMN verification_status TEXT NOT NULL DEFAULT 'unverified'`,
		// Evidence gets stable identity + context
		`ALTER TABLE graph_evidence ADD COLUMN evidence_uid TEXT`,
		`ALTER TABLE graph_evidence ADD COLUMN context_id INTEGER`,
		// Evidence verification system (assertion-based)
		`ALTER TABLE graph_evidence ADD COLUMN assertion TEXT NOT NULL DEFAULT '{}'`,
		`ALTER TABLE graph_evidence ADD COLUMN assertion_kind TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE graph_evidence ADD COLUMN assertion_version TEXT NOT NULL DEFAULT 'v1'`,
		`ALTER TABLE graph_evidence ADD COLUMN verification_status TEXT NOT NULL DEFAULT 'unverified'`,
		`ALTER TABLE graph_evidence ADD COLUMN verification_reason TEXT NOT NULL DEFAULT ''`,
		// Parallel agent support: claim columns for obligations
		`ALTER TABLE scan_obligations ADD COLUMN claimed_at TEXT`,
		`ALTER TABLE scan_obligations ADD COLUMN claim_expires_at TEXT`,
		// File-level metadata
		`ALTER TABLE scan_extractions ADD COLUMN from_type TEXT NOT NULL DEFAULT ''`,
		// Voting support
		`ALTER TABLE scan_extractions ADD COLUMN extraction_role TEXT NOT NULL DEFAULT 'single'`,
		`ALTER TABLE scan_extractions ADD COLUMN vote_group TEXT`,
		`ALTER TABLE scan_extractions ADD COLUMN vote_index INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE scan_runs ADD COLUMN votes_needed INTEGER NOT NULL DEFAULT 1`,
		// Claim attempt tracking
		`ALTER TABLE scan_obligations ADD COLUMN attempt_count INTEGER NOT NULL DEFAULT 0`,
		// Vote routing for obligations
		`ALTER TABLE scan_obligations ADD COLUMN vote_group TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE scan_obligations ADD COLUMN vote_index INTEGER NOT NULL DEFAULT 0`,
		// Symbol-level metadata on nodes
		`ALTER TABLE graph_nodes ADD COLUMN symbol_name TEXT`,
		`ALTER TABLE graph_nodes ADD COLUMN support_kind TEXT`,
	}
	for _, q := range alters {
		s.db.Exec(q) // ignore errors (column already exists)
	}
	// Drop the old UNIQUE index on node_key (allow multiple versions per key).
	// SQLite auto-generated unique index name is sqlite_autoindex_graph_nodes_1.
	s.db.Exec(`DROP INDEX IF EXISTS sqlite_autoindex_graph_nodes_1`)
	// Also drop if there was a named unique index.
	s.db.Exec(`DROP INDEX IF EXISTS idx_graph_nodes_node_key_unique`)
	s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_graph_nodes_node_key ON graph_nodes(node_key)`)

	// Ensure indexes exist (idempotent).
	s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_graph_evidence_status ON graph_evidence(evidence_status)`)
	s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_graph_evidence_file_status ON graph_evidence(file_path, evidence_status)`)
	s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_nodes_valid_to ON graph_nodes(valid_to_revision_id)`)
	s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_edges_valid_to ON graph_edges(valid_to_revision_id)`)
	s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_evidence_valid_to ON graph_evidence(valid_to_revision_id)`)
	s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_evidence_uid ON graph_evidence(evidence_uid)`)
	s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_edges_from_node_key ON graph_edges(from_node_key)`)
	s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_edges_to_node_key ON graph_edges(to_node_key)`)
	if err := s.migrateScanRunPhases(); err != nil {
		return err
	}
	s.backfillContexts()
	return nil
}

// migrateScanRunPhases recreates scan_runs when the phase CHECK constraint is outdated.
// SQLite cannot ALTER CHECK constraints; table recreation is required.
func (s *Store) migrateScanRunPhases() error {
	var tableSQL string
	if err := s.db.QueryRow(`SELECT sql FROM sqlite_master WHERE type='table' AND name='scan_runs'`).Scan(&tableSQL); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}
	if strings.Contains(tableSQL, "'confirm_scope'") && strings.Contains(tableSQL, "'phase2_confirm'") && strings.Contains(tableSQL, "'phase1_review'") {
		return nil
	}
	_, err := s.db.Exec(`
		CREATE TABLE scan_runs_new (
		    run_id          INTEGER PRIMARY KEY AUTOINCREMENT,
		    revision_id     INTEGER NOT NULL REFERENCES graph_revisions(revision_id),
		    domain_key      TEXT NOT NULL,
		    phase           TEXT NOT NULL DEFAULT 'setup'
		                      CHECK (phase IN ('setup','confirm_scope','phase1_extract','phase1_resolve','phase1_review','endpoint_reconcile','phase2_select','phase2_confirm','phase2_extract','phase2_resolve','finalized')),
		    status          TEXT NOT NULL DEFAULT 'running'
		                      CHECK (status IN ('running','paused','blocked','completed','failed')),
		    total_files     INTEGER NOT NULL DEFAULT 0,
		    extracted_files INTEGER NOT NULL DEFAULT 0,
		    resolved        INTEGER NOT NULL DEFAULT 0,
		    votes_needed    INTEGER NOT NULL DEFAULT 1,
		    created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
		    updated_at      TEXT
		);
		INSERT INTO scan_runs_new
		  SELECT run_id, revision_id, domain_key, phase, status,
		         total_files, extracted_files, resolved, COALESCE(votes_needed, 1),
		         created_at, updated_at
		  FROM scan_runs;
		DROP TABLE scan_runs;
		ALTER TABLE scan_runs_new RENAME TO scan_runs;
		CREATE INDEX IF NOT EXISTS idx_scan_runs_domain_status ON scan_runs(domain_key, status);
	`)
	return err
}

// backfillContexts creates a "main" knowledge context for any domain that has
// revisions but no context yet, and populates the new temporal columns on
// existing rows. This is idempotent: the UNIQUE(domain_key, name) constraint
// plus the NOT EXISTS check prevent duplicate contexts.
func (s *Store) backfillContexts() {
	rows, err := s.db.Query(`
		SELECT DISTINCT r.domain_key FROM graph_revisions r
		WHERE NOT EXISTS (
			SELECT 1 FROM knowledge_contexts c WHERE c.domain_key = r.domain_key
		)
	`)
	if err != nil {
		return
	}
	defer rows.Close()

	var domains []string
	for rows.Next() {
		var d string
		rows.Scan(&d)
		domains = append(domains, d)
	}

	for _, dom := range domains {
		res, err := s.db.Exec(`
			INSERT INTO knowledge_contexts (domain_key, name, git_ref, status)
			VALUES (?, 'main', 'main', 'active')
		`, dom)
		if err != nil {
			continue
		}
		ctxID, _ := res.LastInsertId()

		// Backfill context_id on revisions
		s.db.Exec(`UPDATE graph_revisions SET context_id = ? WHERE domain_key = ? AND context_id IS NULL`, ctxID, dom)

		// Backfill context_id + valid_from on nodes
		s.db.Exec(`UPDATE graph_nodes SET context_id = ?, valid_from_revision_id = first_seen_revision_id WHERE domain_key = ? AND context_id IS NULL`, ctxID, dom)

		// Backfill context_id + valid_from on edges (via from_node's domain)
		s.db.Exec(`
			UPDATE graph_edges SET context_id = ?, valid_from_revision_id = first_seen_revision_id
			WHERE context_id IS NULL AND from_node_id IN (SELECT node_id FROM graph_nodes WHERE domain_key = ?)
		`, ctxID, dom)

		// Backfill from_node_key, to_node_key on edges
		s.db.Exec(`
			UPDATE graph_edges SET
				from_node_key = (SELECT node_key FROM graph_nodes WHERE node_id = graph_edges.from_node_id),
				to_node_key = (SELECT node_key FROM graph_nodes WHERE node_id = graph_edges.to_node_id)
			WHERE from_node_key IS NULL
		`)

		// Backfill evidence context_id
		s.db.Exec(`
			UPDATE graph_evidence SET context_id = ?
			WHERE context_id IS NULL AND (
				node_id IN (SELECT node_id FROM graph_nodes WHERE domain_key = ?)
				OR edge_id IN (SELECT edge_id FROM graph_edges WHERE from_node_id IN (SELECT node_id FROM graph_nodes WHERE domain_key = ?))
			)
		`, ctxID, dom, dom)

		// Update context head to latest revision
		var latestRevID int64
		var latestSHA string
		s.db.QueryRow(`SELECT revision_id, git_after_sha FROM graph_revisions WHERE domain_key = ? ORDER BY revision_id DESC LIMIT 1`, dom).Scan(&latestRevID, &latestSHA)
		if latestRevID > 0 {
			s.db.Exec(`UPDATE knowledge_contexts SET head_revision_id = ?, head_commit_sha = ? WHERE context_id = ?`, latestRevID, latestSHA, ctxID)
		}
	}
}

// ResetDB drops all data and recreates the schema. Use when schema changes.
// If the DB file was deleted externally, this reopens the connection.
// Refuses to reset if a scan is actively running.
func (s *Store) ResetDB() error {
	// Reconnect first — if DB file was deleted or connection is dead, reopen
	if s.conn != nil {
		if err := s.conn.Ping(); err != nil && s.dbPath != "" {
			s.conn.Close()
			db, err := sql.Open("sqlite", s.dbPath+"?_journal_mode=WAL&_foreign_keys=on&_busy_timeout=60000")
			if err != nil {
				return fmt.Errorf("reopening database: %w", err)
			}
			s.conn = db
			s.db = db
		}
	}

	// Auto-close any running scans — user explicitly asked for reset
	s.db.Exec("UPDATE scan_runs SET status='failed' WHERE status='running'")

	tables := []string{"scan_runs", "scan_extractions", "scan_obligations", "evidence_verification_runs", "node_aliases", "graph_changelog", "mcp_request_log", "graph_discoveries", "domain_language", "project_settings", "graph_evidence", "graph_snapshots", "graph_edges", "graph_nodes", "graph_revisions", "knowledge_contexts"}
	for _, t := range tables {
		s.db.Exec("DROP TABLE IF EXISTS " + t)
	}
	return s.migrate()
}

const schema = `
CREATE TABLE IF NOT EXISTS graph_revisions (
  revision_id    INTEGER PRIMARY KEY AUTOINCREMENT,
  domain_key     TEXT NOT NULL,
  git_before_sha TEXT,
  git_after_sha  TEXT NOT NULL,
  trigger_kind   TEXT NOT NULL
                   CHECK (trigger_kind IN (
                     'full_scan','manual','git_hook',
                     'push_webhook','release_webhook','ci_pipeline'
                   )),
  mode           TEXT NOT NULL
                   CHECK (mode IN ('full','incremental')),
  created_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
  metadata       TEXT NOT NULL DEFAULT '{}'
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_graph_revisions_domain_after
  ON graph_revisions(domain_key, git_after_sha);

CREATE TABLE IF NOT EXISTS graph_nodes (
  node_id                INTEGER PRIMARY KEY AUTOINCREMENT,
  node_key               TEXT NOT NULL,
  layer                  TEXT NOT NULL,
  node_type              TEXT NOT NULL,
  domain_key             TEXT NOT NULL,
  name                   TEXT NOT NULL,
  qualified_name         TEXT,
  repo_name              TEXT,
  file_path              TEXT,
  lang                   TEXT,
  owner_key              TEXT,
  environment            TEXT,
  visibility             TEXT,
  status                 TEXT NOT NULL DEFAULT 'active'
                           CHECK (status IN ('active','stale','deleted','unknown','contradicted','external')),
  first_seen_revision_id INTEGER REFERENCES graph_revisions(revision_id),
  last_seen_revision_id  INTEGER REFERENCES graph_revisions(revision_id),
  confidence             REAL NOT NULL DEFAULT 1.0
                           CHECK (confidence >= 0 AND confidence <= 1),
  freshness              REAL NOT NULL DEFAULT 1.0
                           CHECK (freshness >= 0 AND freshness <= 1),
  trust_score            REAL NOT NULL DEFAULT 1.0
                           CHECK (trust_score >= 0 AND trust_score <= 1),
  metadata               TEXT NOT NULL DEFAULT '{}',
  symbol_name            TEXT,
  support_kind           TEXT
);

CREATE INDEX IF NOT EXISTS idx_graph_nodes_node_key ON graph_nodes(node_key);
CREATE INDEX IF NOT EXISTS idx_graph_nodes_layer_type ON graph_nodes(layer, node_type);
CREATE INDEX IF NOT EXISTS idx_graph_nodes_domain ON graph_nodes(domain_key);
CREATE INDEX IF NOT EXISTS idx_graph_nodes_repo_path ON graph_nodes(repo_name, file_path);

CREATE TABLE IF NOT EXISTS graph_edges (
  edge_id                INTEGER PRIMARY KEY AUTOINCREMENT,
  edge_key               TEXT NOT NULL UNIQUE,
  from_node_id           INTEGER NOT NULL REFERENCES graph_nodes(node_id),
  to_node_id             INTEGER NOT NULL REFERENCES graph_nodes(node_id),
  edge_type              TEXT NOT NULL,
  derivation_kind        TEXT NOT NULL
                           CHECK (derivation_kind IN ('hard','linked','inferred','unknown')),
  context_key            TEXT,
  active                 INTEGER NOT NULL DEFAULT 1,
  first_seen_revision_id INTEGER REFERENCES graph_revisions(revision_id),
  last_seen_revision_id  INTEGER REFERENCES graph_revisions(revision_id),
  confidence             REAL NOT NULL DEFAULT 1.0
                           CHECK (confidence >= 0 AND confidence <= 1),
  freshness              REAL NOT NULL DEFAULT 1.0
                           CHECK (freshness >= 0 AND freshness <= 1),
  trust_score            REAL NOT NULL DEFAULT 1.0
                           CHECK (trust_score >= 0 AND trust_score <= 1),
  metadata               TEXT NOT NULL DEFAULT '{}'
);

CREATE INDEX IF NOT EXISTS idx_graph_edges_from ON graph_edges(from_node_id);
CREATE INDEX IF NOT EXISTS idx_graph_edges_to ON graph_edges(to_node_id);
CREATE INDEX IF NOT EXISTS idx_graph_edges_type ON graph_edges(edge_type);

CREATE TABLE IF NOT EXISTS graph_evidence (
  evidence_id      INTEGER PRIMARY KEY AUTOINCREMENT,
  target_kind      TEXT NOT NULL
                     CHECK (target_kind IN ('node','edge')),
  node_id          INTEGER REFERENCES graph_nodes(node_id),
  edge_id          INTEGER REFERENCES graph_edges(edge_id),
  source_kind      TEXT NOT NULL,
  repo_name        TEXT,
  file_path        TEXT,
  line_start       INTEGER,
  line_end         INTEGER,
  column_start     INTEGER,
  column_end       INTEGER,
  locator          TEXT,
  extractor_id     TEXT NOT NULL,
  extractor_version TEXT NOT NULL,
  ast_rule         TEXT,
  snippet_hash     TEXT,
  commit_sha       TEXT,
  observed_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
  verified_at      TEXT,
  confidence       REAL NOT NULL DEFAULT 1.0
                     CHECK (confidence >= 0 AND confidence <= 1),
  evidence_status  TEXT NOT NULL DEFAULT 'valid'
                     CHECK (evidence_status IN ('valid','stale','revalidated','invalidated','superseded')),
  evidence_polarity TEXT NOT NULL DEFAULT 'positive'
                     CHECK (evidence_polarity IN ('positive','negative')),
  valid_from_revision_id  INTEGER REFERENCES graph_revisions(revision_id),
  valid_to_revision_id    INTEGER REFERENCES graph_revisions(revision_id),
  last_verified_revision_id INTEGER REFERENCES graph_revisions(revision_id),
  invalidated_by_revision_id INTEGER REFERENCES graph_revisions(revision_id),
  invalidated_reason TEXT,
  metadata         TEXT NOT NULL DEFAULT '{}',
  CHECK (
    (target_kind = 'node' AND node_id IS NOT NULL AND edge_id IS NULL) OR
    (target_kind = 'edge' AND edge_id IS NOT NULL AND node_id IS NULL)
  )
);

CREATE INDEX IF NOT EXISTS idx_graph_evidence_node ON graph_evidence(node_id);
CREATE INDEX IF NOT EXISTS idx_graph_evidence_edge ON graph_evidence(edge_id);
CREATE INDEX IF NOT EXISTS idx_graph_evidence_source ON graph_evidence(source_kind, repo_name, file_path);

CREATE TABLE IF NOT EXISTS graph_snapshots (
  snapshot_id        INTEGER PRIMARY KEY AUTOINCREMENT,
  revision_id        INTEGER NOT NULL REFERENCES graph_revisions(revision_id),
  domain_key         TEXT NOT NULL,
  snapshot_kind      TEXT NOT NULL
                       CHECK (snapshot_kind IN ('full','incremental')),
  created_at         TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
  node_count         INTEGER NOT NULL,
  edge_count         INTEGER NOT NULL,
  changed_file_count INTEGER NOT NULL DEFAULT 0,
  changed_node_count INTEGER NOT NULL DEFAULT 0,
  changed_edge_count INTEGER NOT NULL DEFAULT 0,
  impacted_node_count INTEGER NOT NULL DEFAULT 0,
  summary            TEXT NOT NULL DEFAULT '{}',
  UNIQUE (revision_id, domain_key)
);

CREATE INDEX IF NOT EXISTS idx_graph_snapshots_domain_created
  ON graph_snapshots(domain_key, created_at DESC);

CREATE TABLE IF NOT EXISTS mcp_request_log (
  request_id     INTEGER PRIMARY KEY AUTOINCREMENT,
  timestamp      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
  tool_name      TEXT NOT NULL,
  params_json    TEXT NOT NULL DEFAULT '{}',
  result_json    TEXT,
  error_message  TEXT,
  duration_ms    INTEGER NOT NULL DEFAULT 0,
  summary        TEXT
);

CREATE INDEX IF NOT EXISTS idx_mcp_request_log_timestamp
  ON mcp_request_log(timestamp DESC);

CREATE TABLE IF NOT EXISTS graph_discoveries (
  discovery_id   INTEGER PRIMARY KEY AUTOINCREMENT,
  domain_key     TEXT NOT NULL,
  category       TEXT NOT NULL,
  title          TEXT NOT NULL,
  description    TEXT NOT NULL,
  source         TEXT NOT NULL DEFAULT 'claude',
  confidence     REAL NOT NULL DEFAULT 0.5,
  related_nodes  TEXT NOT NULL DEFAULT '[]',
  applied        INTEGER NOT NULL DEFAULT 0,
  created_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);

CREATE INDEX IF NOT EXISTS idx_graph_discoveries_domain
  ON graph_discoveries(domain_key, category);

CREATE TABLE IF NOT EXISTS project_settings (
  key    TEXT PRIMARY KEY,
  value  TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS domain_language (
  term_id        INTEGER PRIMARY KEY AUTOINCREMENT,
  domain_key     TEXT NOT NULL,
  term           TEXT NOT NULL,
  aliases        TEXT NOT NULL DEFAULT '[]',
  anti_patterns  TEXT NOT NULL DEFAULT '[]',
  description    TEXT NOT NULL DEFAULT '',
  context        TEXT NOT NULL DEFAULT '',
  examples       TEXT NOT NULL DEFAULT '[]',
  created_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
  updated_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
  UNIQUE(domain_key, term)
);

CREATE TABLE IF NOT EXISTS diagram_sessions (
  session_id   TEXT PRIMARY KEY,
  title        TEXT NOT NULL DEFAULT '',
  data         TEXT NOT NULL DEFAULT '{}',
  created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
  updated_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);

CREATE TABLE IF NOT EXISTS knowledge_contexts (
    context_id        INTEGER PRIMARY KEY AUTOINCREMENT,
    domain_key        TEXT NOT NULL,
    name              TEXT NOT NULL,
    git_ref           TEXT,
    base_context_id   INTEGER REFERENCES knowledge_contexts(context_id),
    base_revision_id  INTEGER REFERENCES graph_revisions(revision_id),
    head_revision_id  INTEGER REFERENCES graph_revisions(revision_id),
    head_commit_sha   TEXT,
    status            TEXT NOT NULL DEFAULT 'active'
                        CHECK (status IN ('active','merged','archived')),
    created_at        TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    UNIQUE (domain_key, name)
);

CREATE TABLE IF NOT EXISTS graph_changelog (
    changelog_id  INTEGER PRIMARY KEY AUTOINCREMENT,
    revision_id   INTEGER NOT NULL REFERENCES graph_revisions(revision_id),
    context_id    INTEGER NOT NULL REFERENCES knowledge_contexts(context_id),
    entity_type   TEXT NOT NULL CHECK (entity_type IN ('node','edge','evidence')),
    entity_key    TEXT NOT NULL,
    entity_id     INTEGER,
    change_type   TEXT NOT NULL
                    CHECK (change_type IN ('created','updated','stale','invalidated','revalidated','deleted')),
    field_changes TEXT,
    created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);

CREATE INDEX IF NOT EXISTS idx_changelog_revision ON graph_changelog(revision_id);
CREATE INDEX IF NOT EXISTS idx_changelog_context ON graph_changelog(context_id);
CREATE INDEX IF NOT EXISTS idx_changelog_entity ON graph_changelog(entity_type, entity_key);

CREATE TABLE IF NOT EXISTS node_aliases (
    alias_id         INTEGER PRIMARY KEY AUTOINCREMENT,
    node_id          INTEGER NOT NULL REFERENCES graph_nodes(node_id),
    alias            TEXT    NOT NULL,
    normalized_alias TEXT    NOT NULL,
    alias_kind       TEXT    NOT NULL,
    confidence       REAL    NOT NULL DEFAULT 0.8,
    UNIQUE(node_id, normalized_alias, alias_kind)
);

CREATE INDEX IF NOT EXISTS idx_node_aliases_normalized ON node_aliases(normalized_alias, alias_kind);
CREATE INDEX IF NOT EXISTS idx_node_aliases_node_id ON node_aliases(node_id);

CREATE TABLE IF NOT EXISTS scan_obligations (
    obligation_id    INTEGER PRIMARY KEY AUTOINCREMENT,
    revision_id      INTEGER NOT NULL REFERENCES graph_revisions(revision_id),
    domain_key       TEXT NOT NULL,
    obligation_type  TEXT NOT NULL,
    target_key       TEXT NOT NULL,
    reason           TEXT,
    status           TEXT NOT NULL DEFAULT 'open'
                       CHECK (status IN ('open','satisfied','skipped','deferred')),
    defer_reason     TEXT,
    claimed_at       TEXT,
    claim_expires_at TEXT,
    attempt_count    INTEGER NOT NULL DEFAULT 0,
    vote_group       TEXT NOT NULL DEFAULT '',
    vote_index       INTEGER NOT NULL DEFAULT 0,
    created_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    resolved_at      TEXT
);

CREATE INDEX IF NOT EXISTS idx_scan_obligations_revision ON scan_obligations(revision_id, status);
CREATE INDEX IF NOT EXISTS idx_scan_obligations_target ON scan_obligations(target_key);

CREATE TABLE IF NOT EXISTS evidence_verification_runs (
    run_id            INTEGER PRIMARY KEY AUTOINCREMENT,
    revision_id       INTEGER NOT NULL REFERENCES graph_revisions(revision_id),
    domain_key        TEXT NOT NULL,
    file_path         TEXT NOT NULL,
    verifier_id       TEXT NOT NULL,
    verifier_version  TEXT NOT NULL,
    status            TEXT NOT NULL
                        CHECK (status IN ('succeeded','failed','skipped','unsupported')),
    evidence_checked  INTEGER NOT NULL DEFAULT 0,
    valid_count       INTEGER NOT NULL DEFAULT 0,
    moved_count       INTEGER NOT NULL DEFAULT 0,
    missing_count     INTEGER NOT NULL DEFAULT 0,
    changed_count     INTEGER NOT NULL DEFAULT 0,
    ambiguous_count   INTEGER NOT NULL DEFAULT 0,
    started_at        TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    finished_at       TEXT
);

CREATE INDEX IF NOT EXISTS idx_verification_runs_revision ON evidence_verification_runs(revision_id, file_path);

CREATE TABLE IF NOT EXISTS scan_extractions (
    extraction_id   INTEGER PRIMARY KEY AUTOINCREMENT,
    revision_id     INTEGER NOT NULL REFERENCES graph_revisions(revision_id),
    domain_key      TEXT NOT NULL,
    file_path       TEXT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'extracted'
                      CHECK (status IN ('extracted','no_runtime_architecture','config_only','type_only','generated','skipped','failed','resolved')),
    from_type       TEXT NOT NULL DEFAULT '',
    extraction_role TEXT NOT NULL DEFAULT 'single',
    vote_group      TEXT,
    vote_index      INTEGER NOT NULL DEFAULT 0,
    facts_json      TEXT NOT NULL DEFAULT '[]',
    error_message   TEXT,
    created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);

CREATE INDEX IF NOT EXISTS idx_scan_extractions_revision ON scan_extractions(revision_id, domain_key);
CREATE INDEX IF NOT EXISTS idx_scan_extractions_file ON scan_extractions(file_path, revision_id);

CREATE TABLE IF NOT EXISTS scan_runs (
    run_id          INTEGER PRIMARY KEY AUTOINCREMENT,
    revision_id     INTEGER NOT NULL REFERENCES graph_revisions(revision_id),
    domain_key      TEXT NOT NULL,
    phase           TEXT NOT NULL DEFAULT 'setup'
                      CHECK (phase IN ('setup','confirm_scope','phase1_extract','phase1_resolve','endpoint_reconcile','phase2_select','phase2_confirm','phase2_extract','phase2_resolve','finalized')),
    status          TEXT NOT NULL DEFAULT 'running'
                      CHECK (status IN ('running','paused','blocked','completed','failed')),
    total_files     INTEGER NOT NULL DEFAULT 0,
    extracted_files INTEGER NOT NULL DEFAULT 0,
    resolved        INTEGER NOT NULL DEFAULT 0,
    votes_needed    INTEGER NOT NULL DEFAULT 1,
    created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
    updated_at      TEXT
);

CREATE INDEX IF NOT EXISTS idx_scan_runs_domain_status ON scan_runs(domain_key, status);
`

// SaveDiagramSession upserts a diagram session as a JSON blob.
func (s *Store) SaveDiagramSession(sessionID, title, dataJSON string) error {
	_, err := s.db.Exec(`
		INSERT INTO diagram_sessions (session_id, title, data, updated_at)
		VALUES (?, ?, ?, strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
		ON CONFLICT(session_id) DO UPDATE SET
			title = excluded.title,
			data = excluded.data,
			updated_at = strftime('%Y-%m-%dT%H:%M:%SZ', 'now')
	`, sessionID, title, dataJSON)
	return err
}

// GetDiagramSession returns the JSON blob for a diagram session.
func (s *Store) GetDiagramSession(sessionID string) (title, dataJSON string, err error) {
	err = s.db.QueryRow(`SELECT title, data FROM diagram_sessions WHERE session_id = ?`, sessionID).Scan(&title, &dataJSON)
	return
}

// DeleteDiagramSession removes a diagram session.
func (s *Store) DeleteDiagramSession(sessionID string) error {
	_, err := s.db.Exec(`DELETE FROM diagram_sessions WHERE session_id = ?`, sessionID)
	return err
}

// ListDiagramSessions returns all session IDs and titles.
func (s *Store) ListDiagramSessions() ([]map[string]string, error) {
	rows, err := s.db.Query(`SELECT session_id, title, created_at, updated_at FROM diagram_sessions ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []map[string]string
	for rows.Next() {
		var id, title, created, updated string
		rows.Scan(&id, &title, &created, &updated)
		result = append(result, map[string]string{"session_id": id, "title": title, "created_at": created, "updated_at": updated})
	}
	return result, nil
}
