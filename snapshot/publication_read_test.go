package snapshot

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// makePublicationT builds a publication commit by hand (core has no
// publish.BuildCommit): blobs through hash-object, a tree through a
// throwaway index, a commit with the Chronicle trailers, and update-ref
// puts it under ref. When ref already exists the new commit is its child.
// types == nil leaves the types file out. Returns the commit sha.
func makePublicationT(t *testing.T, repo, code string, graph, domain, types []byte, ref string) string {
	t.Helper()
	id, err := ContentID(graph, domain, types)
	if err != nil {
		t.Fatal(err)
	}
	idx := t.TempDir() + "/index"
	env := append(os.Environ(), "GIT_INDEX_FILE="+idx,
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	git := func(stdin string, args ...string) string {
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		cmd.Env = env
		cmd.Stdin = strings.NewReader(stdin)
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
		return strings.TrimSpace(string(out))
	}
	add := func(path string, content []byte) {
		blob := git(string(content), "hash-object", "-w", "--stdin")
		git("", "update-index", "--add", "--cacheinfo", "100644,"+blob+","+path)
	}
	add(Path, graph)
	add(ManifestPath, domain)
	if types != nil {
		add(TypesPath, types)
	}
	tree := git("", "write-tree")
	msg := "publish " + code + "\n\n" + TrailerCodeSha + ": " + code + "\n" + TrailerSnapshotID + ": " + id + "\n"
	args := []string{"commit-tree", tree}
	if parent := captureGitAllowFailT(t, repo, "rev-parse", "-q", "--verify", ref); parent != "" {
		args = append(args, "-p", parent)
	}
	commit := git(msg, args...)
	git("", "update-ref", ref, commit)
	return commit
}

func pubFilesT(t *testing.T) (graph, domain []byte) {
	t.Helper()
	snap := &Snapshot{Header: Header{Kind: "header", Format: Format, Version: Version, Seq: 1, FactSchema: FactSchema}}
	return []byte(encodeSnapshotT(t, snap)), []byte("domains:\n  d: {name: D}\n")
}

func TestReadPublicationFiles_WithAndWithoutTypes(t *testing.T) {
	repo := initGitRepoT(t)
	graph, domain := pubFilesT(t)
	types := []byte("types: {}\n")
	code := strings.Repeat("a", 40)
	withTypes := makePublicationT(t, repo, code, graph, domain, types, HeadRefPrefix+code)
	withoutTypes := makePublicationT(t, repo, code, graph, domain, nil, HeadRefPrefix+strings.Repeat("b", 40))

	f, err := ReadPublicationFiles(repo, withTypes)
	if err != nil {
		t.Fatal(err)
	}
	if string(f.Graph) != string(graph) || string(f.Domain) != string(domain) || string(f.Types) != string(types) {
		t.Fatalf("files not byte-identical: %+v", f)
	}
	f, err = ReadPublicationFiles(repo, withoutTypes)
	if err != nil {
		t.Fatal(err)
	}
	if f.Types != nil {
		t.Fatalf("Types must be nil when the file is absent, got %q", f.Types)
	}
	if string(f.Graph) != string(graph) {
		t.Fatal("graph differs")
	}
}

func TestReadPublicationFiles_Profile(t *testing.T) {
	repo := initGitRepoT(t)
	graph, domain := pubFilesT(t)
	code := strings.Repeat("c", 40)
	without := makePublicationT(t, repo, code, graph, domain, nil, HeadRefPrefix+code)
	f, err := ReadPublicationFiles(repo, without)
	if err != nil || f.Profile != nil {
		t.Fatalf("absent profile: %v %q", err, f.Profile)
	}
	// add profile.json on top of that tree
	idx := t.TempDir() + "/index"
	env := append(os.Environ(), "GIT_INDEX_FILE="+idx, "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	git := func(stdin string, args ...string) string {
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		cmd.Env = env
		cmd.Stdin = strings.NewReader(stdin)
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
		return strings.TrimSpace(string(out))
	}
	git("", "read-tree", without)
	blob := git(`{"p":1}`, "hash-object", "-w", "--stdin")
	git("", "update-index", "--add", "--cacheinfo", "100644,"+blob+","+ProfilePath)
	with := git("x", "commit-tree", git("", "write-tree"), "-p", without)
	f, err = ReadPublicationFiles(repo, with)
	if err != nil || string(f.Profile) != `{"p":1}` {
		t.Fatalf("profile: %v %q", err, f.Profile)
	}
}

func TestReadPublicationFiles_MissingGraphIsError(t *testing.T) {
	repo := initGitRepoT(t)
	writeFileT(t, repo, ManifestPath, "domains:\n  d: {name: D}\n")
	commit := commitAllT(t, repo, "no graph")
	if _, err := ReadPublicationFiles(repo, commit); err == nil {
		t.Fatal("a tree without graph.jsonl must be an error")
	}
}

func TestReadPublicationFiles_RejectsOptionShapedCommit(t *testing.T) {
	repo := t.TempDir() // not even a git repo: git must never run
	if _, err := ReadPublicationFiles(repo, "--upload-pack=x"); !errors.Is(err, ErrBadSha) {
		t.Fatalf("err = %v, want ErrBadSha", err)
	}
}

func TestHeadRefs_RemoteFirstNoDuplicates(t *testing.T) {
	repo := initGitRepoT(t)
	writeFileT(t, repo, "README", "x")
	head := commitAllT(t, repo, "init")
	both := strings.Repeat("a", 40)
	localOnly := strings.Repeat("b", 40)
	runGitT(t, repo, "update-ref", HeadRefPrefix+both, head)
	runGitT(t, repo, "update-ref", RemoteHeadRefPrefix+both, head)
	runGitT(t, repo, "update-ref", HeadRefPrefix+localOnly, head)
	refs, err := HeadRefs(repo)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{RemoteHeadRefPrefix + both, HeadRefPrefix + localOnly}
	if strings.Join(refs, ",") != strings.Join(want, ",") {
		t.Fatalf("refs = %v, want %v", refs, want)
	}
}

func twoPublicationsT(t *testing.T) (repo, ref, parent, tip string) {
	t.Helper()
	repo = initGitRepoT(t)
	graph, domain := pubFilesT(t)
	code := strings.Repeat("c", 40)
	ref = ChainRefPrefix + "main"
	parent = makePublicationT(t, repo, code, graph, domain, nil, ref)
	tip = makePublicationT(t, repo, strings.Repeat("d", 40), graph, domain, nil, ref)
	return
}

func TestRefPublications_ParsesTrailersTipFirst(t *testing.T) {
	repo, ref, parent, tip := twoPublicationsT(t)
	pubs, warns, err := RefPublications(repo, ref, nil)
	if err != nil || len(warns) != 0 {
		t.Fatalf("err=%v warns=%v", err, warns)
	}
	if len(pubs) != 2 || pubs[0].Commit != tip || pubs[1].Commit != parent {
		t.Fatalf("want [tip, parent], got %+v", pubs)
	}
	graph, domain := pubFilesT(t)
	id, _ := ContentID(graph, domain, nil)
	if pubs[0].CodeSha != strings.Repeat("d", 40) || pubs[1].CodeSha != strings.Repeat("c", 40) || pubs[0].SnapshotID != id {
		t.Fatalf("trailers not mapped: %+v", pubs)
	}
}

func TestRefPublications_VerifierFiltersUntrusted(t *testing.T) {
	repo, ref, _, _ := twoPublicationsT(t)
	// VerifySigned with no signers trusts nobody (fail-closed, like
	// Resolver.versionFor builds it).
	pubs, _, err := RefPublications(repo, ref, &Verifier{Mode: VerifySigned})
	if err != nil {
		t.Fatal(err)
	}
	if len(pubs) != 0 {
		t.Fatalf("untrusted publications must be dropped, got %+v", pubs)
	}
	pubs, _, err = RefPublications(repo, ref, nil)
	if err != nil || len(pubs) != 2 {
		t.Fatalf("v == nil must not check signatures: %v %+v", err, pubs)
	}
}
