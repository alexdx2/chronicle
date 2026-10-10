// depbot/snapshot/publication_test.go
package snapshot

import (
	"strings"
	"testing"
)

const rec = "\x1e"

func logLine(commit, sig, codeSha, snapID, codeRef, base, schema, force string) string {
	return strings.Join([]string{commit, sig, codeSha + "\n", snapID + "\n", codeRef + "\n", base + "\n", schema + "\n", force}, "\x00") + rec
}

func TestParseLogRecords_Basic(t *testing.T) {
	sha1 := strings.Repeat("a", 40)
	sha2 := strings.Repeat("b", 40)
	out := logLine("1111111111111111111111111111111111111111", "G", sha1, strings.Repeat("0", 64), "main", "", "7", "") +
		logLine("2222222222222222222222222222222222222222", "N", sha2, strings.Repeat("1", 64), "main", strings.Repeat("0", 64), "7", "true")
	pubs, warns := parseLogRecords(out, ChainRefPrefix+"main")
	if len(warns) != 0 {
		t.Fatalf("unexpected warnings: %v", warns)
	}
	if len(pubs) != 2 {
		t.Fatalf("want 2 publications, got %d", len(pubs))
	}
	if pubs[0].Distance != 0 || pubs[1].Distance != 1 {
		t.Fatalf("distance must count from the tip: %+v", pubs)
	}
	if pubs[0].Signature != SigGood || pubs[1].Signature != SigNone {
		t.Fatalf("signature mapping wrong: %+v", pubs)
	}
	if pubs[0].CodeSha != sha1 || pubs[0].SnapshotID != strings.Repeat("0", 64) || pubs[0].CodeRef != "main" {
		t.Fatalf("trailers not trimmed/mapped: %+v", pubs[0])
	}
	if !pubs[1].Force || pubs[1].Base != strings.Repeat("0", 64) {
		t.Fatalf("force/base not parsed: %+v", pubs[1])
	}
}

func TestParseLogRecords_SkipsMalformed(t *testing.T) {
	good := strings.Repeat("c", 40)
	out := logLine("3333333333333333333333333333333333333333", "G", "not-a-sha", strings.Repeat("0", 64), "main", "", "7", "") + // bad code sha
		logLine("4444444444444444444444444444444444444444", "G", good, "short", "main", "", "7", "") + // bad snapshot id
		logLine("5555555555555555555555555555555555555555", "G", "", "", "", "", "", "") + // no trailers at all (plain commit on the branch)
		logLine("6666666666666666666666666666666666666666", "G", good, strings.Repeat("0", 64), "main", "", "7", "")
	pubs, warns := parseLogRecords(out, ChainRefPrefix+"main")
	if len(pubs) != 1 || pubs[0].Commit != "6666666666666666666666666666666666666666" {
		t.Fatalf("only the well-formed record must survive: %+v", pubs)
	}
	if len(warns) != 3 {
		t.Fatalf("each skipped record must produce one warning, got %d: %v", len(warns), warns)
	}
	if pubs[0].Distance != 3 {
		t.Fatalf("distance counts every commit on the ref, skipped ones included: %d", pubs[0].Distance)
	}
}

func TestParseLogRecords_SignatureCodes(t *testing.T) {
	cases := map[string]SignatureStatus{"G": SigGood, "N": SigNone, "B": SigBad, "U": SigUntrusted, "X": SigUntrusted, "Y": SigUntrusted, "R": SigUntrusted, "E": SigUntrusted, "": SigUnchecked}
	for code, want := range cases {
		out := logLine("7777777777777777777777777777777777777777", code, strings.Repeat("d", 40), strings.Repeat("0", 64), "main", "", "7", "")
		pubs, _ := parseLogRecords(out, ChainRefPrefix+"main")
		if len(pubs) != 1 || pubs[0].Signature != want {
			t.Fatalf("%q → %v, want %v", code, pubs, want)
		}
	}
}

func TestHeadRef(t *testing.T) {
	l, r := HeadRef(strings.Repeat("e", 40))
	if l != HeadRefPrefix+strings.Repeat("e", 40) || r != RemoteHeadRefPrefix+strings.Repeat("e", 40) {
		t.Fatal(l, r)
	}
}

func TestChainRefs_RemoteTwinFirstLocalSkipped(t *testing.T) {
	repo := initGitRepoT(t)
	writeFileT(t, repo, "README", "x")
	runGitT(t, repo, "add", "README")
	runGitT(t, repo, "commit", "-q", "-m", "init")
	head := captureGitT(t, repo, "rev-parse", "HEAD")
	runGitT(t, repo, "update-ref", "refs/heads/chronicle/graphs/main", head)
	runGitT(t, repo, "update-ref", "refs/remotes/origin/chronicle/graphs/main", head)
	runGitT(t, repo, "update-ref", "refs/heads/chronicle/graphs/release-1", head)
	runGitT(t, repo, "update-ref", "refs/remotes/origin/chronicle/graphs/release-2", head)
	refs, err := ChainRefs(repo)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"refs/remotes/origin/chronicle/graphs/main",
		"refs/remotes/origin/chronicle/graphs/release-2",
		"refs/heads/chronicle/graphs/release-1",
	}
	if strings.Join(refs, ",") != strings.Join(want, ",") {
		t.Fatalf("got %v want %v", refs, want)
	}
}

func TestParseLogRecords_DuplicateTrailerSkipped(t *testing.T) {
	sha := strings.Repeat("a", 40)
	out := logLine("7777777777777777777777777777777777777777", "G", sha, strings.Repeat("0", 64), "main", "", "7", "true\nfalse")
	pubs, warns := parseLogRecords(out, ChainRefPrefix+"main")
	if len(pubs) != 0 {
		t.Fatalf("a record with a duplicated trailer must be skipped, not parsed with a guessed value: %+v", pubs)
	}
	if len(warns) != 1 || !strings.Contains(warns[0], "duplicated or multi-line trailer") {
		t.Fatalf("want one duplicated-trailer warning, got %v", warns)
	}
}
