package snapshot

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// buildDeterminismFixture returns a Snapshot with the same logical content
// every time, but with record order, nested-slice order, and metadata JSON
// key order permuted when permuted is true. Used to prove that Encode's
// output depends only on content, never on input order.
func buildDeterminismFixture(permuted bool) *Snapshot {
	metaA := `{"b":2,"a":1}`
	metaEdge := `{"y":"2","x":"1"}`
	if permuted {
		metaA = `{"a":1,"b":2}`
		metaEdge = `{"x":"1","y":"2"}`
	}

	aliasesA := []Alias{
		{Alias: "a-alias", Kind: "name", Confidence: 0.8},
		{Alias: "a2-alias", Kind: "dns", Confidence: 0.5},
	}
	evidenceA := []Evidence{
		{ID: "ev-a-1", Source: "code", File: "a.go", Blob: "blobA", Range: "1:1-2:2", Extractor: "chronicle-scan", Confidence: 0.9},
		{ID: "ev-a-2", Source: "code", File: "a.go", Blob: "blobA", Range: "3:1-4:2", Extractor: "chronicle-scan", Confidence: 0.7},
	}
	if permuted {
		aliasesA = []Alias{aliasesA[1], aliasesA[0]}
		evidenceA = []Evidence{evidenceA[1], evidenceA[0]}
	}

	nodeA := Node{
		Kind: "node", ID: "code:module:orders:a", Layer: "code", Type: "module",
		Domain: "orders", Name: "A", QualifiedName: "a", File: "a.go", Lang: "go",
		Status:   "active",
		Attrs:    map[string]string{"owner_key": "team-orders", "visibility": "public"},
		Metadata: json.RawMessage(metaA),
		Aliases:  aliasesA,
		Evidence: evidenceA,
	}
	nodeB := Node{
		Kind: "node", ID: "code:module:orders:b", Layer: "code", Type: "module",
		Domain: "orders", Name: "B", QualifiedName: "b", File: "b.go", Lang: "go",
		Status: "active",
	}

	edgeAB := Edge{
		Kind: "edge", ID: "code:module:orders:a->code:module:orders:b:CALLS",
		Type: "CALLS", From: "code:module:orders:a", To: "code:module:orders:b",
		Derivation: "hard", DependencySource: "code", Context: "ctx",
		Active: true, Verification: "verified",
		Metadata: json.RawMessage(metaEdge),
		Evidence: []Evidence{
			{ID: "ev-e-1", Source: "code", File: "a.go", Extractor: "chronicle-scan", Confidence: 1.0},
		},
	}
	edgeBA := Edge{
		Kind: "edge", ID: "code:module:orders:b->code:module:orders:a:REFERS_TO",
		Type: "REFERS_TO", From: "code:module:orders:b", To: "code:module:orders:a",
		Derivation: "soft", DependencySource: "code", Active: true,
	}

	term1 := Term{
		Kind: "term", ID: "orders#order", Domain: "orders", Term: "order",
		Aliases: []string{"po"}, AntiPatterns: []string{"invoice"},
		Description: "An order.", Context: "ctx", Examples: []string{"ex1"},
	}
	term2 := Term{
		Kind: "term", ID: "orders#invoice", Domain: "orders", Term: "invoice",
		Description: "An invoice.",
	}

	cov1 := Coverage{
		Kind: "coverage", Path: "a.go", Blob: "blobA",
		Extractors: []ExtractorRun{{ID: "chronicle-scan", Version: "v1", Status: "extracted"}},
	}
	cov2 := Coverage{
		Kind: "coverage", Path: "b.go", Blob: "blobB",
		Extractors: []ExtractorRun{{ID: "chronicle-scan", Version: "v1", Status: "extracted"}},
	}

	nodes := []Node{nodeA, nodeB}
	edges := []Edge{edgeAB, edgeBA}
	terms := []Term{term1, term2}
	coverage := []Coverage{cov1, cov2}
	if permuted {
		nodes = []Node{nodeB, nodeA}
		edges = []Edge{edgeBA, edgeAB}
		terms = []Term{term2, term1}
		coverage = []Coverage{cov2, cov1}
	}

	return &Snapshot{
		Header: Header{
			Kind: "header", Format: Format, Version: Version,
			Seq: 3, Domains: []string{"orders"}, FactSchema: FactSchema,
			Registry: "sha256:deadbeef",
		},
		Nodes:    nodes,
		Edges:    edges,
		Terms:    terms,
		Coverage: coverage,
	}
}

