package snapshot

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Match kinds a Lookup can answer with, and the step names of LookupConfig.Order.
const (
	MatchExact    = "exact"
	MatchAncestor = "ancestor"
	MatchTarget   = "target"
	MatchDefault  = "default"
)

var defaultOrder = []string{MatchExact, MatchAncestor, MatchTarget, MatchDefault}

// LookupQuery names the commit a snapshot is wanted for. TargetSha and
// TargetBranch describe where the commit is going (an MR's target), for the
// fallback when the commit's own history has no snapshot.
type LookupQuery struct{ Repo, Sha, TargetSha, TargetBranch string }

// LookupConfig tunes Lookup; the zero value means the defaults of WithDefaults.
type LookupConfig struct {
	Order         []string
	MaxAncestors  int
	DefaultBranch string
	FarDistance   int
}

// WithDefaults fills every unset field.
func (c LookupConfig) WithDefaults() LookupConfig {
	if len(c.Order) == 0 {
		c.Order = append([]string(nil), defaultOrder...)
	}
	if c.MaxAncestors <= 0 {
		c.MaxAncestors = 2000
	}
	if c.DefaultBranch == "" {
		c.DefaultBranch = "main"
	}
	if c.FarDistance <= 0 {
		c.FarDistance = 50
	}
	return c
}

// LookupAnswer is the snapshot Lookup chose. Distance is the number of
// commits between the query and the snapshot for exact (0) and ancestor
// matches, and -1 for target and default matches, which are not ancestors.
type LookupAnswer struct {
	SnapshotID string   `json:"snapshot_id"`
	CodeSha    string   `json:"code_sha"`
	Match      string   `json:"match"`
	Distance   int      `json:"distance"`
	Warnings   []string `json:"warnings,omitempty"`
}

// Far says whether the snapshot is too distant from the query to trust as a
// base: not an ancestor at all, or more than threshold commits away.
func (a LookupAnswer) Far(threshold int) bool { return a.Distance < 0 || a.Distance > threshold }

var branchNameRe = regexp.MustCompile(`^[A-Za-z0-9._/-]+$`)

func validBranch(b string) bool {
	return b != "" && branchNameRe.MatchString(b) && !strings.HasPrefix(b, "-") && !strings.Contains(b, "..") && !strings.HasSuffix(b, "/")
}

// Lookup finds the snapshot to use for q.Sha, trying cfg.Order in turn
// (spec §4.3): the snapshot of the sha itself, the nearest snapshotted
// ancestor, the target's snapshot, the default branch's snapshot. codeRepo is
// a clone of the code repository; "" skips the steps that need git history.
// When nothing is found the error wraps ErrNoSnapshot and the returned answer
// still carries Warnings.
func Lookup(ctx context.Context, st Store, codeRepo string, q LookupQuery, cfg LookupConfig) (LookupAnswer, error) {
	cfg = cfg.WithDefaults()
	if !ValidCodeSha(q.Sha) {
		return LookupAnswer{}, ErrBadSha
	}
	for _, step := range cfg.Order {
		switch step {
		case MatchExact, MatchAncestor, MatchTarget, MatchDefault:
		default:
			return LookupAnswer{}, fmt.Errorf("lookup: unknown step %q", step)
		}
	}
	entries, err := st.List(ctx, q.Repo)
	if err != nil {
		return LookupAnswer{}, err
	}
	if len(entries) == 0 {
		// Nothing to find: no step can succeed, and git (rev-list, rev-parse)
		// need not be run, nor the code repository exist.
		return LookupAnswer{}, fmt.Errorf("%w (repo %s, sha %s)", ErrNoSnapshot, q.Repo, q.Sha)
	}
	bySha := make(map[string]Entry, len(entries))
	for _, e := range entries {
		bySha[e.CodeSha] = e
	}
	var warns []string
	answer := func(e Entry, match string, dist int) LookupAnswer {
		return LookupAnswer{SnapshotID: e.SnapshotID, CodeSha: e.CodeSha, Match: match, Distance: dist, Warnings: warns}
	}
	for _, step := range cfg.Order {
		if err := ctx.Err(); err != nil {
			return LookupAnswer{}, err
		}
		switch step {
		case MatchExact:
			if e, ok := bySha[q.Sha]; ok {
				return answer(e, MatchExact, 0), nil
			}
		case MatchAncestor:
			shas, err := revList(codeRepo, q.Sha, cfg.MaxAncestors)
			if err != nil {
				warns = append(warns, "ancestor step skipped: "+err.Error())
				continue
			}
			for i, s := range shas {
				if i == 0 {
					continue // the sha itself is the exact step's business
				}
				if e, ok := bySha[s]; ok {
					return answer(e, MatchAncestor, i), nil
				}
			}
		case MatchTarget:
			if q.TargetSha != "" && ValidCodeSha(q.TargetSha) {
				if e, ok := bySha[q.TargetSha]; ok {
					return answer(e, MatchTarget, -1), nil
				}
			}
			if q.TargetBranch != "" {
				if !validBranch(q.TargetBranch) {
					warns = append(warns, fmt.Sprintf("target step: invalid branch %q", q.TargetBranch))
				} else if e, ok := branchSnapshot(codeRepo, entries, bySha, q.TargetBranch); ok {
					return answer(e, MatchTarget, -1), nil
				}
			}
		case MatchDefault:
			if e, ok := branchSnapshot(codeRepo, entries, bySha, cfg.DefaultBranch); ok {
				return answer(e, MatchDefault, -1), nil
			}
		}
	}
	return LookupAnswer{Warnings: warns}, fmt.Errorf("%w (repo %s, sha %s)", ErrNoSnapshot, q.Repo, q.Sha)
}

// branchSnapshot: the snapshot of the branch tip, else the newest snapshot on the branch's chain.
func branchSnapshot(codeRepo string, entries []Entry, bySha map[string]Entry, branch string) (Entry, bool) {
	if codeRepo != "" {
		for _, ref := range []string{"refs/remotes/origin/" + branch, "refs/heads/" + branch} {
			if out, err := runGit(codeRepo, "rev-parse", "--verify", "-q", "--end-of-options", ref+"^{commit}"); err == nil {
				if e, ok := bySha[strings.TrimSpace(out)]; ok {
					return e, true
				}
				break
			}
		}
	}
	var best Entry
	found := false
	for _, e := range entries {
		if e.OnChain(branch) && (!found || e.CreatedAt.After(best.CreatedAt) || (e.CreatedAt.Equal(best.CreatedAt) && e.CodeSha > best.CodeSha)) {
			best, found = e, true
		}
	}
	return best, found
}

func revList(codeRepo, sha string, n int) ([]string, error) {
	if codeRepo == "" {
		return nil, errors.New("no code repository to walk")
	}
	out, err := runGit(codeRepo, "rev-list", "--topo-order", "-n", strconv.Itoa(n), "--end-of-options", sha)
	if err != nil {
		return nil, fmt.Errorf("rev-list %s: %w", sha, err)
	}
	return strings.Fields(out), nil
}
