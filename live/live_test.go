package live

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/store"
)

func openStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "live.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestParseScreen_CleansAndBounds(t *testing.T) {
	raw := `{"client_id":"a1-b2","tab":"graph","title":"C3:\n arena\u0007-api",
		"spec":{"scope":{"domain":"d"},"layout":{"preset":"c3"},"bogus":1,
		        "marks":{"k":{"kind":" Breaks ","note":"x\ny"}}},
		"selected":{"kind":"node","key":"code:provider:d:x"},
		"breadcrumbs":["C2","C3\tx"],"visible":true,"extra":"dropped"}`
	s, err := ParseScreen([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if s.Title != "C3: arena-api" || s.Breadcrumbs[1] != "C3 x" || !s.Visible {
		t.Fatalf("cleaned screen = %+v", s)
	}
	if s.Spec == nil || s.Spec.Layout.Preset != "c3" || s.Spec.Marks["k"].Kind != "breaks" || s.Spec.Marks["k"].Note != "x y" {
		t.Fatalf("spec = %+v", s.Spec)
	}
	long := `{"client_id":"a","title":"` + strings.Repeat("я", 300) + `"}`
	if s, err := ParseScreen([]byte(long)); err != nil || len([]rune(s.Title)) != 120 {
		t.Fatalf("title bounded to 120 characters: %d, %v", len([]rune(s.Title)), err)
	}
}

func TestParseScreen_Refuses(t *testing.T) {
	crumbs := `[` + strings.TrimSuffix(strings.Repeat(`"x",`, 21), ",") + `]`
	keys := `[` + strings.TrimSuffix(strings.Repeat(`"k",`, 51), ",") + `]`
	for name, raw := range map[string]string{
		"bad json":        `{`,
		"no client":       `{"tab":"graph"}`,
		"bad client":      `{"client_id":"<script>"}`,
		"bad kind":        `{"client_id":"a","selected":{"kind":"group","key":"k"}}`,
		"too many crumbs": `{"client_id":"a","breadcrumbs":` + crumbs + `}`,
		"too many keys":   `{"client_id":"a","selected":{"kind":"edge","keys":` + keys + `}}`,
		"long key":        `{"client_id":"a","selected":{"kind":"node","key":"` + strings.Repeat("k", 513) + `"}}`,
		"bad mark":        `{"client_id":"a","spec":{"marks":{"k":{"kind":"explode"}}}}`,
	} {
		if _, err := ParseScreen([]byte(raw)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	big := bytes.Repeat([]byte("x"), MaxScreenBytes+1)
	if _, err := ParseScreen(big); err == nil {
		t.Error("oversized report accepted")
	}
}

func TestPublishAndReport_WriteEvents(t *testing.T) {
	st := openStore(t)
	ev, err := PublishDiagram(st, SourceMCP, Diagram{Title: "Impact", Level: "impact", Domain: "d", URL: "u", Counts: Counts{Nodes: 3}, Marks: 1})
	if err != nil {
		t.Fatal(err)
	}
	var d Diagram
	if err := json.Unmarshal(ev.Payload, &d); err != nil || ev.Kind != KindDiagram || ev.Source != SourceMCP || d.Title != "Impact" || d.Counts.Nodes != 3 {
		t.Fatalf("diagram event = %+v (%+v, %v)", ev, d, err)
	}
	sc, err := ReportScreen(st, Screen{ClientID: "c1", Tab: "graph", Visible: true})
	if err != nil || sc.Kind != KindScreen || sc.Source != SourceDashboard {
		t.Fatalf("screen event = %+v, %v", sc, err)
	}
}
