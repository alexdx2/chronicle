// depbot/snapshot/version_test.go
package snapshot

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// codeCommitT makes a plain code commit on the current branch and returns its sha.
func codeCommitT(t *testing.T, repo, file, content, msg string) string {
	t.Helper()
	writeFileT(t, repo, file, content)
	runGitT(t, repo, "add", file)
	runGitT(t, repo, "commit", "-q", "-m", msg)
	return captureGitT(t, repo, "rev-parse", "HEAD")
}

// graphFilesT writes a minimal valid graph.jsonl (+manifest) into a scratch dir and returns (graphBytes, manifestBytes).
func graphFilesT(t *testing.T, seq int, nodeName string) ([]byte, []byte) {
	t.Helper()
	s := twoNodeSnapshot(seq)
	s.Nodes[1].Name = nodeName
	return encodeT(t, s), []byte("domains:\n  d: {name: D}\n")
}

// publishT creates a publication commit for codeSha (tree = graph + manifest), optionally signed,
// and moves ref to it (parent = previous ref tip when ref already exists). Returns the publication sha.
func publishT(t *testing.T, repo, ref, codeSha, codeRef, keyPath string, graph, man []byte, force bool) string {
	t.Helper()
	idx := filepath.Join(t.TempDir(), "index")
	env := append(os.Environ(), "GIT_INDEX_FILE="+idx)
	scratch := t.TempDir()
	writeFileT(t, scratch, ".depbot/graph.jsonl", string(graph))
	writeFileT(t, scratch, ".depbot/chronicle.domain.yaml", string(man))
	for _, f := range []string{".depbot/graph.jsonl", ".depbot/chronicle.domain.yaml"} {
		blob := captureGitT(t, repo, "hash-object", "-w", filepath.Join(scratch, f))
		runGitEnvT(t, repo, env, "update-index", "--add", "--cacheinfo", "100644,"+blob+","+f)
	}
	tree := captureGitEnvT(t, repo, env, "write-tree")
	id, err := ContentID(graph, man, nil)
	if err != nil {
		t.Fatal(err)
	}
	args := []string{}
	if keyPath != "" {
		args = append(args, "-c", "gpg.format=ssh", "-c", "user.signingkey="+keyPath)
	}
	args = append(args, "commit-tree")
	if keyPath != "" {
		args = append(args, "-S")
	}
	args = append(args, tree, "-m", publicationMsg(codeSha, id, codeRef, "", force))
	if tip := captureGitAllowFailT(t, repo, "rev-parse", "--verify", "-q", ref); tip != "" {
		args = append(args, "-p", tip)
	}
	c := captureGitEnvT(t, repo, os.Environ(), args...)
	runGitT(t, repo, "update-ref", ref, c)
	return c
}

func TestResolveVersion_FallbackInTreeHistory(t *testing.T) {
	repo := initGitRepoT(t)
	g, m := graphFilesT(t, 1, "A")
	writeFileT(t, repo, ".depbot/graph.jsonl", string(g))
	writeFileT(t, repo, ".depbot/chronicle.domain.yaml", string(m))
	runGitT(t, repo, "add", "-f", ".depbot")
	runGitT(t, repo, "commit", "-q", "-m", "snapshot in tree")
	graphCommit := captureGitT(t, repo, "rev-parse", "HEAD")
	later := codeCommitT(t, repo, "src/a.go", "package a", "code after")
	v, err := ResolveVersion(repo, later)
	if err != nil {
		t.Fatal(err)
	}
	if v.Commit != graphCommit || v.Source != "tree" || v.Seq != 1 || v.Blob == "" {
		t.Fatalf("fallback must behave like before: %+v", v)
	}
}

func TestResolveVersion_NoVersion(t *testing.T) {
	repo := initGitRepoT(t)
	c := codeCommitT(t, repo, "f", "x", "c")
	if _, err := ResolveVersion(repo, c); !errors.Is(err, ErrNoVersion) {
		t.Fatalf("want ErrNoVersion, got %v", err)
	}
}

