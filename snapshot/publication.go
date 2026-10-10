// depbot/snapshot/publication.go
package snapshot

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Trailer keys of a publication commit (spec §4.1). The commit is only a
// publication envelope: its tree holds the snapshot files, its trailers
// say which code it describes and what its content identity is.
const (
	TrailerSnapshotID = "Chronicle-Snapshot-Id"
	TrailerCodeSha    = "Chronicle-Code-Sha"
	TrailerCodeRef    = "Chronicle-Code-Ref"
	TrailerBase       = "Chronicle-Base"
	TrailerSchema     = "Chronicle-Schema"
	TrailerProducer   = "Chronicle-Producer"
	TrailerForce      = "Chronicle-Force"
)

// Ref namespaces (spec §4.1). Chains carry the publication history of one
// long-lived code branch; heads carry one MR head each.
const (
	ChainRefPrefix       = "refs/heads/chronicle/graphs/"
	HeadRefPrefix        = "refs/heads/chronicle/heads/"
	RemoteChainRefPrefix = "refs/remotes/origin/chronicle/graphs/"
	RemoteHeadRefPrefix  = "refs/remotes/origin/chronicle/heads/"
)

// SignatureStatus is git's %G? folded to what trust decisions need.
type SignatureStatus string

const (
	SigGood      SignatureStatus = "good"      // G: valid signature from an allowed signer
	SigNone      SignatureStatus = "none"      // N: unsigned
	SigBad       SignatureStatus = "bad"       // B: signature does not verify
	SigUntrusted SignatureStatus = "untrusted" // U/X/Y/R/E: valid but not allowed, expired, revoked, or unverifiable
	SigUnchecked SignatureStatus = "unchecked" // %G? not requested (OSS trust-all mode)
)

// Publication is one commit on a chronicle/** ref read through its trailers.
type Publication struct {
	Commit     string
	Ref        string
	CodeSha    string
	SnapshotID string
	CodeRef    string
	Base       string
	Schema     string
	Force      bool
	Signature  SignatureStatus
	Distance   int // commits from the tip of Ref; 0 = tip
}

