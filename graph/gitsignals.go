package graph

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/alexdx2/chronicle-core/store"
	"github.com/alexdx2/chronicle-core/validate"
)

// metadataMap parses a node/edge Metadata JSON object ("" and "{}" -> empty map).
func metadataMap(metadata string) (map[string]any, error) {
	root := map[string]any{}
	if metadata != "" && metadata != "{}" {
		if err := json.Unmarshal([]byte(metadata), &root); err != nil {
			return nil, err
		}
	}
	return root, nil
}

// marshalMetadata serializes a metadata map deterministically (sorted keys).
func marshalMetadata(root map[string]any) (string, error) {
	b, err := json.Marshal(root)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// Git-signal thresholds, adopted from change-coupling research (and matching
// codebase-memory-mcp's pass_githistory): commits touching more than
// couplingMaxFiles files are refactor/merge noise; a pair must co-change at
// least couplingMinCoChanges times and reach couplingMinScore to count.
const (
	churnWindowDays      = 180
	couplingMinCoChanges = 3
	couplingMaxFiles     = 20
	couplingMinScore     = 0.3
	changesWithEdgeType  = "CHANGES_WITH"
)

// commitFiles is one commit's trackable file list from git log.
type commitFiles struct {
	Timestamp int64
	Files     []string
}

// churnInfo is the per-file change frequency inside the window.
type churnInfo struct {
	Commits        int
	LastCommitUnix int64
}

// couplingPair is a pair of files that change together: an architectural
// dependency git reveals even when no import/call connects them.
type couplingPair struct {
	FileA, FileB string
	CoChanges    int
	Score        float64 // co_changes / min(total_a, total_b)
}

// parseGitLog parses `git log --pretty=format:%H|%ct --name-only` output into
// per-commit file lists. Malformed blocks are skipped, never fatal.
func parseGitLog(out string) []commitFiles {
	var commits []commitFiles
	var cur *commitFiles
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			cur = nil
			continue
		}
		if i := strings.IndexByte(line, '|'); i > 0 && cur == nil {
			ts, err := strconv.ParseInt(line[i+1:], 10, 64)
			if err != nil {
				continue
			}
			commits = append(commits, commitFiles{Timestamp: ts})
			cur = &commits[len(commits)-1]
			continue
		}
		if cur != nil {
			cur.Files = append(cur.Files, line)
		}
	}
	return commits
}

// computeChurn counts commits per file and tracks the latest commit timestamp.
func computeChurn(commits []commitFiles) map[string]churnInfo {
	out := map[string]churnInfo{}
	for _, c := range commits {
		for _, f := range c.Files {
			ci := out[f]
			ci.Commits++
			if c.Timestamp > ci.LastCommitUnix {
				ci.LastCommitUnix = c.Timestamp
			}
			out[f] = ci
		}
	}
	return out
}

// computeCoupling finds file pairs that change together. Score is
// co_changes / min(total_a, total_b); pairs below the co-change or score
// thresholds are dropped, and mega-commits are skipped as refactor noise.
// Deterministic: pairs are emitted sorted by (FileA, FileB), FileA < FileB.
func computeCoupling(commits []commitFiles) []couplingPair {
	fileTotals := map[string]int{}
	pairCounts := map[[2]string]int{}
	for _, c := range commits {
		if len(c.Files) > couplingMaxFiles {
			continue
		}
		for _, f := range c.Files {
			fileTotals[f]++
		}
		files := append([]string(nil), c.Files...)
		sort.Strings(files)
		for i := 0; i < len(files); i++ {
			for j := i + 1; j < len(files); j++ {
				if files[i] == files[j] {
					continue
				}
				pairCounts[[2]string{files[i], files[j]}]++
			}
		}
	}

	var out []couplingPair
	for pair, co := range pairCounts {
		if co < couplingMinCoChanges {
			continue
		}
		minTotal := fileTotals[pair[0]]
		if t := fileTotals[pair[1]]; t < minTotal {
			minTotal = t
		}
		if minTotal == 0 {
			continue
		}
		score := float64(co) / float64(minTotal)
		if score < couplingMinScore {
			continue
		}
		out = append(out, couplingPair{FileA: pair[0], FileB: pair[1], CoChanges: co, Score: score})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].FileA != out[j].FileA {
			return out[i].FileA < out[j].FileA
		}
		return out[i].FileB < out[j].FileB
	})
	return out
}

