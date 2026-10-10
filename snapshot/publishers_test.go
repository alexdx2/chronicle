// depbot/snapshot/publishers_test.go
package snapshot

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// sshKeyT generates an ed25519 keypair and returns (privateKeyPath, allowedSignersLine).
func sshKeyT(t *testing.T, principal string) (string, string) {
	t.Helper()
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("ssh-keygen not available")
	}
	dir := t.TempDir()
	key := filepath.Join(dir, "id")
	if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", key).CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen: %v\n%s", err, out)
	}
	pub, err := os.ReadFile(key + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	fields := strings.Fields(string(pub)) // "ssh-ed25519 AAAA... comment"
	return key, principal + " namespaces=\"git\" " + fields[0] + " " + fields[1]
}

func runGitEnvT(t *testing.T, dir string, env []string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(env, "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func captureGitEnvT(t *testing.T, dir string, env []string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(env, "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}

// captureGitAllowFailT returns "" instead of failing when git exits non-zero.
func captureGitAllowFailT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// signedCommitT creates a commit (optionally SSH-signed with keyPath) whose tree is the current index and returns its sha.
func signedCommitT(t *testing.T, repo, keyPath, msg string, parents ...string) string {
	t.Helper()
	tree := captureGitT(t, repo, "write-tree")
	args := []string{}
	if keyPath != "" {
		args = append(args, "-c", "gpg.format=ssh", "-c", "user.signingkey="+keyPath)
	}
	args = append(args, "commit-tree")
	if keyPath != "" {
		args = append(args, "-S")
	}
	args = append(args, tree, "-m", msg)
	for _, p := range parents {
		args = append(args, "-p", p)
	}
	return captureGitEnvT(t, repo, os.Environ(), args...)
}

// publicationMsg builds a commit message with the Chronicle trailers.
func publicationMsg(codeSha, snapID, codeRef, base string, force bool) string {
	msg := "chronicle snapshot for " + codeSha + "\n\n" +
		TrailerSnapshotID + ": " + snapID + "\n" +
		TrailerCodeSha + ": " + codeSha + "\n" +
		TrailerCodeRef + ": " + codeRef + "\n" +
		TrailerSchema + ": 1\n"
	if base != "" {
		msg += TrailerBase + ": " + base + "\n"
	}
	if force {
		msg += TrailerForce + ": true\n"
	}
	return msg
}

// setOriginDefaultT writes a manifest with the given publishers block onto a
// fake origin default branch refs/remotes/origin/main and points origin/HEAD at it.
// addOriginT configures an origin remote (once).
func addOriginT(t *testing.T, repo string) {
	t.Helper()
	if captureGitAllowFailT(t, repo, "remote", "get-url", "origin") == "" {
		runGitT(t, repo, "remote", "add", "origin", "/dev/null")
	}
}

func setOriginDefaultT(t *testing.T, repo, publishersYAML string) {
	t.Helper()
	addOriginT(t, repo)
	writeFileT(t, repo, ".depbot/chronicle.domain.yaml", "domains:\n  d: {name: D}\n"+publishersYAML)
	runGitT(t, repo, "add", "-f", ".depbot/chronicle.domain.yaml")
	c := signedCommitT(t, repo, "", "manifest on origin default")
	runGitT(t, repo, "update-ref", "refs/remotes/origin/main", c)
	runGitT(t, repo, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
	runGitT(t, repo, "reset", "-q") // leave the working tree alone, drop index changes
}

func TestLoadVerifier_TrustAllWithoutPublishers(t *testing.T) {
	repo := initGitRepoT(t)
	setOriginDefaultT(t, repo, "")
	v, err := LoadVerifier(repo)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	if v.Mode != TrustAll || v.GitArgs() != nil {
		t.Fatalf("no publishers ⇒ TrustAll with no git args: %+v", v)
	}
	if !v.Trusted(Publication{Signature: SigNone}) || !v.Trusted(Publication{Signature: SigUnchecked}) {
		t.Fatal("TrustAll must trust unsigned publications")
	}
	if len(v.Warnings) != 1 || !strings.Contains(v.Warnings[0], "publishers") {
		t.Fatalf("TrustAll must warn once about missing publishers: %v", v.Warnings)
	}
}

func TestLoadVerifier_VerifySignedFromOriginDefault(t *testing.T) {
	_, signer := sshKeyT(t, "ci@test")
	repo := initGitRepoT(t)
	setOriginDefaultT(t, repo, "publishers:\n  allowed_signers: |\n    "+signer+"\n")
	v, err := LoadVerifier(repo)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	if v.Mode != VerifySigned || v.Source != "refs/remotes/origin/main" {
		t.Fatalf("%+v", v)
	}
	args := strings.Join(v.GitArgs(), " ")
	if !strings.Contains(args, "gpg.ssh.allowedSignersFile=") {
		t.Fatalf("GitArgs must configure allowedSignersFile: %q", args)
	}
	if v.Trusted(Publication{Signature: SigNone}) || v.Trusted(Publication{Signature: SigUntrusted}) || v.Trusted(Publication{Signature: SigBad}) {
		t.Fatal("VerifySigned must trust only SigGood")
	}
	if !v.Trusted(Publication{Signature: SigGood}) {
		t.Fatal("SigGood must be trusted")
	}
}

func TestLoadVerifier_WorkingTreeManifestIgnored(t *testing.T) {
	_, signer := sshKeyT(t, "ci@test")
	repo := initGitRepoT(t)
	setOriginDefaultT(t, repo, "") // origin default: no publishers
	// Working tree (and even a local commit on main) adds a key — must not matter.
	writeFileT(t, repo, ".depbot/chronicle.domain.yaml", "domains:\n  d: {name: D}\npublishers:\n  allowed_signers: |\n    "+signer+"\n")
	runGitT(t, repo, "add", "-f", ".depbot/chronicle.domain.yaml")
	runGitT(t, repo, "commit", "-q", "-m", "local attempt to add a key")
	v, err := LoadVerifier(repo)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	if v.Mode != TrustAll {
		t.Fatalf("working tree / local branch must not influence trust: %+v", v)
	}
}

func TestLoadVerifier_RevokedFileWhenConfigured(t *testing.T) {
	_, signer := sshKeyT(t, "ci@test")
	repo := initGitRepoT(t)
	setOriginDefaultT(t, repo, "publishers:\n  allowed_signers: |\n    "+signer+"\n  revoked: |\n    "+strings.Join(strings.Fields(signer)[2:4], " ")+"\n")
	v, err := LoadVerifier(repo)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	if !strings.Contains(strings.Join(v.GitArgs(), " "), "gpg.ssh.revocationFile=") {
		t.Fatal("revoked keys must be passed as gpg.ssh.revocationFile")
	}
}

func TestDefaultBranchRef(t *testing.T) {
	repo := initGitRepoT(t)
	if got, err := DefaultBranchRef(repo); err != nil || got != "HEAD" {
		t.Fatalf("empty repo without origin: want HEAD, got %s, %v", got, err)
	}
	writeFileT(t, repo, "f", "1")
	runGitT(t, repo, "add", "f")
	runGitT(t, repo, "commit", "-q", "-m", "c")
	if got, err := DefaultBranchRef(repo); err != nil || got != "refs/heads/main" {
		t.Fatalf("local main only: want refs/heads/main, got %s, %v", got, err)
	}
	runGitT(t, repo, "update-ref", "refs/remotes/origin/main", captureGitT(t, repo, "rev-parse", "HEAD"))
	if got, err := DefaultBranchRef(repo); err != nil || got != "refs/remotes/origin/main" {
		t.Fatalf("origin/main present: got %s, %v", got, err)
	}
	runGitT(t, repo, "update-ref", "refs/remotes/origin/trunk", captureGitT(t, repo, "rev-parse", "HEAD"))
	runGitT(t, repo, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/trunk")
	if got, err := DefaultBranchRef(repo); err != nil || got != "refs/remotes/origin/trunk" {
		t.Fatalf("origin/HEAD wins: got %s, %v", got, err)
	}
}

func TestLoadVerifier_UnresolvableDefaultErrors(t *testing.T) {
	repo := initGitRepoT(t)
	writeFileT(t, repo, "f", "1")
	runGitT(t, repo, "add", "f")
	runGitT(t, repo, "commit", "-q", "-m", "c")
	addOriginT(t, repo)
	runGitT(t, repo, "update-ref", "refs/remotes/origin/feature", captureGitT(t, repo, "rev-parse", "HEAD"))
	if got, err := DefaultBranchRef(repo); err == nil {
		t.Fatalf("origin without a default branch must error, got %s", got)
	}
	if v, err := LoadVerifier(repo); err == nil {
		t.Fatalf("LoadVerifier must propagate the error, got %+v", v)
	}
}

func TestLoadVerifier_ManifestAbsentIsTrustAll(t *testing.T) {
	repo := initGitRepoT(t)
	addOriginT(t, repo)
	writeFileT(t, repo, "f", "1")
	runGitT(t, repo, "add", "f")
	c := signedCommitT(t, repo, "", "no manifest")
	runGitT(t, repo, "update-ref", "refs/remotes/origin/main", c)
	runGitT(t, repo, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
	v, err := LoadVerifier(repo)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	if v.Mode != TrustAll || len(v.Warnings) != 1 || !strings.Contains(v.Warnings[0], "no .depbot/chronicle.domain.yaml") {
		t.Fatalf("absent manifest ⇒ TrustAll with warning: %+v", v)
	}
}

func TestLoadVerifier_MalformedManifestErrors(t *testing.T) {
	repo := initGitRepoT(t)
	addOriginT(t, repo)
	writeFileT(t, repo, ".depbot/chronicle.domain.yaml", "domains: [\n")
	runGitT(t, repo, "add", "-f", ".depbot/chronicle.domain.yaml")
	c := signedCommitT(t, repo, "", "bad manifest")
	runGitT(t, repo, "update-ref", "refs/remotes/origin/main", c)
	runGitT(t, repo, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
	if v, err := LoadVerifier(repo); err == nil {
		t.Fatalf("malformed manifest must error, got %+v", v)
	}
}

func TestLoadVerifier_NotARepoErrors(t *testing.T) {
	if v, err := LoadVerifier(t.TempDir()); err == nil {
		t.Fatalf("not a repository must error, got %+v", v)
	}
}

func TestVerifier_CloseRemovesFiles(t *testing.T) {
	_, signer := sshKeyT(t, "ci@test")
	repo := initGitRepoT(t)
	setOriginDefaultT(t, repo, "publishers:\n  allowed_signers: |\n    "+signer+"\n")
	v, err := LoadVerifier(repo)
	if err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(v.allowedPath)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("allowed_signers must exist with mode 0600: %v %v", st, err)
	}
	v.Close()
	if _, err := os.Stat(v.allowedPath); !os.IsNotExist(err) {
		t.Fatalf("Close must remove the files: %v", err)
	}
}
