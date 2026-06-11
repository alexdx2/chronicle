package store

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ReplayResult summarizes a journal replay.
type ReplayResult struct {
	Applied int `json:"applied"`
	Skipped int `json:"skipped"` // duplicate event_id
}

// replayEvent pairs a wire event with the domain of the file it came from
// (<domain>.jsonl) — used for journal_applied_events bookkeeping.
type replayEvent struct {
	flushedEvent
	domain string
}

// ReplayJournal reads all *.jsonl files in eventsDir, sorts events by
// (lamport, actor), and applies unseen ones (journal_applied_events dedup).
// Replay does NOT re-journal: applied mutations bypass appendEvent.
// Trust/confidence are derived values and are not replayed — recompute them
// separately (graph.RecalculateAllTrust) after replay.
func (s *Store) ReplayJournal(eventsDir string) (*ReplayResult, error) {
	entries, err := os.ReadDir(eventsDir)
	if err != nil {
		return nil, fmt.Errorf("ReplayJournal: %w", err)
	}
	var events []replayEvent
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		domain := strings.TrimSuffix(e.Name(), ".jsonl")
		f, err := os.Open(filepath.Join(eventsDir, e.Name()))
		if err != nil {
			return nil, err
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 1024*1024), 1024*1024)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" {
				continue
			}
			var ev flushedEvent
			if err := json.Unmarshal([]byte(line), &ev); err != nil {
				f.Close()
				return nil, fmt.Errorf("ReplayJournal: bad line in %s: %w", e.Name(), err)
			}
			events = append(events, replayEvent{flushedEvent: ev, domain: domain})
		}
		f.Close()
		if err := sc.Err(); err != nil {
			return nil, err
		}
	}

	sort.SliceStable(events, func(i, j int) bool {
		if events[i].Lamport != events[j].Lamport {
			return events[i].Lamport < events[j].Lamport
		}
		return events[i].Actor < events[j].Actor
	})

	// Lamport floor: after merging someone's journal, our next events must sort
	// after theirs — max(seen) feeds "max(seen)+1" in nextLamport.
	var maxLamport int64
	for _, ev := range events {
		if ev.Lamport > maxLamport {
			maxLamport = ev.Lamport
		}
	}

	res := &ReplayResult{}
	err = s.WithTx(func(tx *Store) error {
		txq := tx.journalSuppressed()
		for _, ev := range events {
			var seen int
			if err := txq.db.QueryRow(
				`SELECT COUNT(*) FROM journal_applied_events WHERE event_id = ?`, ev.EventID,
			).Scan(&seen); err != nil {
				return fmt.Errorf("ReplayJournal dedup lookup: %w", err)
			}
			if seen > 0 {
				res.Skipped++
				continue
			}
			if err := txq.applyJournalEvent(ev.flushedEvent); err != nil {
				return fmt.Errorf("applying %s %s: %w", ev.Kind, ev.Key, err)
			}
			domain := ev.domain
			if domain == "" {
				domain = DomainFromNodeKey(ev.Key)
			}
			if domain == "" {
				domain = "_unknown"
			}
			if _, err := txq.db.Exec(`
				INSERT INTO journal_applied_events (event_id, domain_key, lamport, actor, kind, applied_at)
				VALUES (?, ?, ?, ?, ?, strftime('%Y-%m-%dT%H:%M:%SZ','now'))`,
				ev.EventID, domain, ev.Lamport, ev.Actor, ev.Kind); err != nil {
				return fmt.Errorf("ReplayJournal record applied: %w", err)
			}
			res.Applied++
		}
		if maxLamport > 0 {
			if _, err := txq.db.Exec(`
				INSERT INTO journal_state (actor, last_lamport) VALUES (?, ?)
				ON CONFLICT(actor) DO UPDATE SET last_lamport = MAX(last_lamport, excluded.last_lamport)`,
				s.actorOrDefault(), maxLamport); err != nil {
				return fmt.Errorf("ReplayJournal lamport floor: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return res, nil
}

// applyJournalEvent applies one semantic event by natural key. The receiver
// MUST be journal-suppressed — mutations here must not re-journal.
func (s *Store) applyJournalEvent(ev flushedEvent) error {
	var fields map[string]any
	if len(ev.Fields) > 0 {
		if err := json.Unmarshal(ev.Fields, &fields); err != nil {
			return err
		}
	}
	str := func(k string) string {
		v, _ := fields[k].(string)
		return v
	}
	f64 := func(k string) float64 {
		v, _ := fields[k].(float64) // JSON numbers decode as float64
		return v
	}

	switch ev.Kind {
	case EvRevisionOpen, EvRevisionClose, EvSnapshot:
		return nil // bookkeeping; revisions/snapshots rebuild via re-scan, not replay

	case EvNodeUpsert:
		_, err := s.UpsertNode(NodeRow{
			NodeKey: ev.Key, Layer: str("layer"), NodeType: str("node_type"),
			DomainKey: str("domain_key"), Name: str("name"),
			QualifiedName: str("qualified_name"), RepoName: str("repo_name"),
			FilePath: str("file_path"), Lang: str("lang"), OwnerKey: str("owner_key"),
			Environment: str("environment"), Visibility: str("visibility"),
			Status: str("status"), SymbolName: str("symbol_name"), SupportKind: str("support_kind"),
			Metadata:   defaultStr(str("metadata"), "{}"),
			Confidence: 1.0, Freshness: 1.0, TrustScore: 1.0, // derived — recomputed after replay
		})
		return err

	case EvNodeStatus:
		_, err := s.db.Exec(`UPDATE graph_nodes SET status = ? WHERE node_key = ?
			AND (valid_to_revision_id IS NULL OR valid_to_revision_id = 0)`, str("status"), ev.Key)
		return err

	case EvNodeRekey:
		// ev.Key is the OLD node key — resolve the node by it, then re-apply
		// the same rewrites as Store.RekeyNode (node key + denormalized edge keys).
		nodeID, err := s.GetNodeIDByKey(ev.Key)
		if err != nil {
			return fmt.Errorf("node_rekey %s: %w", ev.Key, err)
		}
		newKey := str("new_key")
		if _, err := s.db.Exec(
			`UPDATE graph_nodes SET node_key = ?, file_path = ? WHERE node_id = ?`,
			newKey, nullableStr(str("file_path")), nodeID); err != nil {
			return fmt.Errorf("node_rekey %s: %w", ev.Key, err)
		}
		return s.rekeyNodeEdges(nodeID, newKey)

	case EvEdgeRekey:
		// ev.Key is the OLD edge key; from/to/edge_type fields are present only
		// when they changed. Node ids are resolved by key — ids differ per db.
		sets := []string{"edge_key = ?"}
		args := []any{str("new_edge_key")}
		if from := str("from"); from != "" {
			fromID, err := s.GetNodeIDByKey(from)
			if err != nil {
				return fmt.Errorf("edge_rekey %s: from %q: %w", ev.Key, from, err)
			}
			sets = append(sets, "from_node_key = ?", "from_node_id = ?")
			args = append(args, from, fromID)
		}
		if to := str("to"); to != "" {
			toID, err := s.GetNodeIDByKey(to)
			if err != nil {
				return fmt.Errorf("edge_rekey %s: to %q: %w", ev.Key, to, err)
			}
			sets = append(sets, "to_node_key = ?", "to_node_id = ?")
			args = append(args, to, toID)
		}
		if et := str("edge_type"); et != "" {
			sets = append(sets, "edge_type = ?")
			args = append(args, et)
		}
		args = append(args, ev.Key)
		_, err := s.db.Exec(`UPDATE graph_edges SET `+strings.Join(sets, ", ")+`
			WHERE edge_key = ? AND (valid_to_revision_id IS NULL OR valid_to_revision_id = 0)`, args...)
		return err

	case EvEdgeMeta:
		_, err := s.db.Exec(`UPDATE graph_edges SET metadata = ? WHERE edge_key = ?
			AND (valid_to_revision_id IS NULL OR valid_to_revision_id = 0)`, str("metadata"), ev.Key)
		return err

	case EvEdgeUpsert:
		fromID, _ := s.GetNodeIDByKey(str("from"))
		toID, _ := s.GetNodeIDByKey(str("to"))
		if fromID == 0 || toID == 0 {
			return fmt.Errorf("edge %s: endpoint nodes not found (from=%s to=%s)", ev.Key, str("from"), str("to"))
		}
		active, _ := fields["active"].(bool)
		_, err := s.UpsertEdge(EdgeRow{
			EdgeKey: ev.Key, FromNodeID: fromID, ToNodeID: toID,
			EdgeType: str("edge_type"), DerivationKind: str("derivation_kind"),
			ContextKey: str("context_key"), Active: active,
			Metadata:    defaultStr(str("metadata"), "{}"),
			FromNodeKey: str("from"), ToNodeKey: str("to"),
			Confidence: 1.0, Freshness: 1.0, TrustScore: 1.0,
		})
		return err

	case EvEdgeStatus:
		active := 0
		if a, ok := fields["active"].(bool); ok && a {
			active = 1
		}
		_, err := s.db.Exec(`UPDATE graph_edges SET active = ? WHERE edge_key = ?
			AND (valid_to_revision_id IS NULL OR valid_to_revision_id = 0)`, active, ev.Key)
		return err

	case EvEvidenceAdd:
		row := EvidenceRow{
			TargetKind:       str("target_kind"),
			SourceKind:       str("source_kind"),
			RepoName:         str("repo_name"),
			FilePath:         str("file_path"),
			LineStart:        int(f64("line_start")),
			LineEnd:          int(f64("line_end")),
			ColumnStart:      int(f64("column_start")),
			ColumnEnd:        int(f64("column_end")),
			Locator:          str("locator"),
			ExtractorID:      str("extractor_id"),
			ExtractorVersion: str("extractor_version"),
			ASTRule:          str("ast_rule"),
			SnippetHash:      str("snippet_hash"),
			CommitSHA:        str("commit_sha"),
			Confidence:       f64("confidence"),
			EvidencePolarity: str("polarity"),
			EvidenceStatus:   str("status"),
			Assertion:        str("assertion"),
			AssertionKind:    str("assertion_kind"),
			// ev.Key is the source row's stable identity — pass it explicitly so
			// the replayed row carries the SAME uid even if tuple hashing changes.
			EvidenceUID: ev.Key,
			Metadata:    defaultStr(str("metadata"), "{}"),
		}
		if row.TargetKind == "edge" {
			edge, err := s.GetEdgeByKey(ev.OwnerKey)
			if err != nil {
				return fmt.Errorf("evidence %s: owner edge %q: %w", ev.Key, ev.OwnerKey, err)
			}
			row.EdgeID = edge.EdgeID
		} else {
			nodeID, err := s.GetNodeIDByKey(ev.OwnerKey)
			if err != nil {
				return fmt.Errorf("evidence %s: owner node %q: %w", ev.Key, ev.OwnerKey, err)
			}
			row.NodeID = nodeID
		}
		_, err := s.AddEvidence(row)
		return err

	case EvEvidenceStatus:
		// Optional fields apply only when present in the event (NULL → COALESCE
		// keeps the row's value): re-observation carries confidence, mechanical
		// verification moves line_start/line_end.
		var conf *float64
		if v, ok := fields["confidence"].(float64); ok {
			conf = &v
		}
		var lineStart, lineEnd *int
		if v, ok := fields["line_start"].(float64); ok {
			n := int(v)
			lineStart = &n
		}
		if v, ok := fields["line_end"].(float64); ok {
			n := int(v)
			lineEnd = &n
		}
		_, err := s.db.Exec(`UPDATE graph_evidence
			SET evidence_status = ?,
			    confidence = COALESCE(?, confidence),
			    line_start = COALESCE(?, line_start),
			    line_end = COALESCE(?, line_end)
			WHERE evidence_uid = ?`,
			str("status"), conf, lineStart, lineEnd, ev.Key)
		return err

	case EvAliasAdd:
		nodeID, err := s.GetNodeIDByKey(ev.OwnerKey)
		if err != nil {
			return fmt.Errorf("alias %s: owner node %q: %w", ev.Key, ev.OwnerKey, err)
		}
		_, err = s.AddAlias(AliasRow{
			NodeID: nodeID, Alias: str("alias"),
			AliasKind: str("alias_kind"), Confidence: f64("confidence"),
		})
		return err

	case EvAliasDel:
		nodeID, err := s.GetNodeIDByKey(ev.OwnerKey)
		if err != nil {
			return fmt.Errorf("alias del %s: owner node %q: %w", ev.Key, ev.OwnerKey, err)
		}
		_, err = s.db.Exec(`DELETE FROM node_aliases WHERE node_id = ? AND normalized_alias = ?`,
			nodeID, str("normalized_alias"))
		return err

	case EvGlossarySet:
		// Key format: "term:<domain_key>:<term>" (see UpsertTerm emission).
		rest := strings.TrimPrefix(ev.Key, "term:")
		domainKey, term, ok := strings.Cut(rest, ":")
		if !ok {
			return fmt.Errorf("glossary_set: malformed key %q", ev.Key)
		}
		t := DomainTerm{
			DomainKey:   domainKey,
			Term:        term,
			Description: str("description"),
			Context:     str("context"),
		}
		// aliases/anti_patterns/examples travel as JSON-array strings.
		for k, dst := range map[string]*[]string{
			"aliases": &t.Aliases, "anti_patterns": &t.AntiPatterns, "examples": &t.Examples,
		} {
			if v := str(k); v != "" {
				if err := json.Unmarshal([]byte(v), dst); err != nil {
					return fmt.Errorf("glossary_set %s: bad %s: %w", ev.Key, k, err)
				}
			}
		}
		_, err := s.UpsertTerm(t)
		return err

	case EvGlossaryDel:
		return s.DeleteTerm(str("domain_key"), str("term"))

	default:
		return fmt.Errorf("unknown event kind %q", ev.Kind)
	}
}
