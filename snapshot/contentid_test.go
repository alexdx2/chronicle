package snapshot

import (
	"bytes"
	"regexp"
	"testing"
)

func encodeT(t *testing.T, s *Snapshot) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := Encode(&buf, s); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func mustContentID(t *testing.T, graph, domain, types []byte) string {
	t.Helper()
	id, err := ContentID(graph, domain, types)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func twoNodeSnapshot(seq int) *Snapshot {
	return &Snapshot{
		Header: Header{Kind: "header", Format: Format, Version: Version, Seq: seq, Domains: []string{"d"}, FactSchema: FactSchema},
		Nodes: []Node{
			{Kind: "node", ID: "code:class:d:B", Domain: "d", Name: "B", Layer: "code", Type: "class"},
			{Kind: "node", ID: "code:class:d:A", Domain: "d", Name: "A", Layer: "code", Type: "class"},
		},
		Edges: []Edge{{Kind: "edge", ID: "code:class:d:A->code:class:d:B:CALLS_SYMBOL", From: "code:class:d:A", To: "code:class:d:B", Type: "CALLS_SYMBOL"}},
	}
}

func TestContentID_IgnoresSeq(t *testing.T) {
	a, err := ContentID(encodeT(t, twoNodeSnapshot(3)), []byte("domains: {d: {}}\n"), nil)
	if err != nil {
		t.Fatal(err)
	}
	b, err := ContentID(encodeT(t, twoNodeSnapshot(7)), []byte("domains: {d: {}}\n"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatalf("Seq changed the id: %s vs %s", a, b)
	}
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(a) {
		t.Fatalf("not 64 lowercase hex: %q", a)
	}
}

func TestContentID_IgnoresRecordOrder(t *testing.T) {
	canon := encodeT(t, twoNodeSnapshot(1))
	a := mustContentID(t, canon, nil, nil)
	// Encode writes header, then node lines sorted by ID (A before B). Swap the
	// two node lines by hand so the input file is genuinely out of order.
	lines := bytes.SplitAfter(canon, []byte("\n"))
	lines[1], lines[2] = lines[2], lines[1]
	shuffled := bytes.Join(lines, nil)
	if bytes.Equal(canon, shuffled) {
		t.Fatal("test setup: shuffled bytes equal canonical bytes")
	}
	b, err := ContentID(shuffled, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatalf("record order changed the id")
	}
}

func TestContentID_ChangesWithContent(t *testing.T) {
	a := mustContentID(t, encodeT(t, twoNodeSnapshot(1)), []byte("x"), nil)
	changed := twoNodeSnapshot(1)
	changed.Nodes[0].Name = "Renamed"
	b := mustContentID(t, encodeT(t, changed), []byte("x"), nil)
	c := mustContentID(t, encodeT(t, twoNodeSnapshot(1)), []byte("y"), nil)
	d := mustContentID(t, encodeT(t, twoNodeSnapshot(1)), []byte("x"), []byte("types: {}"))
	if a == b || a == c || a == d {
		t.Fatalf("content change did not change the id: %s %s %s %s", a, b, c, d)
	}
}

func encodeMinimalGraphT(t *testing.T, version int) []byte {
	t.Helper()
	return encodeT(t, &Snapshot{Header: Header{Kind: "header", Format: Format, Version: version, Seq: 1, FactSchema: FactSchema}})
}

func TestContentID_V2IncludesProfileAlways(t *testing.T) {
	g := encodeMinimalGraphT(t, 2)
	a, _ := ContentIDFiles(Files{Graph: g, Domain: []byte("d"), Types: nil, Profile: nil})
	b, _ := ContentIDFiles(Files{Graph: g, Domain: []byte("d"), Types: nil, Profile: []byte(`{"x":1}`)})
	if a == b {
		t.Fatal("profile must change a v2 id")
	}
	c, _ := ContentIDFiles(Files{Graph: g, Domain: []byte("d"), Types: []byte("t\x1fp"), Profile: nil})
	d, _ := ContentIDFiles(Files{Graph: g, Domain: []byte("d"), Types: []byte("t"), Profile: []byte("p")})
	if c == d {
		t.Fatal("v2 ids collide across the types/profile boundary")
	}
	e0, _ := ContentIDFiles(Files{Graph: g, Domain: []byte("d")})
	e1, _ := ContentIDFiles(Files{Graph: g, Domain: []byte("d"), Profile: []byte{}})
	if e0 != e1 {
		t.Fatal("nil and empty profile must hash the same")
	}
	g1 := encodeMinimalGraphT(t, 1)
	x, _ := ContentIDFiles(Files{Graph: g1, Domain: []byte("d"), Profile: []byte("ignored")})
	y, _ := ContentID(g1, []byte("d"), nil)
	if x != y {
		t.Fatal("v1 id must equal the legacy ContentID and ignore profile")
	}
}

func TestDecode_AcceptsV1AndV2RejectsV3(t *testing.T) {
	for _, v := range []int{1, 2} {
		if _, _, err := Decode(bytes.NewReader(encodeMinimalGraphT(t, v)), DefaultLimits()); err != nil {
			t.Fatalf("v%d: %v", v, err)
		}
	}
	if _, _, err := Decode(bytes.NewReader(encodeMinimalGraphT(t, 3)), DefaultLimits()); err == nil {
		t.Fatal("v3 accepted")
	}
}

func TestContentID_NilAndEmptyTypesEqual(t *testing.T) {
	g := encodeT(t, twoNodeSnapshot(1))
	a := mustContentID(t, g, []byte("m"), nil)
	b := mustContentID(t, g, []byte("m"), []byte{})
	if a != b {
		t.Fatal("nil and empty types must hash the same")
	}
}

func TestContentID_ToleratedBadLineIgnored(t *testing.T) {
	g := encodeT(t, twoNodeSnapshot(1))
	withBad := append(append([]byte{}, g...), []byte("{not json\n")...)
	a, err := ContentID(g, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	b, err := ContentID(withBad, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatal("a tolerated bad_json line must not change the id")
	}
}

func TestContentID_HeaderlessInputErrors(t *testing.T) {
	nodeOnly := []byte(`{"kind":"node","id":"code:class:d:A","domain":"d","name":"A","layer":"code","type":"class"}` + "\n")
	cases := map[string][]byte{
		"nil":       nil,
		"garbage":   []byte("\x00\x01"),
		"node-only": nodeOnly,
	}
	for name, graph := range cases {
		id, err := ContentID(graph, nil, nil)
		if err == nil {
			t.Errorf("%s: expected an error, got id %q", name, id)
		}
		if id != "" {
			t.Errorf("%s: expected empty id on error, got %q", name, id)
		}
	}
}

// A version 1 snapshot keeps the identity it had before profile.json existed.
// The id below was computed outside this code:
// sha256(canonical graph (seq 0) | 0x1f | domain | 0x1f | types).
func TestContentID_LegacyV1GoldenID(t *testing.T) {
	const want = "d6ae50f266027dc0db6978445e4eedf81903a5fb3bc44e91767f3535b2958864"
	graph := []byte(`{"kind":"header","format":"chronicle-graph","version":1,"seq":7,"domains":["d"],"fact_schema":1,"counts":{"node":1}}` + "\n" +
		`{"kind":"node","id":"code:class:d:A","layer":"code","type":"class","domain":"d","name":"A"}` + "\n")
	domain, types := []byte("domains: {d: {}}\n"), []byte("types: {}\n")
	got, err := ContentIDFiles(Files{Graph: graph, Domain: domain, Types: types, Profile: []byte(`{"ignored":true}`)})
	if err != nil || got != want {
		t.Fatalf("ContentIDFiles = %s, %v; want %s", got, err, want)
	}
	if got, err := ContentID(graph, domain, types); err != nil || got != want {
		t.Fatalf("ContentID = %s, %v; want %s", got, err, want)
	}
}
