package store

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// BootstrapJournal exports the current graph state as genesis journal events,
// for databases that predate the event journal (graph data exists, but the
// journal is empty). After it runs, the journal is complete: replaying
// .depbot/events/ into a fresh db reproduces the current graph.
//
// Emission order is nodes → edges → evidence → aliases → glossary; lamports
// are assigned in emission order, so owners replay before dependents. Only
// CURRENT versions are exported (valid_to_revision_id NULL/0) — historical
// versions predate the journal by definition. The surrounding WithTx commit
// auto-flushes the events to files, and FlushJournal marks them applied, so
// a subsequent sync/replay over the same files applies nothing.
//
// Refuses to run if the journal already contains ANY events (outbox rows or
// non-empty events/*.jsonl): genesis events on top of an existing journal
// would double-apply on other machines.
func (s *Store) BootstrapJournal() (int, error) {
	if s.InTx() {
		return 0, fmt.Errorf("BootstrapJournal: must run outside a transaction")
	}
	var outboxRows int
	if err := s.QueryRowScan(`SELECT COUNT(*) FROM journal_outbox`, &outboxRows); err != nil {
		return 0, fmt.Errorf("BootstrapJournal: %w", err)
	}
	if outboxRows > 0 {
		return 0, fmt.Errorf("journal already initialized (%d event(s) in journal_outbox) — use `chronicle journal verify` or `chronicle journal rebuild`", outboxRows)
	}
	eventsDir := filepath.Join(s.Dir(), "events")
	if entries, err := os.ReadDir(eventsDir); err == nil {
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
				continue
			}
			if info, ierr := e.Info(); ierr == nil && info.Size() > 0 {
				return 0, fmt.Errorf("journal already initialized (%s is non-empty) — use `chronicle journal verify` or `chronicle journal rebuild`", filepath.Join(eventsDir, e.Name()))
			}
		}
	}

	total := 0
	err := s.WithTx(func(tx *Store) error {
		for _, step := range []func() (int, error){
			tx.bootstrapNodes, tx.bootstrapEdges, tx.bootstrapEvidence,
			tx.bootstrapAliases, tx.bootstrapGlossary,
		} {
			n, err := step()
			if err != nil {
				return err
			}
			total += n
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return total, nil
}

// bootstrapNodes emits node_upsert for every current node.
func (s *Store) bootstrapNodes() (int, error) {
	rows, err := s.db.Query(`
		SELECT node_key, layer, node_type, domain_key, name,
		       COALESCE(qualified_name,''), COALESCE(repo_name,''), COALESCE(file_path,''),
		       COALESCE(lang,''), COALESCE(owner_key,''), COALESCE(environment,''),
		       COALESCE(visibility,''), status, COALESCE(symbol_name,''), COALESCE(support_kind,''),
		       COALESCE(metadata,'{}'), COALESCE(last_seen_revision_id,0)
		FROM graph_nodes
		WHERE (valid_to_revision_id IS NULL OR valid_to_revision_id = 0)
		ORDER BY node_id`)
	if err != nil {
		return 0, fmt.Errorf("bootstrap nodes: %w", err)
	}
	defer rows.Close()
	var batch []NodeRow
	for rows.Next() {
		var nr NodeRow
		if err := rows.Scan(&nr.NodeKey, &nr.Layer, &nr.NodeType, &nr.DomainKey, &nr.Name,
			&nr.QualifiedName, &nr.RepoName, &nr.FilePath,
			&nr.Lang, &nr.OwnerKey, &nr.Environment,
			&nr.Visibility, &nr.Status, &nr.SymbolName, &nr.SupportKind,
			&nr.Metadata, &nr.LastSeenRevisionID); err != nil {
			return 0, fmt.Errorf("bootstrap nodes scan: %w", err)
		}
		batch = append(batch, nr)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	for _, nr := range batch {
		if err := s.emitNodeUpsert(nr); err != nil {
			return 0, fmt.Errorf("bootstrap node %s: %w", nr.NodeKey, err)
		}
	}
	return len(batch), nil
}

// bootstrapEdges emits edge_upsert for every current edge (the active flag
// travels in the event fields — inactive current edges are graph state too).
// Denormalized from/to keys are backfilled from the endpoint nodes for legacy
// rows; an edge whose endpoint key cannot be resolved fails loud, because the
// resulting event would hard-fail replay on another machine.
func (s *Store) bootstrapEdges() (int, error) {
	rows, err := s.db.Query(`
		SELECT e.edge_key, e.edge_type, e.derivation_kind, COALESCE(e.context_key,''),
		       e.active, COALESCE(e.metadata,'{}'),
		       COALESCE(NULLIF(e.from_node_key,''), fn.node_key, ''),
		       COALESCE(NULLIF(e.to_node_key,''), tn.node_key, ''),
		       COALESCE(e.last_seen_revision_id,0)
		FROM graph_edges e
		LEFT JOIN graph_nodes fn ON fn.node_id = e.from_node_id
		LEFT JOIN graph_nodes tn ON tn.node_id = e.to_node_id
		WHERE (e.valid_to_revision_id IS NULL OR e.valid_to_revision_id = 0)
		ORDER BY e.edge_id`)
	if err != nil {
		return 0, fmt.Errorf("bootstrap edges: %w", err)
	}
	defer rows.Close()
	var batch []EdgeRow
	for rows.Next() {
		var er EdgeRow
		var active int
		if err := rows.Scan(&er.EdgeKey, &er.EdgeType, &er.DerivationKind, &er.ContextKey,
			&active, &er.Metadata, &er.FromNodeKey, &er.ToNodeKey, &er.LastSeenRevisionID); err != nil {
			return 0, fmt.Errorf("bootstrap edges scan: %w", err)
		}
		if er.FromNodeKey == "" || er.ToNodeKey == "" {
			return 0, fmt.Errorf("bootstrap edge %s: endpoint node key unresolved (from=%q to=%q)", er.EdgeKey, er.FromNodeKey, er.ToNodeKey)
		}
		er.Active = active != 0
		batch = append(batch, er)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	for _, er := range batch {
		if err := s.emitEdgeUpsert(er); err != nil {
			return 0, fmt.Errorf("bootstrap edge %s: %w", er.EdgeKey, err)
		}
	}
	return len(batch), nil
}

// bootstrapEvidence emits evidence_add for every current evidence row.
// Subject is the row's evidence_uid; legacy rows without a uid get one
// computed from their dedup tuple and backfilled onto the row (same as
// UpdateEvidenceVerification does).
func (s *Store) bootstrapEvidence() (int, error) {
	rows, err := s.db.Query(`
		SELECT e.evidence_id, e.target_kind, COALESCE(n.node_key, ed.edge_key, ''),
		       e.source_kind, COALESCE(e.repo_name,''), COALESCE(e.file_path,''),
		       COALESCE(e.line_start,0), COALESCE(e.line_end,0),
		       COALESCE(e.column_start,0), COALESCE(e.column_end,0),
		       COALESCE(e.locator,''), e.extractor_id, e.extractor_version,
		       COALESCE(e.ast_rule,''), COALESCE(e.snippet_hash,''), COALESCE(e.commit_sha,''),
		       e.confidence, e.evidence_status, e.evidence_polarity,
		       COALESCE(e.evidence_uid,''),
		       COALESCE(e.assertion,'{}'), COALESCE(e.assertion_kind,''),
		       COALESCE(e.metadata,'{}'), COALESCE(e.valid_from_revision_id,0)
		FROM graph_evidence e
		LEFT JOIN graph_nodes n ON e.node_id = n.node_id
		LEFT JOIN graph_edges ed ON e.edge_id = ed.edge_id
		WHERE (e.valid_to_revision_id IS NULL OR e.valid_to_revision_id = 0)
		ORDER BY e.evidence_id`)
	if err != nil {
		return 0, fmt.Errorf("bootstrap evidence: %w", err)
	}
	defer rows.Close()
	type pending struct {
		id       int64
		uid      string
		needsUID bool
		owner    string
		domain   string
		revID    int64
		fields   map[string]any
	}
	var batch []pending
	for rows.Next() {
		var er EvidenceRow
		var ownerKey, uid string
		if err := rows.Scan(&er.EvidenceID, &er.TargetKind, &ownerKey,
			&er.SourceKind, &er.RepoName, &er.FilePath,
			&er.LineStart, &er.LineEnd,
			&er.ColumnStart, &er.ColumnEnd,
			&er.Locator, &er.ExtractorID, &er.ExtractorVersion,
			&er.ASTRule, &er.SnippetHash, &er.CommitSHA,
			&er.Confidence, &er.EvidenceStatus, &er.EvidencePolarity,
			&uid,
			&er.Assertion, &er.AssertionKind,
			&er.Metadata, &er.ValidFromRevisionID); err != nil {
			return 0, fmt.Errorf("bootstrap evidence scan: %w", err)
		}
		if ownerKey == "" {
			return 0, fmt.Errorf("bootstrap evidence %d: owner node/edge not found", er.EvidenceID)
		}
		p := pending{id: er.EvidenceID, uid: uid, owner: ownerKey, revID: er.ValidFromRevisionID}
		if p.uid == "" {
			p.uid = EvidenceKey(er.TargetKind, ownerKey, er.SourceKind, er.RepoName, er.FilePath, er.LineStart, er.ExtractorID, er.EvidencePolarity)
			p.needsUID = true
		}
		p.domain = DomainFromNodeKey(ownerKey)
		if er.TargetKind == "edge" {
			p.domain = domainFromEdgeKey(ownerKey)
		}
		p.fields = evidenceEventFields(er)
		batch = append(batch, p)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	for _, p := range batch {
		if p.needsUID {
			if _, err := s.db.Exec(`UPDATE graph_evidence SET evidence_uid = ? WHERE evidence_id = ?`, p.uid, p.id); err != nil {
				return 0, fmt.Errorf("bootstrap evidence uid backfill: %w", err)
			}
		}
		if err := s.appendEvent(journalEvent{
			DomainKey: p.domain, RevisionID: p.revID,
			Kind: EvEvidenceAdd, Key: p.uid, OwnerKey: p.owner,
			Fields: p.fields,
		}); err != nil {
			return 0, fmt.Errorf("bootstrap evidence %s: %w", p.uid, err)
		}
	}
	return len(batch), nil
}

// bootstrapAliases emits alias_add for every alias owned by a current node.
func (s *Store) bootstrapAliases() (int, error) {
	rows, err := s.db.Query(`
		SELECT n.node_key, a.alias, a.normalized_alias, a.alias_kind, a.confidence
		FROM node_aliases a
		JOIN graph_nodes n ON n.node_id = a.node_id
		WHERE (n.valid_to_revision_id IS NULL OR n.valid_to_revision_id = 0)
		ORDER BY a.alias_id`)
	if err != nil {
		return 0, fmt.Errorf("bootstrap aliases: %w", err)
	}
	defer rows.Close()
	type pending struct {
		owner, alias, normalized, kind string
		confidence                     float64
	}
	var batch []pending
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.owner, &p.alias, &p.normalized, &p.kind, &p.confidence); err != nil {
			return 0, fmt.Errorf("bootstrap aliases scan: %w", err)
		}
		batch = append(batch, p)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	for _, p := range batch {
		if err := s.appendEvent(journalEvent{
			DomainKey: DomainFromNodeKey(p.owner),
			Kind:      EvAliasAdd,
			Key:       p.owner + "#alias:" + p.normalized,
			OwnerKey:  p.owner,
			Fields: map[string]any{
				"alias": p.alias, "alias_kind": p.kind,
				"confidence": p.confidence, "normalized_alias": p.normalized,
			},
		}); err != nil {
			return len(batch), fmt.Errorf("bootstrap alias %s: %w", p.normalized, err)
		}
	}
	return len(batch), nil
}

// bootstrapGlossary emits glossary_set for every domain term.
func (s *Store) bootstrapGlossary() (int, error) {
	rows, err := s.db.Query(`
		SELECT domain_key, term, COALESCE(aliases,'[]'), COALESCE(anti_patterns,'[]'),
		       COALESCE(description,''), COALESCE(context,''), COALESCE(examples,'[]')
		FROM domain_language ORDER BY term_id`)
	if err != nil {
		return 0, fmt.Errorf("bootstrap glossary: %w", err)
	}
	defer rows.Close()
	type pending struct {
		domain, term, aliases, anti, desc, ctx, examples string
	}
	var batch []pending
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.domain, &p.term, &p.aliases, &p.anti, &p.desc, &p.ctx, &p.examples); err != nil {
			return 0, fmt.Errorf("bootstrap glossary scan: %w", err)
		}
		batch = append(batch, p)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	for _, p := range batch {
		if err := s.appendEvent(journalEvent{
			DomainKey: p.domain,
			Kind:      EvGlossarySet,
			Key:       "term:" + p.domain + ":" + p.term,
			Fields: map[string]any{
				"description":   p.desc,
				"context":       p.ctx,
				"aliases":       p.aliases,
				"anti_patterns": p.anti,
				"examples":      p.examples,
			},
		}); err != nil {
			return len(batch), fmt.Errorf("bootstrap glossary term %s: %w", p.term, err)
		}
	}
	return len(batch), nil
}
