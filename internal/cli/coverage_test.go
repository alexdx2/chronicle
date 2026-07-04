package cli

import "testing"

func TestBuildEdgeKey(t *testing.T) {
	if got := buildEdgeKey("a", "b", "INJECTS", "custom-key"); got != "custom-key" {
		t.Errorf("custom key should win: %q", got)
	}
	got := buildEdgeKey("code:provider:d:a", "code:provider:d:b", "INJECTS", "")
	if got == "" || got == "custom-key" {
		t.Errorf("derived edge key unexpected: %q", got)
	}
}

func TestShortSHA(t *testing.T) {
	if shortSHA("0123456789abcdef") != "0123456" {
		t.Errorf("long sha should truncate to 7")
	}
	if shortSHA("abc") != "abc" {
		t.Errorf("short sha unchanged")
	}
	if shortSHA("1234567") != "1234567" {
		t.Errorf("exactly 7 unchanged")
	}
}

func TestFilterRefreshable(t *testing.T) {
	in := []string{"a.ts", "b.go", "c.md", "d.cs", "e.png"}
	out := filterRefreshable(in)
	// At least the source files survive; non-source (png/md) are filtered.
	for _, f := range out {
		if f == "e.png" {
			t.Errorf("png should be filtered out: %v", out)
		}
	}
	if len(out) == 0 {
		t.Errorf("expected some refreshable files from %v", in)
	}
}

func TestPortFromPath(t *testing.T) {
	p := portFromPath("/some/project")
	if p < 4200 || p >= 5000 {
		t.Errorf("port out of range: %d", p)
	}
	if portFromPath("/x") != portFromPath("/x") {
		t.Error("portFromPath must be deterministic")
	}
}

func TestHookFireMarker(t *testing.T) {
	if hookFireMarker() != "hook fire" {
		t.Errorf("hookFireMarker: %q", hookFireMarker())
	}
}
