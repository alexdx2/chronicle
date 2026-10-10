// depbot/snapshot/publishers.go
package snapshot

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/alexdx2/chronicle-core/gitdiff"
	"github.com/alexdx2/chronicle-core/manifest"
)

// TrustMode says how publications on chronicle/graphs/* are trusted.
type TrustMode int

const (
	// TrustAll: no publishers configured (OSS, one developer, one clone).
	// Every chain commit counts; LoadVerifier warns once.
	TrustAll TrustMode = iota
	// VerifySigned: only commits with a good SSH signature from the
	// manifest's publishers.allowed_signers count (spec §4.2).
	VerifySigned
)

// ManifestPath is where the domain manifest lives in a tree.
const ManifestPath = ".depbot/chronicle.domain.yaml"

// Verifier carries the trust configuration for one repository. Build it
// with LoadVerifier, pass GitArgs() to every git log that reads trailers,
// and Close() it to drop the temp files.
type Verifier struct {
	Mode     TrustMode
	Source   string   // ref the manifest was read from ("" in TrustAll)
	Warnings []string // human-readable, for doctor / CLI output

	dir         string
	allowedPath string
	revokedPath string
}

// DefaultBranchRef is the ref whose manifest is the trust authority: the
// branch origin/HEAD points to, else refs/remotes/origin/main, else
// refs/heads/main, else HEAD. The working tree is never the authority.
// The HEAD fallback is allowed only when the repository has no origin
// remote; with an origin but no resolvable default it is an error (fail
// closed), never a silent fall back to local state.
func DefaultBranchRef(repoRoot string) (string, error) {
	if out, err := runGit(repoRoot, "symbolic-ref", "-q", "refs/remotes/origin/HEAD"); err == nil {
		if ref := strings.TrimSpace(out); strings.HasPrefix(ref, "refs/remotes/origin/") {
			return ref, nil
		}
	}
	if _, err := runGit(repoRoot, "rev-parse", "--verify", "-q", "--end-of-options", "refs/remotes/origin/main"); err == nil {
		return "refs/remotes/origin/main", nil
	}
	if _, err := runGit(repoRoot, "remote", "get-url", "origin"); err == nil {
		return "", fmt.Errorf("snapshot: cannot determine origin default branch for %s: fetch it or run git remote set-head origin -a", repoRoot)
	}
	if _, err := runGit(repoRoot, "rev-parse", "--verify", "-q", "--end-of-options", "refs/heads/main"); err == nil {
		return "refs/heads/main", nil
	}
	return "HEAD", nil
}

// LoadVerifier reads publishers from the manifest at DefaultBranchRef.
// Only a manifest that is cleanly absent from a resolved default ref, or one
// without publishers, yields TrustAll. Every other failure is an error.
func LoadVerifier(repoRoot string) (*Verifier, error) {
	ref, err := DefaultBranchRef(repoRoot)
	if err != nil {
		return nil, err
	}
	if _, err := runGit(repoRoot, "rev-parse", "--verify", "-q", "--end-of-options", ref+"^{commit}"); err != nil {
		return nil, fmt.Errorf("snapshot: LoadVerifier: default branch %s does not resolve: %w", ref, err)
	}
	// ref resolves to a commit, so ls-tree succeeding with empty output means
	// the path is absent; any ls-tree failure is a real git error.
	listed, err := runGit(repoRoot, "ls-tree", "--name-only", ref, "--", ManifestPath)
	if err != nil {
		return nil, fmt.Errorf("snapshot: LoadVerifier: look up %s at %s: %w", ManifestPath, ref, err)
	}
	if strings.TrimSpace(listed) == "" {
		return &Verifier{Mode: TrustAll, Warnings: []string{
			fmt.Sprintf("no %s at %s: trusting every chronicle/graphs/* commit (configure publishers for a team)", ManifestPath, ref)}}, nil
	}
	data, err := gitdiff.Show(repoRoot, ref, ManifestPath)
	if err != nil {
		return nil, fmt.Errorf("snapshot: LoadVerifier: %w", err)
	}
	man, err := manifest.Load(data)
	if err != nil {
		return nil, fmt.Errorf("snapshot: LoadVerifier: manifest at %s: %w", ref, err)
	}
	if !man.HasPublishers() {
		return &Verifier{Mode: TrustAll, Warnings: []string{
			fmt.Sprintf("manifest at %s has no publishers: trusting every chronicle/graphs/* commit (configure publishers for a team)", ref)}}, nil
	}
	dir, err := os.MkdirTemp("", "chronicle-signers-")
	if err != nil {
		return nil, fmt.Errorf("snapshot: LoadVerifier: %w", err)
	}
	v := &Verifier{Mode: VerifySigned, Source: ref, dir: dir}
	v.allowedPath = filepath.Join(dir, "allowed_signers")
	if err := os.WriteFile(v.allowedPath, []byte(man.Publishers.AllowedSigners), 0o600); err != nil {
		v.Close()
		return nil, fmt.Errorf("snapshot: LoadVerifier: %w", err)
	}
	if strings.TrimSpace(man.Publishers.Revoked) != "" {
		v.revokedPath = filepath.Join(dir, "revoked")
		if err := os.WriteFile(v.revokedPath, []byte(man.Publishers.Revoked), 0o600); err != nil {
			v.Close()
			return nil, fmt.Errorf("snapshot: LoadVerifier: %w", err)
		}
	}
	return v, nil
}

// GitArgs returns the -c options that make %G? verify SSH signatures against
// the publishers list. nil in TrustAll (git never touches signatures).
func (v *Verifier) GitArgs() []string {
	if v == nil || v.Mode != VerifySigned {
		return nil
	}
	args := []string{"-c", "gpg.ssh.allowedSignersFile=" + v.allowedPath}
	if v.revokedPath != "" {
		args = append(args, "-c", "gpg.ssh.revocationFile="+v.revokedPath)
	}
	return args
}

// Trusted says whether p counts as a publication under this trust mode.
func (v *Verifier) Trusted(p Publication) bool {
	if v == nil || v.Mode == TrustAll {
		return true
	}
	return p.Signature == SigGood
}

// Close removes the temp files. Safe on nil and on TrustAll verifiers.
func (v *Verifier) Close() {
	if v != nil && v.dir != "" {
		_ = os.RemoveAll(v.dir)
		v.dir = ""
	}
}
