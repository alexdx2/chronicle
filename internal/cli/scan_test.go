package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/paths"
	"github.com/alexdx2/chronicle-core/scan"
	"github.com/alexdx2/chronicle-core/snapshot"
)

// scanRepoEnv is newRepo (src/{a,b,c}.ts, no manifest) with the project root
// pointed at it and a graph.
func scanRepoEnv(t *testing.T) *extractEnv {
	t.Helper()
	root := newRepo(t)
	paths.SetProjectRoot(root)
	t.Cleanup(func() { paths.SetProjectRoot("") })
	return &extractEnv{root: root, dir: filepath.Join(root, ".depbot"), g: newGraph(t)}
}

func TestScanCmd_RequiresFull(t *testing.T) {
	e := scanRepoEnv(t)
	code, err := runScanFull(context.Background(), &bytes.Buffer{}, e.g, e.root, scanCLIOptions{}, noEnv)
	if code != 1 || err == nil || !strings.Contains(err.Error(), "--full") {
		t.Fatalf("code=%d err=%v", code, err)
	}
}

func TestScanCmd_FakeRun(t *testing.T) {
	e := scanRepoEnv(t)
	var buf bytes.Buffer
	code, err := runScanFull(context.Background(), &buf, e.g, e.root, scanCLIOptions{
		Full: true, Model: "fake:" + filepath.Join("testdata", "extract-fake"), Concurrency: 1,
	}, noEnv)
	if code != 0 || err != nil {
		t.Fatalf("code=%d err=%v\n%s", code, err, buf.String())
	}
	var rep scan.FullReport
	if err := json.Unmarshal(buf.Bytes(), &rep); err != nil {
		t.Fatalf("stdout is not a FullReport: %v\n%s", err, buf.String())
	}
	if !rep.Complete || !rep.ManifestCreated || len(rep.Domains) != 1 || rep.Domains[0].Extracted != 3 {
		t.Fatalf("report = %+v", rep)
	}
	if !strings.Contains(buf.String(), `"complete": true`) {
		t.Fatalf("no complete flag in output:\n%s", buf.String())
	}
	data, err := os.ReadFile(filepath.Join(e.dir, "graph.jsonl"))
	if err != nil {
		t.Fatalf("graph.jsonl not written: %v", err)
	}
	snap, _, err := snapshot.Decode(bytes.NewReader(data), snapshot.DefaultLimits())
	if err != nil || len(snap.Nodes) == 0 {
		t.Fatalf("graph.jsonl does not decode into a graph: nodes=%v err=%v", snap, err)
	}
}

func TestScanCmd_NoExportAndStructuralOnly(t *testing.T) {
	e := scanRepoEnv(t)
	var buf bytes.Buffer
	// StructuralOnly must not need a key: no model is opened.
	code, err := runScanFull(context.Background(), &buf, e.g, e.root, scanCLIOptions{Full: true, StructuralOnly: true, NoExport: true}, noEnv)
	if code != 0 || err != nil {
		t.Fatalf("code=%d err=%v\n%s", code, err, buf.String())
	}
	if _, err := os.Stat(filepath.Join(e.dir, "graph.jsonl")); err == nil {
		t.Fatal("--no-export still wrote graph.jsonl")
	}
}

func TestScanCmd_NoKeyExit3(t *testing.T) {
	e := scanRepoEnv(t)
	var buf bytes.Buffer
	code, err := runScanFull(context.Background(), &buf, e.g, e.root, scanCLIOptions{Full: true, Model: "anthropic:claude-haiku-5-5"}, noEnv)
	if code != 3 || err != nil {
		t.Fatalf("code=%d err=%v", code, err)
	}
	if !strings.Contains(buf.String(), `"skipped": true`) {
		t.Fatalf("output = %s", buf.String())
	}
}

func TestScanCmd_IncompleteExit4(t *testing.T) {
	e := scanRepoEnv(t)
	empty := t.TempDir() // no fixtures, no default.json: every file fails
	var buf bytes.Buffer
	code, err := runScanFull(context.Background(), &buf, e.g, e.root, scanCLIOptions{Full: true, Model: "fake:" + empty, Concurrency: 1}, noEnv)
	if code != 4 || err != nil {
		t.Fatalf("code=%d err=%v\n%s", code, err, buf.String())
	}
	var rep scan.FullReport
	if err := json.Unmarshal(buf.Bytes(), &rep); err != nil {
		t.Fatal(err)
	}
	if rep.Complete || len(rep.Failed) != 3 {
		t.Fatalf("report = %+v", rep)
	}
}

func TestScanCmd_NoProfileWritesSeededProfile(t *testing.T) {
	e := scanRepoEnv(t)
	out := t.TempDir()
	var buf bytes.Buffer
	code, err := runScanFull(context.Background(), &buf, e.g, e.root, scanCLIOptions{
		Full: true, NoProfile: true, Out: out,
		Model: "fake:" + filepath.Join("testdata", "extract-fake"), Concurrency: 1,
	}, noEnv)
	if code != 0 || err != nil {
		t.Fatalf("code=%d err=%v\n%s", code, err, buf.String())
	}
	raw, err := os.ReadFile(filepath.Join(out, ".depbot", "profile.json"))
	if err != nil {
		t.Fatalf("profile.json not written: %v", err)
	}
	pf, err := scan.ParseProfileFile(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !pf.Seeded || pf.SeedReason != "profile disabled (--no-profile)" {
		t.Fatalf("seeded=%v reason=%q", pf.Seeded, pf.SeedReason)
	}
	if _, err := os.Stat(filepath.Join(out, ".depbot", "graph.jsonl")); err != nil {
		t.Fatalf("graph.jsonl not written next to the profile: %v", err)
	}
}

func TestScanCmd_ProfileFlagsRegistered(t *testing.T) {
	cmd := newScanCmd()
	for _, n := range []string{"profile-model", "no-profile", "out"} {
		if cmd.Flags().Lookup(n) == nil {
			t.Errorf("flag --%s missing", n)
		}
	}
}
