package store

import "fmt"

// FactSchemaVersion is the current version of the graph.jsonl fact schema.
// It mirrors snapshot.FactSchema — store cannot import the snapshot package
// to read it directly without risking an import cycle (snapshot is the
// higher-level package built on top of store), so this constant is the
// store-side source of truth; snapshot.FactSchema is expected to equal it.
const FactSchemaVersion = 1

// CoverageRow is one row of file_coverage: what one extractor saw the last
// time it looked at one file — its outcome (Status) and the git blob of the
// file's content at that moment (BlobSHA).
type CoverageRow struct {
	DomainKey        string
	FilePath         string
	ExtractorID      string
	ExtractorVersion string
	Status           string
	BlobSHA          string
	// ObservedAt is the UTC RFC 3339 time of the observation. UpsertCoverage
	// stamps "now" when it is empty; ListCoverage always fills it.
	ObservedAt string
}

// UpsertCoverage records what an extractor observed the last time it looked
// at a file: BlobSHA is computed from the file on disk at report time (see
// gitutil.BlobSHA / gitutil.FileBlobSHA), Status is the outcome it reported
// (e.g. a scan_extractions status). One row per (domain_key, file_path,
// extractor_id) — re-observation overwrites the previous row in place.
func (s *Store) UpsertCoverage(c CoverageRow) error {
	_, err := s.db.Exec(`
		INSERT INTO file_coverage
		  (domain_key, file_path, extractor_id, extractor_version, status, blob_sha, observed_at)
		VALUES (?, ?, ?, ?, ?, ?, COALESCE(NULLIF(?, ''), strftime('%Y-%m-%dT%H:%M:%SZ','now')))
		ON CONFLICT(domain_key, file_path, extractor_id) DO UPDATE SET
			extractor_version = excluded.extractor_version,
			status            = excluded.status,
			blob_sha          = excluded.blob_sha,
			observed_at       = excluded.observed_at
	`, c.DomainKey, c.FilePath, c.ExtractorID, c.ExtractorVersion, c.Status, c.BlobSHA, c.ObservedAt)
	if err != nil {
		return fmt.Errorf("UpsertCoverage: %w", err)
	}
	return nil
}

// ListCoverage returns file_coverage rows for domainKey, or every domain when
// domainKey is "", ordered by file_path then extractor_id.
func (s *Store) ListCoverage(domainKey string) ([]CoverageRow, error) {
	q := `SELECT domain_key, file_path, extractor_id, extractor_version, status, blob_sha, observed_at
	      FROM file_coverage`
	var args []any
	if domainKey != "" {
		q += ` WHERE domain_key = ?`
		args = append(args, domainKey)
	}
	q += ` ORDER BY file_path, extractor_id`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("ListCoverage: %w", err)
	}
	defer rows.Close()
	var out []CoverageRow
	for rows.Next() {
		var c CoverageRow
		if err := rows.Scan(&c.DomainKey, &c.FilePath, &c.ExtractorID,
			&c.ExtractorVersion, &c.Status, &c.BlobSHA, &c.ObservedAt); err != nil {
			return nil, fmt.Errorf("ListCoverage scan: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ClearCoverageBlob blanks blob_sha for every extractor's coverage row on
// (domainKey, filePath) — the file becomes stale: still a known file, but its
// last-observed content is no longer trusted.
func (s *Store) ClearCoverageBlob(domainKey, filePath string) error {
	_, err := s.db.Exec(`UPDATE file_coverage SET blob_sha = '' WHERE domain_key = ? AND file_path = ?`,
		domainKey, filePath)
	if err != nil {
		return fmt.Errorf("ClearCoverageBlob: %w", err)
	}
	return nil
}