// ComputeGitSignals derives change-frequency signals from git history: per-node
// churn (commit count + last touch inside the window) and CHANGES_WITH edges
// between nodes whose files co-change (hidden coupling imports/calls don't
// show). The repo root is derived from the first file-backed node. Best-effort:
// a repo without git history is silently skipped, never fabricated.
//
// Churn counts are exact facts of the git log at scan time (confidence 1.0,
// source_kind git); coupling is a statistical signal, so its edges carry the
// coupling score as confidence and derivation_kind "inferred".
func (g *Graph) ComputeGitSignals(revisionID int64) error {
	nodes, err := g.store.ListNodes(store.NodeFilter{Layer: "code"})
	if err != nil {
		return fmt.Errorf("ComputeGitSignals nodes: %w", err)
	}

	// Index nodes by their file path (absolute paths reduced per repo root).
	fileNodes := map[string][]store.NodeRow{} // repo-relative path -> nodes
	var anyFile string
	for _, n := range nodes {
		if n.FilePath == "" {
			continue
		}
		if anyFile == "" {
			anyFile = n.FilePath
		}
		fileNodes[n.FilePath] = append(fileNodes[n.FilePath], n)
	}
	if anyFile == "" {
		return nil // nothing file-backed to attribute history to
	}

	root, log, err := gitLogSince(anyFile)
	if err != nil || log == "" {
		return nil // no git repo / history — skip, don't fabricate
	}
	commits := parseGitLog(log)
	if len(commits) == 0 {
		return nil
	}

	// nodeFor resolves a git-relative path to graph nodes: nodes may store
	// absolute paths or repo-relative paths.
	nodeFor := func(rel string) []store.NodeRow {
		if ns, ok := fileNodes[rel]; ok {
			return ns
		}
		return fileNodes[filepath.Join(root, rel)]
	}

	churn := computeChurn(commits)
	rels := make([]string, 0, len(churn))
	for rel := range churn {
		rels = append(rels, rel)
	}
	sort.Strings(rels)
	for _, rel := range rels {
		ci := churn[rel]
		for _, n := range nodeFor(rel) {
			merged, err := mergeChurn(n.Metadata, ci)
			if err != nil {
				return fmt.Errorf("ComputeGitSignals merge %s: %w", n.NodeKey, err)
			}
			if err := g.store.UpdateNodeMetadata(n.NodeID, merged); err != nil {
				return fmt.Errorf("ComputeGitSignals metadata %s: %w", n.NodeKey, err)
			}
			assertion := fmt.Sprintf(`{"commits":%d,"last_commit_unix":%d,"window_days":%d}`,
				ci.Commits, ci.LastCommitUnix, churnWindowDays)
			if _, err := g.AddNodeEvidence(n.NodeKey, validate.EvidenceInput{
				SourceKind:       "git",
				ExtractorID:      "churn-git",
				ExtractorVersion: "1",
				ASTRule:          "gitsignals/v1",
				Confidence:       1.0,
				RevisionID:       revisionID,
				Assertion:        assertion,
				AssertionKind:    "churn",
				Metadata:         `{"metric_type":"exact"}`,
			}); err != nil {
				return fmt.Errorf("ComputeGitSignals evidence %s: %w", n.NodeKey, err)
			}
		}
	}

	for _, p := range computeCoupling(commits) {
		nas, nbs := nodeFor(p.FileA), nodeFor(p.FileB)
		if len(nas) == 0 || len(nbs) == 0 {
			continue // history mentions files the graph doesn't track
		}
		na, nb := nas[0], nbs[0]
		if na.NodeKey == nb.NodeKey {
			continue
		}
		edgeMeta := fmt.Sprintf(`{"co_changes":%d,"coupling_score":%.4f,"window_days":%d}`,
			p.CoChanges, p.Score, churnWindowDays)
		if _, err := g.UpsertEdge(validate.EdgeInput{
			FromNodeKey: na.NodeKey, ToNodeKey: nb.NodeKey,
			EdgeType: changesWithEdgeType, DerivationKind: "inferred",
			FromLayer: "code", ToLayer: "code",
			Confidence: p.Score, Metadata: edgeMeta,
		}, revisionID); err != nil {
			return fmt.Errorf("ComputeGitSignals edge %s<->%s: %w", na.NodeKey, nb.NodeKey, err)
		}
		edgeKey := na.NodeKey + "->" + nb.NodeKey + ":" + changesWithEdgeType
		assertion := fmt.Sprintf(`{"co_changes":%d,"coupling_score":%.4f,"window_days":%d}`,
			p.CoChanges, p.Score, churnWindowDays)
		if _, err := g.AddEdgeEvidence(edgeKey, validate.EvidenceInput{
			SourceKind:       "git",
			ExtractorID:      "coupling-git",
			ExtractorVersion: "1",
			ASTRule:          "gitsignals/v1",
			Confidence:       p.Score,
			RevisionID:       revisionID,
			Assertion:        assertion,
			AssertionKind:    "change_coupling",
			Metadata:         `{"metric_type":"heuristic"}`,
		}); err != nil {
			return fmt.Errorf("ComputeGitSignals edge evidence: %w", err)
		}
	}
	return nil
}

// gitLogSince returns the repo root for the directory containing anchorFile and
// the raw `git log` output limited to the churn window.
func gitLogSince(anchorFile string) (root, out string, err error) {
	dir := filepath.Dir(anchorFile)
	rootB, err := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", "", err
	}
	root = strings.TrimSpace(string(rootB))
	logB, err := exec.Command("git", "-C", root, "log",
		fmt.Sprintf("--since=%d days ago", churnWindowDays),
		"--pretty=format:%H|%ct", "--name-only").Output()
	if err != nil {
		return root, "", err
	}
	return root, string(logB), nil
}

// churnFromMetadata extracts the churn commit count from node Metadata.
// Returns 0 when the node carries no churn block.
func churnFromMetadata(metadata string) int {
	if metadata == "" {
		return 0
	}
	var wrap struct {
		Churn *struct {
			Commits int `json:"commits"`
		} `json:"churn"`
	}
	if err := json.Unmarshal([]byte(metadata), &wrap); err != nil || wrap.Churn == nil {
		return 0
	}
	return wrap.Churn.Commits
}

// mergeChurn folds churn info into a node's Metadata JSON under the "churn" key,
// preserving all other keys. Deterministic output.
func mergeChurn(metadata string, ci churnInfo) (string, error) {
	root, err := metadataMap(metadata)
	if err != nil {
		return "", err
	}
	root["churn"] = map[string]any{
		"commits":          ci.Commits,
		"last_commit_unix": ci.LastCommitUnix,
		"window_days":      churnWindowDays,
	}
	return marshalMetadata(root)
}
