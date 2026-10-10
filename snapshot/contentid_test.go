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
	a, _ := ContentID(canon, nil, nil)
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
	a, _ := ContentID(encodeT(t, twoNodeSnapshot(1)), []byte("x"), nil)
	changed := twoNodeSnapshot(1)
	changed.Nodes[0].Name = "Renamed"
	b, _ := ContentID(encodeT(t, changed), []byte("x"), nil)
	c, _ := ContentID(encodeT(t, twoNodeSnapshot(1)), []byte("y"), nil)
	d, _ := ContentID(encodeT(t, twoNodeSnapshot(1)), []byte("x"), []byte("types: {}"))
	if a == b || a == c || a == d {
		t.Fatalf("content change did not change the id: %s %s %s %s", a, b, c, d)
	}
}

func TestContentID_NilAndEmptyTypesEqual(t *testing.T) {
	g := encodeT(t, twoNodeSnapshot(1))
	a, _ := ContentID(g, []byte("m"), nil)
	b, _ := ContentID(g, []byte("m"), []byte{})
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

func TestContentID_BinaryGarbageIsTolerated(t *testing.T) {
	// Decode tolerates "\x00\x01" as warnings rather than failing, so ContentID
	// returns an id for it. Pinned here so a change in Decode is noticed.
	id, err := ContentID([]byte("\x00\x01"), nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(id) {
		t.Fatalf("not 64 lowercase hex: %q", id)
	}
}