func TestResolveVersion_ChainExactAndNearest(t *testing.T) {
	repo := initGitRepoT(t)
	setOriginDefaultT(t, repo, "") // TrustAll
	c1 := codeCommitT(t, repo, "f", "1", "c1")
	c2 := codeCommitT(t, repo, "f", "2", "c2")
	c3 := codeCommitT(t, repo, "f", "3", "c3")
	g1, m := graphFilesT(t, 1, "G1")
	g2, _ := graphFilesT(t, 2, "G2")
	p1 := publishT(t, repo, ChainRefPrefix+"main", c1, "main", "", g1, m, false)
	p2 := publishT(t, repo, ChainRefPrefix+"main", c2, "main", "", g2, m, false)
	cases := []struct{ rev, pub, code string }{{c1, p1, c1}, {c2, p2, c2}, {c3, p2, c2}}
	for _, tc := range cases {
		v, err := ResolveVersion(repo, tc.rev)
		if err != nil {
			t.Fatalf("%s: %v", tc.rev, err)
		}
		if v.Commit != tc.pub || v.CodeSha != tc.code || v.Source != "chain" || v.Ref != ChainRefPrefix+"main" {
			t.Fatalf("rev %s: %+v", tc.rev, v)
		}
		if v.Seq == 0 || v.Blob == "" || len(v.SnapshotID) != 64 {
			t.Fatalf("identity fields must be filled: %+v", v)
		}
		snap, _, err := AtCommit(repo, v)
		if err != nil || len(snap.Nodes) != 2 {
			t.Fatalf("AtCommit on a publication: %v %+v", err, snap)
		}
	}
}

func TestResolveVersion_FeatureBranchFallsBackToChain(t *testing.T) {
	repo := initGitRepoT(t)
	setOriginDefaultT(t, repo, "")
	c1 := codeCommitT(t, repo, "f", "1", "c1")
	g, m := graphFilesT(t, 1, "G1")
	p1 := publishT(t, repo, ChainRefPrefix+"main", c1, "main", "", g, m, false)
	runGitT(t, repo, "checkout", "-q", "-b", "feature")
	f1 := codeCommitT(t, repo, "f", "feature", "feature work")
	v, err := ResolveVersion(repo, f1)
	if err != nil {
		t.Fatal(err)
	}
	if v.Commit != p1 || v.CodeSha != c1 {
		t.Fatalf("feature sha must resolve to the merge-base publication: %+v", v)
	}
}

func TestResolveVersion_HeadsExcludedUnlessIncluded(t *testing.T) {
	repo := initGitRepoT(t)
	setOriginDefaultT(t, repo, "")
	c1 := codeCommitT(t, repo, "f", "1", "c1")
	gChain, m := graphFilesT(t, 1, "CHAIN")
	gHead, _ := graphFilesT(t, 1, "HEAD")
	pChain := publishT(t, repo, ChainRefPrefix+"main", c1, "main", "", gChain, m, false)
	pHead := publishT(t, repo, HeadRefPrefix+c1, c1, "merge-request/1", "", gHead, m, false)
	v, _ := ResolveVersion(repo, c1)
	if v.Commit != pChain {
		t.Fatalf("heads must be ignored by default: %+v", v)
	}
	v, _ = ResolveVersionWith(repo, c1, ResolveOptions{IncludeHeads: []string{c1}})
	if v.Commit != pHead || v.Source != "head" {
		t.Fatalf("included head must win over the chain for its own sha: %+v", v)
	}
}

func TestResolveVersion_ClosestToTipWinsOnForce(t *testing.T) {
	repo := initGitRepoT(t)
	setOriginDefaultT(t, repo, "")
	c1 := codeCommitT(t, repo, "f", "1", "c1")
	gA, m := graphFilesT(t, 1, "A")
	gB, _ := graphFilesT(t, 1, "B")
	publishT(t, repo, ChainRefPrefix+"main", c1, "main", "", gA, m, false)
	pForced := publishT(t, repo, ChainRefPrefix+"main", c1, "main", "", gB, m, true)
	v, _ := ResolveVersion(repo, c1)
	if v.Commit != pForced {
		t.Fatalf("the forced (closest to tip) publication must win: %+v", v)
	}
}

