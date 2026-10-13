package snapshot

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/alexdx2/chronicle-core/validate"
)

// Limits bound how much of an input Decode will process, so that a hostile
// or corrupt snapshot cannot exhaust memory or CPU.
type Limits struct {
	MaxBytes   int64 // total bytes read from the input
	MaxLine    int   // longest single line (record) accepted
	MaxRecords int   // longest number of lines processed
}

// DefaultLimits returns the limits Decode uses unless the caller overrides
// them: 512 MiB total, 8 MiB per line, 2,000,000 records — a wide margin
// over any snapshot chronicle-core produces today.
func DefaultLimits() Limits {
	return Limits{
		MaxBytes:   512 << 20,
		MaxLine:    8 << 20,
		MaxRecords: 2_000_000,
	}
}

// Encode writes s to w as a canonical graph.jsonl file. It normalizes record
// and nested-slice order (Normalize), canonicalizes embedded JSON (Metadata,
// Assertion: parsed and re-marshaled so nested object keys are sorted and
// whitespace removed), and recomputes Header.Counts from the actual record
// counts. Each record is written as one compact JSON object followed by
// "\n"; the header line is written first. Encode mutates s in place (via
// Normalize and raw-JSON canonicalization) as a side effect of computing
// this canonical form.
func Encode(w io.Writer, s *Snapshot) error {
	Normalize(s)

	for i := range s.Nodes {
		if err := canonicalizeNodeRaw(&s.Nodes[i]); err != nil {
			return fmt.Errorf("snapshot: encode node %q: %w", s.Nodes[i].ID, err)
		}
	}
	for i := range s.Edges {
		if err := canonicalizeEdgeRaw(&s.Edges[i]); err != nil {
			return fmt.Errorf("snapshot: encode edge %q: %w", s.Edges[i].ID, err)
		}
	}

	s.Header.Counts = Counts{
		Node:     len(s.Nodes),
		Edge:     len(s.Edges),
		Term:     len(s.Terms),
		Coverage: len(s.Coverage),
	}

	bw := bufio.NewWriter(w)
	if err := writeLine(bw, s.Header); err != nil {
		return err
	}
	for _, n := range s.Nodes {
		if err := writeLine(bw, n); err != nil {
			return err
		}
	}
	for _, e := range s.Edges {
		if err := writeLine(bw, e); err != nil {
			return err
		}
	}
	for _, t := range s.Terms {
		if err := writeLine(bw, t); err != nil {
			return err
		}
	}
	for _, c := range s.Coverage {
		if err := writeLine(bw, c); err != nil {
			return err
		}
	}
	return bw.Flush()
}

func writeLine(w *bufio.Writer, v any) error {
	b, err := marshalCompact(v)
	if err != nil {
		return err
	}
	if _, err := w.Write(b); err != nil {
		return err
	}
	return w.WriteByte('\n')
}

