package extract

import (
	"errors"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/alexdx2/chronicle-core/truth"
)

type stubRule struct {
	name  string
	ext   string
	apply func(f *FileCtx) []truth.Fact
}

func (s stubRule) Name() string                  { return s.name }
func (s stubRule) Match(p string) bool           { return strings.HasSuffix(p, s.ext) }
func (s stubRule) Apply(f *FileCtx) []truth.Fact { return s.apply(f) }

func withRules(t *testing.T, rs ...Rule) {
	old := Rules
	Rules = append(append([]Rule(nil), old...), rs...)
	t.Cleanup(func() { Rules = old })
}

func TestExtractWalk(t *testing.T) {
	withRules(t, stubRule{"stub.a", ".cs", func(f *FileCtx) []truth.Fact {
		return []truth.Fact{{Class: truth.ClassServices, Key: f.Path, Sources: []string{"stub.a"}}}
	}})
	fsys := fstest.MapFS{
		"src/a.cs":                {Data: []byte("class A {}")},
		"bin/b.cs":                {Data: []byte("x")},
		"node_modules/x/index.cs": {Data: []byte("x")},
		"big.cs":                  {Data: make([]byte, MaxFile+1)},
		"vendor/v.cs":             {Data: []byte("x")},
		"src/skip.min.js":         {Data: []byte("x")},
	}
	rep, err := Extract(fsys)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Facts) != 1 || rep.Facts[0].Key != "src/a.cs" {
		t.Fatalf("facts %v", rep.Facts)
	}
	if len(rep.SkippedFiles) != 1 || rep.SkippedFiles[0] != "big.cs" {
		t.Fatalf("skipped %v", rep.SkippedFiles)
	}
	if rep.Files != 1 {
		t.Fatalf("files %d", rep.Files)
	}
}

func TestExtractPanicIsolated(t *testing.T) {
	withRules(t, stubRule{"stub.p", ".cs", func(f *FileCtx) []truth.Fact {
		if f.Path == "bad.cs" {
			panic("boom")
		}
		return []truth.Fact{{Class: truth.ClassServices, Key: f.Path}}
	}})
	rep, err := Extract(fstest.MapFS{"bad.cs": {Data: []byte("x")}, "ok.cs": {Data: []byte("x")}})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Facts) != 1 || rep.Facts[0].Key != "ok.cs" {
		t.Fatalf("facts %v", rep.Facts)
	}
	if len(rep.SkippedFiles) != 1 || rep.SkippedFiles[0] != "bad.cs" {
		t.Fatalf("skipped %v", rep.SkippedFiles)
	}
	if len(rep.Warnings) != 1 || rep.Warnings[0] != "panic in stub.p on bad.cs: boom" {
		t.Fatalf("warnings %v", rep.Warnings)
	}
}

func TestTreeAndCtx(t *testing.T) {
	var got *FileCtx
	withRules(t, stubRule{"stub.t", ".cs", func(f *FileCtx) []truth.Fact { got = f; return nil }})
	_, err := Extract(fstest.MapFS{
		"svc/FooTests.cs":  {Data: []byte("a\nb")},
		"svc/package.json": {Data: []byte("{}")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Dir != "svc" || !got.IsTest || len(got.Lines) != 2 {
		t.Fatalf("ctx %+v", got)
	}
	if b, ok := got.Tree.ReadFile("svc/package.json"); !ok || string(b) != "{}" {
		t.Fatal("ReadFile")
	}
	if _, ok := got.Tree.ReadFile("nope"); ok {
		t.Fatal("ReadFile missing")
	}
	if s := got.Tree.Siblings("svc", "*.json"); len(s) != 1 || s[0] != "package.json" {
		t.Fatalf("siblings %v", s)
	}
}

func TestIsTestPath(t *testing.T) {
	cases := map[string]bool{
		"src/OrderTests.cs": true, "src/OrderTest.java": true, "src/OrderTests.java": true, "src/Order.java": false, "src/Latest.java": false, "src/Order.cs": false,
		"a/b.spec.ts": true, "a/b.test.ts": true, "a/b.test.js": true, "a/b.ts": false,
		"pkg/test_x.py": true, "pkg/x_test.py": false,
		"tests/a.py": true, "src/__tests__/a.js": true, "src/contest/a.js": false,
		"Shop.Api.IntegrationTests/Fakes/FakeController.cs": true, "src/Shop.Tests/A.cs": true,
		"src/LoginTest/A.cs": true, "src/test/A.cs": true, "src/Spec/A.cs": true, "src/shop.test/A.cs": true,
		"src/Latest/A.cs": false, "src/Contest/A.cs": false, "src/Shop.Api/Controllers/A.cs": false,
	}
	for p, want := range cases {
		if IsTestPath(p) != want {
			t.Errorf("%s: want %v", p, want)
		}
	}
}

type failFS struct {
	fstest.MapFS
	bad map[string]bool
}

func (f failFS) Open(name string) (fs.File, error) {
	if f.bad[name] {
		return nil, errors.New("denied")
	}
	return f.MapFS.Open(name)
}

func TestExtractIOErrorsIsolated(t *testing.T) {
	withRules(t, stubRule{"stub.io", ".cs", func(f *FileCtx) []truth.Fact {
		return []truth.Fact{{Class: truth.ClassServices, Key: f.Path}}
	}})
	fsys := failFS{fstest.MapFS{
		"ok.cs": {Data: []byte("x")}, "locked.cs": {Data: []byte("x")},
		"d/in.cs": {Data: []byte("x")}, "e/z.cs": {Data: []byte("x")},
	}, map[string]bool{"locked.cs": true, "d": true}}
	rep, err := Extract(fsys)
	if err != nil || rep == nil {
		t.Fatalf("err %v", err)
	}
	var keys []string
	for _, f := range rep.Facts {
		keys = append(keys, f.Key)
	}
	if strings.Join(keys, ",") != "e/z.cs,ok.cs" {
		t.Fatalf("facts %v", keys)
	}
	if len(rep.SkippedFiles) != 1 || rep.SkippedFiles[0] != "locked.cs" {
		t.Fatalf("skipped %v", rep.SkippedFiles)
	}
	w := strings.Join(rep.Warnings, "|")
	if !strings.Contains(w, "walk locked.cs: denied") || !strings.Contains(w, "walk d: denied") {
		t.Fatalf("warnings %v", rep.Warnings)
	}
}

func (f failFS) ReadFile(name string) ([]byte, error) {
	if f.bad[name] {
		return nil, errors.New("denied")
	}
	return f.MapFS.ReadFile(name)
}

func (f failFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if f.bad[name] {
		return nil, errors.New("denied")
	}
	return f.MapFS.ReadDir(name)
}

func TestExtractSetsRulesVersion(t *testing.T) {
	rep, err := Extract(fstest.MapFS{})
	if err != nil || rep.RulesVersion != truth.RulesVersion {
		t.Fatalf("rules version %d err %v", rep.RulesVersion, err)
	}
}
