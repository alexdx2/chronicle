package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/alexdx2/chronicle-core/snapshot"
	"github.com/alexdx2/chronicle-core/truth"
)

const nestController = "import { Controller, Get } from '@nestjs/common';\n@Controller('cats')\nexport class Cats {\n  @Get(':id')\n  one() {}\n}\n"

func runTruth(t *testing.T, args ...string) []byte {
	t.Helper()
	defer func() { projectPath = "" }()
	root := NewRootCmd()
	var out, errb bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errb)
	root.SetArgs(append([]string{"truth"}, args...))
	if err := root.Execute(); err != nil {
		t.Fatalf("truth %v: %v\n%s", args, err, errb.String())
	}
	return out.Bytes()
}

func writeCats(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "src", "cats.controller.ts")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(nestController), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func assertOneEndpoint(t *testing.T, out []byte) {
	t.Helper()
	var rep truth.Report
	if err := json.Unmarshal(out, &rep); err != nil {
		t.Fatalf("not a report: %v\n%s", err, out)
	}
	if len(rep.Facts) != 1 || rep.Facts[0].Class != truth.ClassEndpoints || rep.Facts[0].Key != "GET /cats/{}" {
		t.Fatalf("facts = %+v", rep.Facts)
	}
}

func TestTruthExtractJSON(t *testing.T) {
	dir := writeCats(t)
	assertOneEndpoint(t, runTruth(t, "extract", "--root", dir, "--json"))
}

func TestTruthExtractTable(t *testing.T) {
	dir := writeCats(t)
	out := string(runTruth(t, "extract", "--root", dir))
	if !bytes.Contains([]byte(out), []byte("GET /cats/{}")) || !bytes.Contains([]byte(out), []byte("files read: 1")) {
		t.Fatalf("table = %s", out)
	}
}

func TestTruthCompareJSON(t *testing.T) {
	dir := writeCats(t)
	s := &snapshot.Snapshot{
		Header:   snapshot.Header{Kind: "header", Format: snapshot.Format, Version: snapshot.Version, FactSchema: snapshot.FactSchema, Domains: []string{"d"}},
		Nodes:    []snapshot.Node{{Kind: "node", ID: "contract:endpoint:d:get:/cats/{id}", Type: "endpoint", Domain: "d", Name: "GET /cats/{id}", Status: "active"}},
		Coverage: []snapshot.Coverage{{Kind: "coverage", Path: "src/cats.controller.ts"}},
	}
	var buf bytes.Buffer
	if err := snapshot.Encode(&buf, s); err != nil {
		t.Fatal(err)
	}
	gp := filepath.Join(dir, ".depbot", "graph.jsonl")
	if err := os.MkdirAll(filepath.Dir(gp), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(gp, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	out := runTruth(t, "compare", "--root", dir, "--graph", gp, "--json")
	var v struct {
		Classes []struct {
			Class    string  `json:"class"`
			Coverage float64 `json:"coverage"`
		} `json:"classes"`
	}
	if err := json.Unmarshal(out, &v); err != nil {
		t.Fatal(err)
	}
	if len(v.Classes) == 0 || v.Classes[0].Class != "endpoints" || v.Classes[0].Coverage != 1 {
		t.Fatalf("classes = %+v\n%s", v.Classes, out)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(out, &raw); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"classes", "snapshot_id", "code_sha", "facts", "uncovered"} {
		if _, ok := raw[k]; !ok {
			t.Errorf("missing key %q in %s", k, out)
		}
	}
	if _, ok := raw["Classes"]; ok {
		t.Error("unexpected CamelCase key Classes")
	}
	// --report gives the same verdict as extracting inline.
	rp := filepath.Join(dir, "report.json")
	if err := os.WriteFile(rp, runTruth(t, "extract", "--root", dir, "--json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if viaReport := runTruth(t, "compare", "--graph", gp, "--report", rp, "--json"); !bytes.Equal(viaReport, out) {
		t.Errorf("--report verdict differs:\n%s\nvs\n%s", viaReport, out)
	}
	table := string(runTruth(t, "compare", "--root", dir, "--graph", gp))
	if !bytes.Contains([]byte(table), []byte("100%")) || !bytes.Contains([]byte(table), []byte("нет данных")) {
		t.Fatalf("table = %s", table)
	}
}

func TestTruthExtractSha(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := writeCats(t)
	for _, a := range [][]string{{"init", "-q"}, {"add", "."}, {"-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "-m", "x"}} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, a...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", a, err, out)
		}
	}
	assertOneEndpoint(t, runTruth(t, "extract", "--root", dir, "--sha", "HEAD", "--json"))
}

func TestTruthExtractBadSha(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := writeCats(t)
	if out, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	defer func() { projectPath = "" }()
	root := NewRootCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"truth", "extract", "--root", dir, "--sha", "deadbeef", "--json"})
	if err := root.Execute(); err == nil {
		t.Fatal("expected error for bad sha")
	}
}