func TestResolveVersion_RemoteTwinWins(t *testing.T) {
	repo := initGitRepoT(t)
	setOriginDefaultT(t, repo, "")
	c1 := codeCommitT(t, repo, "f", "1", "c1")
	gOld, m := graphFilesT(t, 1, "OLD")
	gNew, _ := graphFilesT(t, 1, "NEW")
	publishT(t, repo, ChainRefPrefix+"main", c1, "main", "", gOld, m, false) // stale local
	pRemote := publishT(t, repo, RemoteChainRefPrefix+"main", c1, "main", "", gNew, m, false)
	v, _ := ResolveVersion(repo, c1)
	if v.Commit != pRemote || v.Ref != RemoteChainRefPrefix+"main" {
		t.Fatalf("remote-tracking twin must win over the stale local chain: %+v", v)
	}
}

func TestResolveVersion_VerifySignedIgnoresUnsignedAndForeign(t *testing.T) {
	key, signer := sshKeyT(t, "ci@test")
	foreignKey, _ := sshKeyT(t, "stranger@test")
	repo := initGitRepoT(t)
	setOriginDefaultT(t, repo, "publishers:\n  allowed_signers: |\n    "+signer+"\n")
	c1 := codeCommitT(t, repo, "f", "1", "c1")
	gU, m := graphFilesT(t, 1, "UNSIGNED")
	gF, _ := graphFilesT(t, 1, "FOREIGN")
	gS, _ := graphFilesT(t, 1, "SIGNED")
	pSigned := publishT(t, repo, ChainRefPrefix+"main", c1, "main", key, gS, m, false)
	publishT(t, repo, ChainRefPrefix+"main", c1, "main", foreignKey, gF, m, true)
	publishT(t, repo, ChainRefPrefix+"main", c1, "main", "", gU, m, true)
	tip := captureGitT(t, repo, "rev-parse", ChainRefPrefix+"main")
	tipTree := captureGitT(t, repo, "rev-parse", tip+"^{tree}")
	junk := captureGitEnvT(t, repo, os.Environ(), "commit-tree", tipTree, "-p", tip, "-m", "junk")
	runGitT(t, repo, "update-ref", ChainRefPrefix+"main", junk)
	v, err := ResolveVersion(repo, c1)
	if err != nil {
		t.Fatal(err)
	}
	if v.Commit != pSigned || v.Signature != SigGood {
		t.Fatalf("only the publication signed by an allowed key may win: %+v", v)
	}
	_, warns, err := PublicationMap(repo, ResolveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	all := strings.Join(warns, "\n")
	if len(warns) < 3 || !strings.Contains(all, "not a trusted publication") || !strings.Contains(all, "has no Chronicle trailers") {
		t.Fatalf("unsigned, foreign-signed and trailer-less commits must each be reported: %v", warns)
	}
}

func TestResolveVersion_VerifySignedNoTrustedPublicationIsNoVersion(t *testing.T) {
	_, signer := sshKeyT(t, "ci@test")
	repo := initGitRepoT(t)
	setOriginDefaultT(t, repo, "publishers:\n  allowed_signers: |\n    "+signer+"\n")
	c1 := codeCommitT(t, repo, "f", "1", "c1")
	g, m := graphFilesT(t, 1, "UNSIGNED")
	publishT(t, repo, ChainRefPrefix+"main", c1, "main", "", g, m, false)
	if _, err := ResolveVersion(repo, c1); !errors.Is(err, ErrNoVersion) {
		t.Fatalf("an unsigned chain in VerifySigned mode is no version at all (no in-tree snapshot either): %v", err)
	}
}

func TestResolveVersion_MalformedTrailerSkipped(t *testing.T) {
	repo := initGitRepoT(t)
	setOriginDefaultT(t, repo, "")
	c1 := codeCommitT(t, repo, "f", "1", "c1")
	g, m := graphFilesT(t, 1, "G")
	good := publishT(t, repo, ChainRefPrefix+"main", c1, "main", "", g, m, false)
	// Malformed: Code-Sha is garbage — hand-build the commit on top of the chain.
	tree := captureGitT(t, repo, "rev-parse", good+"^{tree}")
	plain := captureGitEnvT(t, repo, os.Environ(), "commit-tree", tree, "-p", good, "-m", "junk")
	bad := captureGitEnvT(t, repo, os.Environ(), "commit-tree", tree, "-p", plain, "-m", "junk\n\n"+TrailerCodeSha+": not-a-sha\n"+TrailerSnapshotID+": "+strings.Repeat("0", 64)+"\n")
	runGitT(t, repo, "update-ref", ChainRefPrefix+"main", bad)
	v, err := ResolveVersion(repo, c1)
	if err != nil {
		t.Fatal(err)
	}
	if v.Commit != good {
		t.Fatalf("malformed publication must be skipped, not crash or win: %+v", v)
	}
	_, warns, _ := PublicationMap(repo, ResolveOptions{})
	if len(warns) == 0 {
		t.Fatal("malformed trailer must be reported")
	}
}

func TestResolveVersion_MergeHistoryTwoChains(t *testing.T) {
	repo := initGitRepoT(t)
	setOriginDefaultT(t, repo, "")
	base := codeCommitT(t, repo, "f", "0", "base")
	gBase, m := graphFilesT(t, 1, "BASE")
	publishT(t, repo, ChainRefPrefix+"main", base, "main", "", gBase, m, false)
	runGitT(t, repo, "checkout", "-q", "-b", "release-1")
	r1 := codeCommitT(t, repo, "r", "1", "release work")
	gR, _ := graphFilesT(t, 2, "RELEASE")
	pR := publishT(t, repo, ChainRefPrefix+"release-1", r1, "release-1", "", gR, m, false)
	runGitT(t, repo, "checkout", "-q", "main")
	m1 := codeCommitT(t, repo, "f", "1", "main work")
	gM, _ := graphFilesT(t, 2, "MAIN")
	pM := publishT(t, repo, ChainRefPrefix+"main", m1, "main", "", gM, m, false)
	runGitT(t, repo, "merge", "-q", "--no-ff", "-m", "merge release", "release-1")
	merge := captureGitT(t, repo, "rev-parse", "HEAD")
	v, err := ResolveVersion(repo, merge)
	if err != nil {
		t.Fatal(err)
	}
	// git --topo-order guarantees parents after children, not first-parent-first:
	// either child publication may win, the base publication never may.
	if v.Commit != pM && v.Commit != pR {
		t.Fatalf("merge must resolve to one of the two child publications, never the base: %+v (main %s, release %s)", v, pM, pR)
	}
	again, _ := ResolveVersion(repo, merge)
	if again.Commit != v.Commit {
		t.Fatalf("resolution must be deterministic: %s then %s", v.Commit, again.Commit)
	}
	if mv, _ := ResolveVersion(repo, m1); mv.Commit != pM {
		t.Fatalf("main sha resolves to its own chain: %+v", mv)
	}
	rv, _ := ResolveVersion(repo, r1)
	if rv.Commit != pR {
		t.Fatalf("release sha resolves to its own chain: %+v", rv)
	}
}

func TestResolveVersion_OriginWithoutDefaultStillResolvesInTree(t *testing.T) {
	repo := initGitRepoT(t)
	g, m := graphFilesT(t, 1, "A")
	writeFileT(t, repo, ".depbot/graph.jsonl", string(g))
	writeFileT(t, repo, ".depbot/chronicle.domain.yaml", string(m))
	runGitT(t, repo, "add", "-f", ".depbot")
	runGitT(t, repo, "commit", "-q", "-m", "snapshot in tree")
	graphCommit := captureGitT(t, repo, "rev-parse", "HEAD")
	runGitT(t, repo, "remote", "add", "origin", "/dev/null")
	v, err := ResolveVersion(repo, "HEAD")
	if err != nil {
		t.Fatalf("a repo without publication refs must not need a verifier: %v", err)
	}
	if v.Commit != graphCommit || v.Source != "tree" {
		t.Fatalf("%+v", v)
	}
}
