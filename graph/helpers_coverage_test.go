package graph

import (
	"encoding/json"
	"testing"

	"github.com/alexdx2/chronicle-core/registry"
	"github.com/alexdx2/chronicle-core/store"
)

func TestStrAndFloatOrDefault(t *testing.T) {
	m := map[string]any{"s": "hi", "f": 3.5, "i": 7, "bad": true}
	if strOrDefault(m, "s", "x") != "hi" {
		t.Error("strOrDefault present")
	}
	if strOrDefault(m, "missing", "def") != "def" {
		t.Error("strOrDefault missing")
	}
	if strOrDefault(m, "f", "def") != "def" {
		t.Error("strOrDefault wrong-type should default")
	}
	if floatOrDefault(m, "f", 0) != 3.5 {
		t.Error("floatOrDefault float64")
	}
	if floatOrDefault(m, "i", 0) != 7 {
		t.Error("floatOrDefault int")
	}
	if floatOrDefault(m, "missing", 1.0) != 1.0 {
		t.Error("floatOrDefault missing")
	}
	if floatOrDefault(m, "bad", 2.0) != 2.0 {
		t.Error("floatOrDefault wrong-type should default")
	}
}

func TestKeyHelpers(t *testing.T) {
	if got := stripNodeKeyPrefix("code:service:tom-and-jerry:ArenaService"); got != "arenaservice" {
		t.Errorf("stripNodeKeyPrefix: %q", got)
	}
	if got := stripNodeKeyPrefix("plain"); got != "plain" {
		t.Errorf("stripNodeKeyPrefix plain: %q", got)
	}
	if got := inferNodeKeyFromFile("d", "src/arena/arena.service.ts"); got == "" || got[:11] != "code:module" {
		t.Errorf("inferNodeKeyFromFile: %q", got)
	}
	if got := inferNodeKeyFromImport("d", "@app/arena"); got == "" || got[:11] != "code:module" {
		t.Errorf("inferNodeKeyFromImport: %q", got)
	}
	if got := typedNodeKeyFromImport("d", "@app/arena", "provider"); got[:14] != "code:provider:" {
		t.Errorf("typedNodeKeyFromImport: %q", got)
	}
	// sortedMapKeys sorts.
	keys := sortedMapKeys(map[string][]map[string]any{"b": nil, "a": nil, "c": nil})
	if len(keys) != 3 || keys[0] != "a" || keys[2] != "c" {
		t.Errorf("sortedMapKeys: %v", keys)
	}
	// mapKeys returns all keys.
	if mk := mapKeys(map[string]bool{"x": true, "y": false}); len(mk) != 2 {
		t.Errorf("mapKeys: %v", mk)
	}
}

func TestCanonicalFactKey_NormalizesPrefix(t *testing.T) {
	a := canonicalFactKey(map[string]any{"kind": "code:injects", "to": "code:provider:d:Svc", "from": "X"})
	b := canonicalFactKey(map[string]any{"kind": "injects", "to": "CODE:PROVIDER:D:SVC", "from": "X"})
	if a == "" {
		t.Fatal("canonicalFactKey empty")
	}
	if a != b {
		t.Errorf("prefix/case normalization mismatch: %q vs %q", a, b)
	}
}

func TestIsArchitecturalDep_Coverage(t *testing.T) {
	if !IsArchitecturalDep("@nestjs/common") {
		t.Error("@nestjs/* should be architectural")
	}
	if IsArchitecturalDep("lodash") {
		t.Error("lodash should not be architectural (infrastructure)")
	}
	if ClassifyDependency("lodash") != DepInfrastructure {
		t.Error("lodash → infrastructure")
	}
	if ClassifyDependency("@nestjs/core") != DepArchitectural {
		t.Error("@nestjs/core → architectural")
	}
	if ClassifyDependency("./my-own-module") != DepInternal {
		t.Error("relative import → internal")
	}
}

func TestGraph_RegistryGetter(t *testing.T) {
	st, err := store.Open(t.TempDir() + "/g.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	reg, _ := registry.LoadDefaults()
	g := New(st, reg)
	if g.Registry() != reg {
		t.Error("Registry() should return the injected registry")
	}
}

func TestFlexStringAndInt_Unmarshal(t *testing.T) {
	// FlexString: accepts string or object.
	var node struct {
		Metadata FlexString `json:"metadata"`
	}
	if err := json.Unmarshal([]byte(`{"metadata":"{\"a\":1}"}`), &node); err != nil || node.Metadata != `{"a":1}` {
		t.Errorf("FlexString string form: %q err=%v", node.Metadata, err)
	}
	if err := json.Unmarshal([]byte(`{"metadata":{"a":1}}`), &node); err != nil || node.Metadata == "" {
		t.Errorf("FlexString object form: %q err=%v", node.Metadata, err)
	}
	// FlexInt: accepts number or string.
	var ev struct {
		Line FlexInt `json:"line_start"`
	}
	if err := json.Unmarshal([]byte(`{"line_start":42}`), &ev); err != nil || ev.Line != 42 {
		t.Errorf("FlexInt number: %d err=%v", ev.Line, err)
	}
	if err := json.Unmarshal([]byte(`{"line_start":"99"}`), &ev); err != nil || ev.Line != 99 {
		t.Errorf("FlexInt string: %d err=%v", ev.Line, err)
	}
	if err := json.Unmarshal([]byte(`{"line_start":""}`), &ev); err != nil || ev.Line != 0 {
		t.Errorf("FlexInt empty string → 0: %d err=%v", ev.Line, err)
	}
}

func TestFileGroups_OnRepo(t *testing.T) {
	// The test runs inside the graph package dir, which is in the git repo.
	groups, total, err := GroupFilesByDirectory(".")
	if err != nil {
		t.Skipf("git ls-files unavailable: %v", err)
	}
	if total == 0 || len(groups) == 0 {
		t.Errorf("expected files/groups, got total=%d groups=%d", total, len(groups))
	}
	tree, total2, err := BuildFileTree(".")
	if err != nil {
		t.Skipf("git ls-files unavailable: %v", err)
	}
	if total2 == 0 || len(tree) == 0 {
		t.Errorf("expected file tree, got total=%d entries=%d", total2, len(tree))
	}
}
