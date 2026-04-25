package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// DBTX is the interface satisfied by both *sql.DB and *sql.Tx.
type DBTX interface {
	Exec(query string, args ...any) (sql.Result, error)
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
}

type Store struct {
	conn *sql.DB // the underlying connection, nil for tx-based stores
	db   DBTX    // the active executor (db or tx)
}

func Open(dbPath string) (*Store, error) {
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("creating db directory: %w", err)
	}

	db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_foreign_keys=on")
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("pinging database: %w", err)
	}

	s := &Store{conn: db, db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrating database: %w", err)
	}

	return s, nil
}

func (s *Store) Close() error {
	if s.conn != nil {
		return s.conn.Close()
	}
	return nil
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
	return err
}

// ResetDB drops all data and recreates the schema. Use when schema changes.
func (s *Store) ResetDB() error {
	tables := []string{"mcp_request_log", "graph_evidence", "graph_snapshots", "graph_edges", "graph_nodes", "graph_revisions"}
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
  node_key               TEXT NOT NULL UNIQUE,
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
                           CHECK (status IN ('active','stale','deleted','unknown')),
  first_seen_revision_id INTEGER REFERENCES graph_revisions(revision_id),
  last_seen_revision_id  INTEGER REFERENCES graph_revisions(revision_id),
  confidence             REAL NOT NULL DEFAULT 1.0
                           CHECK (confidence >= 0 AND confidence <= 1),
  metadata               TEXT NOT NULL DEFAULT '{}'
);

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
`