// marshalCompact marshals v the same way json.Marshal does (compact, no
// indentation) but without HTML-escaping '<', '>' and '&'. graph.jsonl is a
// git-diffed, human-read source artifact, never embedded in HTML/JS, and
// edge ids/from/to always contain the two-byte arrow "->" — the default
// escaping would turn the '>' in every single edge line into a six-byte
// backslash-u escape, for no benefit here.
func marshalCompact(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// Normalize sorts records and their nested slices into canonical order:
// nodes by id, edges by id, terms by id, coverage by path (all byte-wise),
// and within each node/edge, aliases by (kind, alias) and evidence by id.
func Normalize(s *Snapshot) {
	sort.Slice(s.Nodes, func(i, j int) bool { return s.Nodes[i].ID < s.Nodes[j].ID })
	sort.Slice(s.Edges, func(i, j int) bool { return s.Edges[i].ID < s.Edges[j].ID })
	sort.Slice(s.Terms, func(i, j int) bool { return s.Terms[i].ID < s.Terms[j].ID })
	sort.Slice(s.Coverage, func(i, j int) bool { return s.Coverage[i].Path < s.Coverage[j].Path })

	for i := range s.Nodes {
		aliases := s.Nodes[i].Aliases
		sort.Slice(aliases, func(a, b int) bool {
			if aliases[a].Kind != aliases[b].Kind {
				return aliases[a].Kind < aliases[b].Kind
			}
			return aliases[a].Alias < aliases[b].Alias
		})
		sortEvidence(s.Nodes[i].Evidence)
	}
	for i := range s.Edges {
		sortEvidence(s.Edges[i].Evidence)
	}
}

func sortEvidence(ev []Evidence) {
	sort.Slice(ev, func(i, j int) bool { return ev[i].ID < ev[j].ID })
}

func canonicalizeNodeRaw(n *Node) error {
	canon, err := canonicalizeRaw(n.Metadata)
	if err != nil {
		return fmt.Errorf("metadata: %w", err)
	}
	n.Metadata = canon
	for i := range n.Evidence {
		if err := canonicalizeEvidenceRaw(&n.Evidence[i]); err != nil {
			return err
		}
	}
	return nil
}

func canonicalizeEdgeRaw(e *Edge) error {
	canon, err := canonicalizeRaw(e.Metadata)
	if err != nil {
		return fmt.Errorf("metadata: %w", err)
	}
	e.Metadata = canon
	for i := range e.Evidence {
		if err := canonicalizeEvidenceRaw(&e.Evidence[i]); err != nil {
			return err
		}
	}
	return nil
}

func canonicalizeEvidenceRaw(ev *Evidence) error {
	canon, err := canonicalizeRaw(ev.Assertion)
	if err != nil {
		return fmt.Errorf("evidence %q assertion: %w", ev.ID, err)
	}
	ev.Assertion = canon
	return nil
}

// canonicalizeRaw returns raw's canonical form: parsed and re-marshaled so
// nested object keys are sorted, whitespace is removed, and numbers use Go
// encoding/json's shortest representation. A nil/blank input, or one whose
// canonical form is the JSON literal "{}" or "null", is returned as nil so
// the enclosing field's `omitempty` tag drops it from the encoded line.
func canonicalizeRaw(raw json.RawMessage) (json.RawMessage, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, nil
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, err
	}
	canon, err := marshalCompact(v)
	if err != nil {
		return nil, err
	}
	if s := string(canon); s == "{}" || s == "null" {
		return nil, nil
	}
	return json.RawMessage(canon), nil
}

