package graph

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/alexdx2/chronicle-core/store"
	"github.com/alexdx2/chronicle-core/validate"
)

// testLinkConfidence: test linkage by naming convention is a strong but
// heuristic claim — the file exists (exact) but "it tests this unit" is
// interpretation, so it never reaches the 1.0 reserved for exact facts.
const testLinkConfidence = 0.7

// isTestFilePath reports whether a path is itself a test file, by the common
// cross-language conventions (specs, __tests__/, Go _test.go, pytest test_*).
func isTestFilePath(path string) bool {
	base := filepath.Base(path)
	dir := filepath.ToSlash(filepath.Dir(path))
	if strings.HasSuffix(dir, "/__tests__") || strings.Contains(dir, "/__tests__/") ||
		strings.HasSuffix(dir, "/tests") || strings.Contains(dir, "/tests/") ||
		dir == "tests" || strings.HasPrefix(dir, "tests/") {
		return true
	}
	name := base
	if i := strings.LastIndexByte(name, '.'); i > 0 {
		name = name[:i]
	}
	return strings.HasSuffix(name, ".spec") || strings.HasSuffix(name, ".test") ||
		strings.HasSuffix(name, "_test") || strings.HasSuffix(name, "_spec") ||
		strings.HasPrefix(name, "test_")
}

// findTestFile returns the path of a test file covering src by naming
// convention, or "" when none exists. Checked locations, in order:
// sibling <name>.spec.<ext> / <name>.test.<ext>, __tests__/<name>.(spec|test).<ext>,
// __tests__/<name>.<ext>, and Go's sibling <name>_test.go.
func findTestFile(src string) string {
	dir := filepath.Dir(src)
	base := filepath.Base(src)
	ext := filepath.Ext(base)
	name := strings.TrimSuffix(base, ext)

	var candidates []string
	if ext == ".go" {
		candidates = append(candidates, filepath.Join(dir, name+"_test.go"))
	} else {
		candidates = append(candidates,
			filepath.Join(dir, name+".spec"+ext),
			filepath.Join(dir, name+".test"+ext),
			filepath.Join(dir, "__tests__", name+".spec"+ext),
			filepath.Join(dir, "__tests__", name+".test"+ext),
			filepath.Join(dir, "__tests__", name+ext),
		)
	}
	for _, c := range candidates {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return c
		}
	}
	return ""
}

// ComputeTestSignals records, for every non-test code node backed by a source
// file, whether a test file covers it by naming convention. The looked-but-
// absent case is recorded too (has_test_file:false) so insights can honestly
// say "untested" only for nodes the pass actually examined. One heuristic
// evidence row (test-link) per node; test files themselves are skipped.
func (g *Graph) ComputeTestSignals(revisionID int64) error {
	return g.computeTestSignals(revisionID, nil)
}

// computeTestSignals is the scoped worker: a non-nil scope limits filesystem
// probing to the changed set on incremental finalizes.
func (g *Graph) computeTestSignals(revisionID int64, scope fileScope) error {
	nodes, err := g.store.ListNodes(store.NodeFilter{Layer: "code"})
	if err != nil {
		return fmt.Errorf("ComputeTestSignals nodes: %w", err)
	}
	for _, n := range nodes {
		if n.FilePath == "" || isTestFilePath(n.FilePath) || !scope.matches(n.FilePath) {
			continue
		}
		if _, err := os.Stat(n.FilePath); err != nil {
			continue // source not reachable from here — don't claim anything
		}
		testFile := findTestFile(n.FilePath)
		merged, err := mergeTestSignal(n.Metadata, testFile)
		if err != nil {
			return fmt.Errorf("ComputeTestSignals merge %s: %w", n.NodeKey, err)
		}
		if err := g.store.UpdateNodeMetadata(n.NodeID, merged); err != nil {
			return fmt.Errorf("ComputeTestSignals metadata %s: %w", n.NodeKey, err)
		}
		assertion, _ := json.Marshal(map[string]any{
			"has_test_file": testFile != "",
			"test_file":     testFile,
		})
		if _, err := g.AddNodeEvidence(n.NodeKey, validate.EvidenceInput{
			SourceKind:       "file",
			ExtractorID:      "test-link",
			ExtractorVersion: "1",
			ASTRule:          "testsignals/v1",
			Confidence:       testLinkConfidence,
			RevisionID:       revisionID,
			Assertion:        string(assertion),
			AssertionKind:    "test_link",
			Metadata:         `{"metric_type":"heuristic"}`,
		}); err != nil {
			return fmt.Errorf("ComputeTestSignals evidence %s: %w", n.NodeKey, err)
		}
	}
	return nil
}

// mergeTestSignal folds the test-linkage result into node Metadata under the
// "tests" key, preserving other keys.
func mergeTestSignal(metadata, testFile string) (string, error) {
	root, err := metadataMap(metadata)
	if err != nil {
		return "", err
	}
	root["tests"] = map[string]any{
		"has_test_file": testFile != "",
		"test_file":     testFile,
	}
	return marshalMetadata(root)
}

// testSignalFromMetadata reports (looked, tested): looked=false means the test
// pass never examined this node, so no untested claim may be made.
func testSignalFromMetadata(metadata string) (looked, tested bool) {
	if metadata == "" {
		return false, false
	}
	var wrap struct {
		Tests *struct {
			HasTestFile bool `json:"has_test_file"`
		} `json:"tests"`
	}
	if err := json.Unmarshal([]byte(metadata), &wrap); err != nil || wrap.Tests == nil {
		return false, false
	}
	return true, wrap.Tests.HasTestFile
}