func TestEncode_Deterministic(t *testing.T) {
	var buf1a, buf1b, buf2 bytes.Buffer
	if err := Encode(&buf1a, buildDeterminismFixture(false)); err != nil {
		t.Fatalf("Encode (a): %v", err)
	}
	if err := Encode(&buf1b, buildDeterminismFixture(false)); err != nil {
		t.Fatalf("Encode (b, same order): %v", err)
	}
	if err := Encode(&buf2, buildDeterminismFixture(true)); err != nil {
		t.Fatalf("Encode (permuted): %v", err)
	}

	if !bytes.Equal(buf1a.Bytes(), buf1b.Bytes()) {
		t.Fatalf("encoding the same snapshot twice produced different bytes:\n--- a ---\n%s\n--- b ---\n%s", buf1a.String(), buf1b.String())
	}
	if !bytes.Equal(buf1a.Bytes(), buf2.Bytes()) {
		t.Fatalf("encoding a permuted-but-equivalent snapshot produced different bytes:\n--- original ---\n%s\n--- permuted ---\n%s", buf1a.String(), buf2.String())
	}

	out := buf1a.String()
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if len(lines) == 0 {
		t.Fatal("Encode produced no lines")
	}
	var hdr Header
	if err := json.Unmarshal([]byte(lines[0]), &hdr); err != nil {
		t.Fatalf("first line is not valid JSON: %v\nline: %s", err, lines[0])
	}
	if hdr.Kind != "header" {
		t.Fatalf("first line kind = %q, want %q", hdr.Kind, "header")
	}
	if !strings.HasSuffix(out, "\n") {
		t.Fatal("encoded output does not end with a trailing newline")
	}
	// Edge keys always contain "->"; encoding/json's default HTML-escaping
	// would turn that into a backslash-u escape in every edge line, which is
	// unreadable and undiffable — this format is git-committed source, never
	// embedded in HTML/JS, so it must come out literal. Build the escape
	// sequence from its code points rather than typing it, so the literal
	// backslash survives however this test file is transported/edited.
	htmlEscapedGT := string(rune(0x5c)) + "u003e" // what json.Marshal would emit for '>'
	if !strings.Contains(out, "->") || strings.Contains(out, htmlEscapedGT) {
		t.Fatalf("expected literal \"->\" in edge ids with no HTML-escaping, got:\n%s", out)
	}
}

func TestRoundTrip(t *testing.T) {
	s := buildDeterminismFixture(false)

	var buf bytes.Buffer
	if err := Encode(&buf, s); err != nil {
		t.Fatalf("Encode: %v", err)
	}

	got, warnings, err := Decode(&buf, DefaultLimits())
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings on a clean round trip: %+v", warnings)
	}
	if !reflect.DeepEqual(got, s) {
		t.Fatalf("round trip mismatch:\ngot:  %#v\nwant: %#v", got, s)
	}
}

func TestDecode_UnknownMajorVersion(t *testing.T) {
	input := `{"kind":"header","format":"chronicle-graph","version":2,"seq":1,"domains":["x"],"fact_schema":1,"counts":{"node":0,"edge":0,"term":0,"coverage":0}}` + "\n"

	_, _, err := Decode(strings.NewReader(input), DefaultLimits())
	if err == nil {
		t.Fatal("expected an error for an unknown major version, got nil")
	}
	if !strings.Contains(err.Error(), "version 2") {
		t.Fatalf("expected error to mention %q, got: %v", "version 2", err)
	}
}