// Decode reads a graph.jsonl file line by line. An unrecognized major
// Version, or a Format other than this package's, is a fatal error — the
// rest of the file cannot be trusted to mean what it says. Everything else
// hostile is reported as a Warning and the offending record is skipped
// rather than aborting the read: malformed JSON, an unknown record kind, a
// git conflict marker (<<<<<<<, =======, >>>>>>>), a node or edge key that
// fails validate.NormalizeNodeKey/NormalizeEdgeKey, an edge referencing a
// node absent from this snapshot, and a Header.Counts that does not match
// what was actually read. Decode never panics on untrusted input.
func Decode(r io.Reader, lim Limits) (*Snapshot, []Warning, error) {
	snap := &Snapshot{}
	var warnings []Warning
	nodeIDs := make(map[string]bool)
	headerSeen := false

	sc := bufio.NewScanner(io.LimitReader(r, lim.MaxBytes))
	sc.Buffer(make([]byte, 0, initialScanBuf(lim.MaxLine)), lim.MaxLine)

	lineNo := 0
	for sc.Scan() {
		lineNo++
		if lineNo > lim.MaxRecords {
			break
		}
		line := sc.Bytes()

		if isConflictMarker(line) {
			warnings = append(warnings, Warning{Line: lineNo, Code: "conflict_marker", Msg: "git conflict marker"})
			continue
		}

		var probe struct {
			Kind string `json:"kind"`
		}
		if err := json.Unmarshal(line, &probe); err != nil {
			warnings = append(warnings, Warning{Line: lineNo, Code: "bad_json", Msg: err.Error()})
			continue
		}

		switch probe.Kind {
		case "header":
			var h Header
			if err := json.Unmarshal(line, &h); err != nil {
				warnings = append(warnings, Warning{Line: lineNo, Code: "bad_json", Msg: err.Error()})
				continue
			}
			if !headerSeen {
				headerSeen = true
				if h.Format != Format {
					return nil, nil, fmt.Errorf("snapshot: unrecognized format %q (want %q)", h.Format, Format)
				}
				if h.Version != 1 && h.Version != 2 {
					return nil, nil, fmt.Errorf("snapshot: unsupported major version %d (want %d)", h.Version, Version)
				}
			}
			snap.Header = h

		case "node":
			var n Node
			if err := json.Unmarshal(line, &n); err != nil {
				warnings = append(warnings, Warning{Line: lineNo, Code: "bad_json", Msg: err.Error()})
				continue
			}
			if _, err := validate.NormalizeNodeKey(n.ID); err != nil {
				warnings = append(warnings, Warning{Line: lineNo, Code: "bad_key", Msg: err.Error()})
				continue
			}
			nodeIDs[n.ID] = true
			snap.Nodes = append(snap.Nodes, n)

		case "edge":
			var e Edge
			if err := json.Unmarshal(line, &e); err != nil {
				warnings = append(warnings, Warning{Line: lineNo, Code: "bad_json", Msg: err.Error()})
				continue
			}
			if _, err := validate.NormalizeEdgeKey(e.ID); err != nil {
				warnings = append(warnings, Warning{Line: lineNo, Code: "bad_key", Msg: err.Error()})
				continue
			}
			if !nodeIDs[e.From] || !nodeIDs[e.To] {
				warnings = append(warnings, Warning{Line: lineNo, Code: "dangling_edge", Msg: fmt.Sprintf("edge %q references a node not present in this snapshot", e.ID)})
				continue
			}
			snap.Edges = append(snap.Edges, e)

		case "term":
			var t Term
			if err := json.Unmarshal(line, &t); err != nil {
				warnings = append(warnings, Warning{Line: lineNo, Code: "bad_json", Msg: err.Error()})
				continue
			}
			snap.Terms = append(snap.Terms, t)

		case "coverage":
			var c Coverage
			if err := json.Unmarshal(line, &c); err != nil {
				warnings = append(warnings, Warning{Line: lineNo, Code: "bad_json", Msg: err.Error()})
				continue
			}
			snap.Coverage = append(snap.Coverage, c)

		default:
			warnings = append(warnings, Warning{Line: lineNo, Code: "unknown_kind", Msg: fmt.Sprintf("unknown kind %q", probe.Kind)})
		}
	}

	if err := sc.Err(); err != nil {
		if errors.Is(err, bufio.ErrTooLong) {
			warnings = append(warnings, Warning{Line: lineNo + 1, Code: "too_long", Msg: "line exceeds MaxLine"})
		} else {
			return snap, warnings, fmt.Errorf("snapshot: reading input: %w", err)
		}
	}

	if msg := countsMismatch(snap); msg != "" {
		warnings = append(warnings, Warning{Line: 1, Code: "counts_mismatch", Msg: msg})
	}

	return snap, warnings, nil
}

// initialScanBuf picks a starting buffer capacity that never exceeds
// maxLine: bufio.Scanner.Buffer uses the larger of the two sizes it is
// given, so an initial buffer bigger than maxLine would silently defeat it.
func initialScanBuf(maxLine int) int {
	const want = 64 * 1024
	if maxLine > 0 && maxLine < want {
		return maxLine
	}
	return want
}

func countsMismatch(snap *Snapshot) string {
	var mismatches []string
	if snap.Header.Counts.Node != len(snap.Nodes) {
		mismatches = append(mismatches, fmt.Sprintf("node: header=%d actual=%d", snap.Header.Counts.Node, len(snap.Nodes)))
	}
	if snap.Header.Counts.Edge != len(snap.Edges) {
		mismatches = append(mismatches, fmt.Sprintf("edge: header=%d actual=%d", snap.Header.Counts.Edge, len(snap.Edges)))
	}
	if snap.Header.Counts.Term != len(snap.Terms) {
		mismatches = append(mismatches, fmt.Sprintf("term: header=%d actual=%d", snap.Header.Counts.Term, len(snap.Terms)))
	}
	if snap.Header.Counts.Coverage != len(snap.Coverage) {
		mismatches = append(mismatches, fmt.Sprintf("coverage: header=%d actual=%d", snap.Header.Counts.Coverage, len(snap.Coverage)))
	}
	return strings.Join(mismatches, "; ")
}

func isConflictMarker(line []byte) bool {
	return bytes.HasPrefix(line, []byte("<<<<<<<")) ||
		bytes.HasPrefix(line, []byte("=======")) ||
		bytes.HasPrefix(line, []byte(">>>>>>>"))
}
