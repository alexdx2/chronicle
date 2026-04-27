package e2e

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLIWorkflow(t *testing.T) {
	// Find the chronicle binary — build it if needed
	binaryPath := findOrBuildBinary(t)
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Helper to run chronicle commands
	run := func(args ...string) string {
		fullArgs := append([]string{"--db", dbPath}, args...)
		cmd := exec.Command(binaryPath, fullArgs...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("chronicle %s failed: %v\nOutput: %s", strings.Join(args, " "), err, string(out))
		}
		return string(out)
	}

	// Helper to run and parse JSON
	runJSON := func(args ...string) map[string]any {
		out := run(args...)
		// Take first line of JSON (ignore stderr)
		lines := strings.Split(strings.TrimSpace(out), "\n")
		var result map[string]any
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "{") {
				if err := json.Unmarshal([]byte(line), &result); err == nil {
					return result
				}
			}
		}
		// Try parsing the full output
		if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &result); err != nil {
			t.Fatalf("failed to parse JSON from: %s", out)
		}
		return result
	}

	// 1. Version
	versionOut := run("version")
	if !strings.Contains(versionOut, "v0.1.0") {
		t.Errorf("version = %q, want contains v0.1.0", versionOut)
	}

	// 2. Init (creates DB via --db flag, which auto-migrates)
	// The DB is created automatically on first command that opens it

	// 3. Create revision
	revOut := runJSON("revision", "create", "--domain", "orders", "--after-sha", "abc123", "--trigger", "manual", "--mode", "full")
	revID, ok := revOut["revision_id"].(float64)
	if !ok || revID <= 0 {
		t.Fatalf("revision_id = %v, want positive number", revOut["revision_id"])
	}

	// 4. Import golden graph
	goldenPath, _ := filepath.Abs("../fixtures/orders-domain/expected-graph.json")
	importOut := runJSON("import", "all", "--file", goldenPath, "--revision", "1")
	nodesCreated, _ := importOut["nodes_created"].(float64)
	edgesCreated, _ := importOut["edges_created"].(float64)
	if nodesCreated < 15 {
		t.Errorf("nodes_created = %v, want >= 15", nodesCreated)
	}
	if edgesCreated < 12 {
		t.Errorf("edges_created = %v, want >= 12", edgesCreated)
	}

	// 5. List nodes
	nodesOut := run("node", "list", "--layer", "code", "--domain", "orders")
	var nodes []map[string]any
	json.Unmarshal([]byte(nodesOut), &nodes)
	if len(nodes) < 5 {
		t.Errorf("code nodes = %d, want >= 5", len(nodes))
	}

	// 6. Query deps
	depsOut := run("query", "deps", "code:controller:orders:orderscontroller", "--depth", "1")
	var deps []map[string]any
	json.Unmarshal([]byte(depsOut), &deps)
	if len(deps) < 1 {
		t.Errorf("deps = %d, want >= 1", len(deps))
	}

	// 7. Query path
	pathOut := run("query", "path", "code:controller:orders:orderscontroller", "service:service:orders:payments-api", "--mode", "directed")
	var pathResult map[string]any
	json.Unmarshal([]byte(pathOut), &pathResult)
	paths, _ := pathResult["paths"].([]any)
	if len(paths) == 0 {
		t.Error("expected at least 1 path from controller to payments-api")
	}

	// 8. Impact
	impactOut := run("impact", "code:provider:orders:paymentsservice", "--depth", "3")
	var impactResult map[string]any
	json.Unmarshal([]byte(impactOut), &impactResult)
	impacts, _ := impactResult["impacts"].([]any)
	if len(impacts) < 2 {
		t.Errorf("impacts = %d, want >= 2", len(impacts))
	}

	// 9. Stats
	statsOut := run("query", "stats", "--domain", "orders")
	var stats map[string]any
	json.Unmarshal([]byte(statsOut), &stats)
	nodeCount, _ := stats["node_count"].(float64)
	if nodeCount < 15 {
		t.Errorf("node_count = %v, want >= 15", nodeCount)
	}

	// 10. Snapshot
	snapOut := runJSON("snapshot", "create", "--revision", "1", "--domain", "orders", "--node-count", "18", "--edge-count", "17")
	snapID, _ := snapOut["snapshot_id"].(float64)
	if snapID <= 0 {
		t.Errorf("snapshot_id = %v, want positive", snapID)
	}

	t.Log("Full CLI workflow passed!")
}

func findOrBuildBinary(t *testing.T) string {
	t.Helper()

	// Try to find existing binary
	projectRoot, _ := filepath.Abs("..")
	binaryPath := filepath.Join(projectRoot, "chronicle")

	if _, err := os.Stat(binaryPath); err == nil {
		return binaryPath
	}

	// Build it
	cmd := exec.Command("go", "build", "-o", binaryPath, "./cmd/chronicle")
	cmd.Dir = projectRoot
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("failed to build chronicle: %v\n%s", err, out)
	}

	return binaryPath
}