func TestDecode_Hostile(t *testing.T) {
	lines := []string{
		`{"kind":"header","format":"chronicle-graph","version":1,"seq":1,"domains":["orders"],"fact_schema":1,"counts":{"node":99,"edge":1,"term":0,"coverage":0}}`,
		`{"kind":"node","id":"code:module:orders:a","layer":"code","type":"module","domain":"orders","name":"A","qualified_name":"a","status":"active"}`,
		`{"kind":"node","id":"code:module:orders:b","layer":"code","type":"module","domain":"orders","name":"B","qualified_name":"b","status":"active"}`,
		`{`,
		`{"kind":"alien"}`,
		`<<<<<<< HEAD`,
		`=======`,
		`>>>>>>> feature-branch`,
		`{"kind":"node","id":"not a key","name":"Bad"}`,
		`{"kind":"edge","id":"code:module:orders:a->code:module:orders:b:CALLS","type":"CALLS","from":"code:module:orders:a","to":"code:module:orders:b","active":true}`,
		`{"kind":"edge","id":"code:module:orders:a->code:module:orders:ghost:CALLS","type":"CALLS","from":"code:module:orders:a","to":"code:module:orders:ghost","active":true}`,
		strings.Repeat("x", 1000),
	}
	input := strings.Join(lines, "\n") + "\n"

	lim := DefaultLimits()
	lim.MaxLine = 200 // small, so the 1000-byte last line reliably trips too_long

	var snap *Snapshot
	var warnings []Warning
	var err error
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("Decode panicked on hostile input: %v", r)
			}
		}()
		snap, warnings, err = Decode(strings.NewReader(input), lim)
	}()

	if err != nil {
		t.Fatalf("Decode returned an error, want nil (hostile input should warn, not fail): %v", err)
	}
	if snap == nil {
		t.Fatal("Decode returned a nil snapshot alongside a nil error")
	}

	if len(snap.Nodes) != 2 {
		t.Errorf("got %d nodes, want 2 (only the well-formed ones should load)", len(snap.Nodes))
	}
	if len(snap.Edges) != 1 {
		t.Errorf("got %d edges, want 1 (only the well-formed one should load)", len(snap.Edges))
	}

	wantCodes := []string{"bad_json", "too_long", "unknown_kind", "bad_key", "conflict_marker", "counts_mismatch", "dangling_edge"}
	allowed := map[string]bool{}
	for _, c := range wantCodes {
		allowed[c] = true
	}
	seen := map[string]bool{}
	for _, w := range warnings {
		seen[w.Code] = true
		if !allowed[w.Code] {
			t.Errorf("unexpected warning code %q: %+v", w.Code, w)
		}
	}
	for _, code := range wantCodes {
		if !seen[code] {
			t.Errorf("missing expected warning code %q; got warnings: %+v", code, warnings)
		}
	}
}

func TestEncode_OmitsEmpty(t *testing.T) {
	s := &Snapshot{
		Header: Header{
			Kind: "header", Format: Format, Version: Version,
			Seq: 1, Domains: []string{"orders"}, FactSchema: FactSchema,
		},
		Nodes: []Node{
			{
				Kind: "node", ID: "code:module:orders:bare", Layer: "code",
				Type: "module", Domain: "orders", Name: "Bare",
				QualifiedName: "bare", Status: "active",
				// Attrs, Metadata, Aliases, Evidence intentionally left unset.
			},
		},
		Edges: []Edge{
			{
				Kind: "edge", ID: "code:module:orders:bare->code:module:orders:bare:SELF_REF",
				Type: "SELF_REF", From: "code:module:orders:bare", To: "code:module:orders:bare",
				Active: false, // must still be present in the output
			},
		},
	}

	var buf bytes.Buffer
	if err := Encode(&buf, s); err != nil {
		t.Fatalf("Encode: %v", err)
	}

	lines := strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3 (header, node, edge):\n%s", len(lines), buf.String())
	}

	var nodeLine map[string]json.RawMessage
	if err := json.Unmarshal([]byte(lines[1]), &nodeLine); err != nil {
		t.Fatalf("node line is not valid JSON: %v", err)
	}
	for _, key := range []string{"attrs", "metadata", "aliases", "evidence"} {
		if _, present := nodeLine[key]; present {
			t.Errorf("node line unexpectedly has key %q: %s", key, lines[1])
		}
	}

	var edgeLine map[string]json.RawMessage
	if err := json.Unmarshal([]byte(lines[2]), &edgeLine); err != nil {
		t.Fatalf("edge line is not valid JSON: %v", err)
	}
	active, present := edgeLine["active"]
	if !present {
		t.Fatalf("edge line missing 'active' key entirely: %s", lines[2])
	}
	if string(active) != "false" {
		t.Errorf("edge line active = %s, want false", active)
	}
}