var (
	shaRe = regexp.MustCompile(`^[0-9a-f]{40}$|^[0-9a-f]{64}$`)
	idRe  = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// logFormat is the --format passed to git log. Fields are NUL-separated,
// records end with 0x1e. %G? triggers signature verification only when the
// caller configured gpg.ssh.allowedSignersFile (see listPublications).
const logFormat = "%H%x00%G?%x00" +
	"%(trailers:key=" + TrailerCodeSha + ",valueonly)%x00" +
	"%(trailers:key=" + TrailerSnapshotID + ",valueonly)%x00" +
	"%(trailers:key=" + TrailerCodeRef + ",valueonly)%x00" +
	"%(trailers:key=" + TrailerBase + ",valueonly)%x00" +
	"%(trailers:key=" + TrailerSchema + ",valueonly)%x00" +
	"%(trailers:key=" + TrailerForce + ",valueonly)%x1e"

const logFormatUnchecked = "%H%x00%x00" + // same fields, no %G?: never invoke signature machinery
	"%(trailers:key=" + TrailerCodeSha + ",valueonly)%x00" +
	"%(trailers:key=" + TrailerSnapshotID + ",valueonly)%x00" +
	"%(trailers:key=" + TrailerCodeRef + ",valueonly)%x00" +
	"%(trailers:key=" + TrailerBase + ",valueonly)%x00" +
	"%(trailers:key=" + TrailerSchema + ",valueonly)%x00" +
	"%(trailers:key=" + TrailerForce + ",valueonly)%x1e"

// parseLogRecords turns git log output in logFormat into publications, tip
// first. Commits without a well-formed Chronicle-Code-Sha (40/64 hex) or
// Chronicle-Snapshot-Id (64 hex) are skipped with one warning each; they
// still count toward Distance so positions stay comparable across runs.
func parseLogRecords(out, ref string) ([]Publication, []string) {
	var pubs []Publication
	var warns []string
	for i, raw := range strings.Split(out, "\x1e") {
		raw = strings.TrimLeft(raw, "\n")
		if strings.TrimSpace(raw) == "" {
			continue
		}
		f := strings.Split(raw, "\x00")
		if len(f) != 8 {
			warns = append(warns, fmt.Sprintf("%s: record %d: expected 8 fields, got %d", ref, i, len(f)))
			continue
		}
		multi := false
		for _, v := range f[2:] {
			if strings.Contains(strings.TrimSpace(v), "\n") {
				multi = true
			}
		}
		if multi {
			warns = append(warns, fmt.Sprintf("%s: %s: duplicated or multi-line trailer", ref, strings.TrimSpace(f[0])))
			continue
		}
		p := Publication{
			Commit: strings.TrimSpace(f[0]), Ref: ref,
			CodeSha: strings.TrimSpace(f[2]), SnapshotID: strings.TrimSpace(f[3]),
			CodeRef: strings.TrimSpace(f[4]), Base: strings.TrimSpace(f[5]),
			Schema:    strings.TrimSpace(f[6]),
			Force:     strings.EqualFold(strings.TrimSpace(f[7]), "true"),
			Signature: signatureFromCode(strings.TrimSpace(f[1])),
			Distance:  i,
		}
		if p.CodeSha == "" && p.SnapshotID == "" {
			warns = append(warns, fmt.Sprintf("%s: %s has no Chronicle trailers (not a publication)", ref, p.Commit))
			continue
		}
		if !shaRe.MatchString(p.CodeSha) {
			warns = append(warns, fmt.Sprintf("%s: %s: %s %q is not a 40/64-hex sha", ref, p.Commit, TrailerCodeSha, p.CodeSha))
			continue
		}
		if !idRe.MatchString(p.SnapshotID) {
			warns = append(warns, fmt.Sprintf("%s: %s: %s %q is not a 64-hex id", ref, p.Commit, TrailerSnapshotID, p.SnapshotID))
			continue
		}
		pubs = append(pubs, p)
	}
	return pubs, warns
}

func signatureFromCode(code string) SignatureStatus {
	switch code {
	case "G":
		return SigGood
	case "N":
		return SigNone
	case "B":
		return SigBad
	case "":
		return SigUnchecked
	default: // U X Y R E
		return SigUntrusted
	}
}

// ChainRefs lists every chronicle/graphs/* ref in the repository, remote
// twins first. A local ref whose remote-tracking twin exists is dropped:
// the remote is the published truth and the local copy may be stale.
func ChainRefs(repoRoot string) ([]string, error) {
	out, err := runGit(repoRoot, "for-each-ref", "--format=%(refname)", ChainRefPrefix, RemoteChainRefPrefix)
	if err != nil {
		return nil, fmt.Errorf("snapshot: ChainRefs: %w", err)
	}
	remote := map[string]bool{}
	var remotes, locals []string
	for _, r := range strings.Split(strings.TrimSpace(out), "\n") {
		switch {
		case r == "":
		case strings.HasPrefix(r, RemoteChainRefPrefix):
			remote[strings.TrimPrefix(r, RemoteChainRefPrefix)] = true
			remotes = append(remotes, r)
		case strings.HasPrefix(r, ChainRefPrefix):
			locals = append(locals, r)
		}
	}
	sort.Strings(remotes)
	sort.Strings(locals)
	refs := remotes
	for _, l := range locals {
		if !remote[strings.TrimPrefix(l, ChainRefPrefix)] {
			refs = append(refs, l)
		}
	}
	return refs, nil
}

// HeadRef returns the local and remote-tracking ref names of a head.
func HeadRef(codeSha string) (local, remote string) {
	return HeadRefPrefix + codeSha, RemoteHeadRefPrefix + codeSha
}

// listPublications reads one ref tip-first. gitArgs are prepended (-c
// gpg.ssh.allowedSignersFile=... from the Verifier); verify=false uses the
// format without %G? so git never touches signature machinery.
func listPublications(repoRoot, ref string, gitArgs []string, verify bool) ([]Publication, []string, error) {
	format := logFormatUnchecked
	if verify {
		format = logFormat
	}
	args := append(append([]string{}, gitArgs...), "log", "--format="+format, "--end-of-options", ref)
	out, err := runGit(repoRoot, args...)
	if err != nil {
		return nil, nil, fmt.Errorf("snapshot: listPublications %s: %w", ref, err)
	}
	pubs, warns := parseLogRecords(out, ref)
	return pubs, warns, nil
}
