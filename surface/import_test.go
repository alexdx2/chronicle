package surface

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/store"
)

// writeVariant copies the fixture with literal substitutions applied and
// returns the path of the copy.
func writeVariant(t *testing.T, name string, subs ...[2]string) string {
	t.Helper()
	raw, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	for _, sub := range subs {
		if !strings.Contains(s, sub[0]) {
			t.Fatalf("fixture has no %q to replace", sub[0])
		}
		s = strings.Replace(s, sub[0], sub[1], 1)
	}
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func countRevisions(t *testing.T, s *store.Store) int {
	t.Helper()
	var n int
	if err := s.QueryRowScan(`SELECT COUNT(*) FROM graph_revisions WHERE domain_key='mini'`, &n); err != nil {
		t.Fatal(err)
	}
	return n
}

func uiNodes(t *testing.T, s *store.Store, status string) []store.NodeRow {
	t.Helper()
	rows, err := s.ListNodes(store.NodeFilter{Layer: "ui", Domain: "mini", Status: status})
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestImportFirstRun(t *testing.T) {
	g := setupSeededGraph(t)
	f := loadFixture(t)
	before := countRevisions(t, g.Store())

	res, err := Import(g, f, ImportOptions{Domain: "mini"})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if res.RevisionID == 0 || res.AlreadyImported {
		t.Fatalf("result = %+v", res)
	}
	if res.Nodes != 5 || res.Edges != 9 {
		t.Fatalf("counts = %d nodes / %d edges", res.Nodes, res.Edges)
	}
	if res.Evidence == 0 {
		t.Fatal("no evidence written")
	}
	if len(res.Deleted) != 0 || len(res.Unresolved) != 0 {
		t.Fatalf("deleted %v unresolved %v", res.Deleted, res.Unresolved)
	}
	if got := countRevisions(t, g.Store()); got != before+1 {
		t.Fatalf("revisions %d → %d", before, got)
	}

	rev, err := g.Store().GetRevision(res.RevisionID)
	if err != nil {
		t.Fatal(err)
	}
	if rev.TriggerKind != "manual" || rev.Mode != "incremental" || rev.GitAfterSHA != f.Commit {
		t.Fatalf("revision = %+v", rev)
	}
	var md map[string]any
	if err := json.Unmarshal([]byte(rev.Metadata), &md); err != nil {
		t.Fatalf("metadata %q: %v", rev.Metadata, err)
	}
	if md["layer"] != "ui" || md["product"] != "mini" || md["content_hash"] != f.ContentHash {
		t.Fatalf("revision metadata = %v", md)
	}
	if md["source"] != fixturePath {
		t.Fatalf("metadata source = %v", md["source"])
	}

	if n := len(uiNodes(t, g.Store(), "active")); n != 5 {
		t.Fatalf("active ui nodes = %d", n)
	}
}

func TestImportIsIdempotent(t *testing.T) {
	g := setupSeededGraph(t)
	f := loadFixture(t)
	if _, err := Import(g, f, ImportOptions{Domain: "mini"}); err != nil {
		t.Fatal(err)
	}
	revs := countRevisions(t, g.Store())
	nodes := len(uiNodes(t, g.Store(), "active"))

	again := loadFixture(t)
	res, err := Import(g, again, ImportOptions{Domain: "mini"})
	if err != nil {
		t.Fatalf("second Import: %v", err)
	}
	if !res.AlreadyImported {
		t.Fatalf("second import not recognised: %+v", res)
	}
	if res.Nodes != 0 || res.Edges != 0 || res.Evidence != 0 {
		t.Fatalf("already-imported run wrote %+v", res)
	}
	if got := countRevisions(t, g.Store()); got != revs {
		t.Fatalf("revisions %d → %d", revs, got)
	}
	if got := len(uiNodes(t, g.Store(), "active")); got != nodes {
		t.Fatalf("ui nodes %d → %d", nodes, got)
	}
}

// TestImportSameCommitDifferentContent: the extract changed but the commit it
// claims did not. Knowledge is named by the commit — accepting this would let
// one commit mean two different things.
func TestImportSameCommitDifferentContent(t *testing.T) {
	g := setupSeededGraph(t)
	f := loadFixture(t)
	if _, err := Import(g, f, ImportOptions{Domain: "mini"}); err != nil {
		t.Fatal(err)
	}

	edited, err := Load(writeVariant(t, "edited.surface.json", [2]string{`"label": "komunikacja"`, `"label": "Komunikacja"`}))
	if err != nil {
		t.Fatal(err)
	}
	if edited.ContentHash == f.ContentHash {
		t.Fatal("variant must differ in content")
	}
	if _, err := Import(g, edited, ImportOptions{Domain: "mini"}); !errors.Is(err, ErrCommitChanged) {
		t.Fatalf("err = %v, want ErrCommitChanged", err)
	}
}

// TestImportClosedWorld: a later extract that no longer mentions a control
// tombstones it. The product's own file is the whole truth about the product's
// own surface.
func TestImportClosedWorld(t *testing.T) {
	g := setupSeededGraph(t)
	f := loadFixture(t)
	if _, err := Import(g, f, ImportOptions{Domain: "mini"}); err != nil {
		t.Fatal(err)
	}
	gone := "ui:control:mini:panel-ustawienia-komunikacja-sms-sender-id"

	raw, _ := os.ReadFile(fixturePath)
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	doc["commit"] = "0000000000000000000000000000000000000002"
	var keptNodes []any
	for _, n := range doc["nodes"].([]any) {
		if m := n.(map[string]any); m["id"] == "ui:panel.ustawienia.komunikacja.smsSenderId" {
			continue
		}
		keptNodes = append(keptNodes, n)
	}
	doc["nodes"] = keptNodes
	var keptEdges []any
	for _, e := range doc["edges"].([]any) {
		if m := e.(map[string]any); m["from"] == "ui:panel.ustawienia.komunikacja.smsSenderId" {
			continue
		}
		keptEdges = append(keptEdges, e)
	}
	doc["edges"] = keptEdges
	out, _ := json.Marshal(doc)
	p := filepath.Join(t.TempDir(), "next.surface.json")
	if err := os.WriteFile(p, out, 0o644); err != nil {
		t.Fatal(err)
	}

	next, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	res, err := Import(g, next, ImportOptions{Domain: "mini"})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if len(res.Deleted) != 1 || res.Deleted[0] != gone {
		t.Fatalf("Deleted = %v", res.Deleted)
	}
	row, err := g.Store().GetNodeByKey(gone)
	if err != nil {
		t.Fatal(err)
	}
	if row.Status != "deleted" {
		t.Fatalf("%s status = %q", gone, row.Status)
	}
	if n := len(uiNodes(t, g.Store(), "active")); n != 4 {
		t.Fatalf("active ui nodes = %d, want 4", n)
	}
}

// TestImportLeavesCodeLayerAlone: the surface import is additive on top of a
// scan. Nothing a scan wrote may move.
func TestImportLeavesCodeLayerAlone(t *testing.T) {
	g := setupSeededGraph(t)
	snapshot := func() map[string]string {
		out := map[string]string{}
		for _, layer := range []string{"data", "contract"} {
			rows, err := g.Store().ListNodes(store.NodeFilter{Layer: layer, Domain: "mini"})
			if err != nil {
				t.Fatal(err)
			}
			for _, r := range rows {
				out[r.NodeKey] = r.Status + "|" + itoa(r.LastSeenRevisionID)
			}
		}
		return out
	}
	before := snapshot()
	beforeRev, err := g.Store().GetLatestRevision("mini")
	if err != nil {
		t.Fatal(err)
	}

	f := loadFixture(t)
	if _, err := Import(g, f, ImportOptions{Domain: "mini"}); err != nil {
		t.Fatal(err)
	}

	after := snapshot()
	if len(before) != len(after) {
		t.Fatalf("node count %d → %d", len(before), len(after))
	}
	for k, v := range before {
		if after[k] != v {
			t.Fatalf("%s: %q → %q", k, v, after[k])
		}
	}
	// The scan's own revision row is untouched — the import made a new one.
	stillThere, err := g.Store().GetRevision(beforeRev.RevisionID)
	if err != nil {
		t.Fatal(err)
	}
	if stillThere.Metadata != beforeRev.Metadata || stillThere.Mode != beforeRev.Mode {
		t.Fatalf("scan revision changed: %+v → %+v", beforeRev, stillThere)
	}
}

// --- git ancestry ------------------------------------------------------

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// sideBranchRepo builds a repo whose HEAD does NOT contain the returned SHA.
func sideBranchRepo(t *testing.T) (dir, sideSHA string) {
	t.Helper()
	dir = t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-q", "-m", "a")
	git(t, dir, "checkout", "-q", "-b", "side")
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-q", "-m", "b")
	sideSHA = git(t, dir, "rev-parse", "HEAD")
	git(t, dir, "checkout", "-q", "main")
	return dir, sideSHA
}

func TestImportRefusesDivergedCommit(t *testing.T) {
	repo, side := sideBranchRepo(t)
	g := setupSeededGraph(t)
	f, err := Load(writeVariant(t, "side.surface.json",
		[2]string{"0000000000000000000000000000000000000001", side}))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := Import(g, f, ImportOptions{Domain: "mini", RepoDir: repo}); !errors.Is(err, ErrDiverged) {
		t.Fatalf("err = %v, want ErrDiverged", err)
	}
	if n := len(uiNodes(t, g.Store(), "active")); n != 0 {
		t.Fatalf("diverged import wrote %d ui nodes", n)
	}

	res, err := Import(g, f, ImportOptions{Domain: "mini", RepoDir: repo, AllowDiverged: true})
	if err != nil {
		t.Fatalf("AllowDiverged: %v", err)
	}
	if !res.Diverged || res.Nodes != 5 {
		t.Fatalf("result = %+v", res)
	}
}

func TestImportRefusesUnknownCommit(t *testing.T) {
	repo, _ := sideBranchRepo(t)
	g := setupSeededGraph(t)
	f := loadFixture(t) // commit 000...001 exists in no repo
	_, err := Import(g, f, ImportOptions{Domain: "mini", RepoDir: repo})
	if err == nil || !strings.Contains(err.Error(), "0000000000000000000000000000000000000001") {
		t.Fatalf("err = %v", err)
	}
}

func TestImportAncestorCommitIsAccepted(t *testing.T) {
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-q", "-m", "a")
	first := git(t, dir, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-q", "-m", "b")

	g := setupSeededGraph(t)
	f, err := Load(writeVariant(t, "anc.surface.json",
		[2]string{"0000000000000000000000000000000000000001", first}))
	if err != nil {
		t.Fatal(err)
	}
	res, err := Import(g, f, ImportOptions{Domain: "mini", RepoDir: dir})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if res.Diverged || res.Nodes != 5 {
		t.Fatalf("result = %+v", res)
	}
}

// TestImportUnresolvedIsRefusedByDefault mirrors Plan's contract at the import
// level: nothing is written when a name does not resolve.
func TestImportUnresolvedIsRefusedByDefault(t *testing.T) {
	g := setupSeededGraph(t)
	f, err := Load(writeVariant(t, "ghost.surface.json",
		[2]string{`"via": ["saveShopSettings"]`, `"via": ["ghostMutation"]`}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Import(g, f, ImportOptions{Domain: "mini"}); err == nil {
		t.Fatal("expected unresolved import to fail")
	}
	if n := len(uiNodes(t, g.Store(), "active")); n != 0 {
		t.Fatalf("refused import wrote %d ui nodes", n)
	}
	if n := countRevisions(t, g.Store()); n != 1 {
		t.Fatalf("refused import left %d revisions", n)
	}

	res, err := Import(g, f, ImportOptions{Domain: "mini", AllowUnresolved: true})
	if err != nil {
		t.Fatalf("AllowUnresolved: %v", err)
	}
	if len(res.Unresolved) != 1 || res.Unresolved[0].Name != "ghostMutation" {
		t.Fatalf("unresolved = %+v", res.Unresolved)
	}
	if res.Nodes != 5 || res.Edges != 8 {
		t.Fatalf("counts = %d/%d", res.Nodes, res.Edges)
	}
}

func TestImportDefaultDomain(t *testing.T) {
	g := setupSeededGraph(t)
	f := loadFixture(t)
	res, err := Import(g, f, ImportOptions{}) // store has exactly one domain
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if res.Nodes != 5 {
		t.Fatalf("result = %+v", res)
	}
}

func itoa(v int64) string { return strconv.FormatInt(v, 10) }
