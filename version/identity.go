package version

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"
)

// ReleaseCodename is a human-memorable release marker. Bump when scan/MCP contract changes.
// Agents compare this string — not semver alone — to detect stale MCP servers.
const ReleaseCodename = "osprey-vw1"

// SchemaGeneration bumps when MCP tool response shapes change incompatibly.
const SchemaGeneration = 4

// BuildTime is set at compile time via -ldflags (optional).
var BuildTime = ""

// Capabilities lists feature flags present in this MCP build. Sorted for stable fingerprint.
var Capabilities = []string{
	"artifact_pool",
	"builtin_kafka_pack",
	"checkout_items_path",
	"deterministic_candidates",
	"graph_hygiene",
	"mcp_identity_v1",
	"phase1_review",
	"pool_status_v2",
	"quality_warnings",
	"scan_review_gate",
	"view_algebra",
}

// MCPIdentity is the full identity payload for agents and operators.
type MCPIdentity struct {
	Banner           string            `json:"banner"`
	ReleaseCodename  string            `json:"release_codename"`
	SchemaGeneration int               `json:"schema_generation"`
	Fingerprint      string            `json:"fingerprint"`
	Version          string            `json:"version"`
	BuildHash        string            `json:"build_hash"`
	BuildTime        string            `json:"build_time,omitempty"`
	GoVersion        string            `json:"go_version,omitempty"`
	Capabilities     []string          `json:"capabilities"`
	ScanContract     map[string]bool   `json:"scan_contract"`
	Verify           string            `json:"how_to_verify"`
}

// Identity returns the current MCP server identity.
func Identity() MCPIdentity {
	caps := append([]string(nil), Capabilities...)
	sort.Strings(caps)

	fp := fingerprint(ReleaseCodename, SchemaGeneration, caps)
	banner := fmt.Sprintf("chronicle-mcp | %s | %s+%s | fp:%s",
		ReleaseCodename, Version, BuildHash, fp)

	return MCPIdentity{
		Banner:           banner,
		ReleaseCodename:  ReleaseCodename,
		SchemaGeneration: SchemaGeneration,
		Fingerprint:      fp,
		Version:          Version,
		BuildHash:        BuildHash,
		BuildTime:        BuildTime,
		Capabilities:     caps,
		ScanContract: map[string]bool{
			"pool_status_has_quality_warnings": hasCapability(caps, "pool_status_v2"),
			"checkout_returns_items_path":      hasCapability(caps, "checkout_items_path"),
			"resolve_has_hygiene_stats":        hasCapability(caps, "graph_hygiene"),
			"phase1_review":                    hasCapability(caps, "phase1_review"),
			"builtin_kafka_pack":               hasCapability(caps, "builtin_kafka_pack"),
			"mcp_identity_tool":                hasCapability(caps, "mcp_identity_v1"),
		},
		Verify: fmt.Sprintf(
			"Before scan: call chronicle_mcp_identity. Expect release_codename=%q fingerprint=%q. If mismatch, rebuild MCP (go install -ldflags \"-X github.com/alexdx2/chronicle-core/version.BuildHash=$(git rev-parse --short HEAD)\" ./cmd/chronicle/...) and restart the MCP server in Cursor.",
			ReleaseCodename, fp,
		),
	}
}

// IdentityMap returns identity as a map for MCP JSON responses.
func IdentityMap() map[string]any {
	id := Identity()
	return map[string]any{
		"banner":            id.Banner,
		"release_codename":  id.ReleaseCodename,
		"schema_generation": id.SchemaGeneration,
		"fingerprint":       id.Fingerprint,
		"version":           id.Version,
		"build_hash":        id.BuildHash,
		"build_time":        id.BuildTime,
		"capabilities":      id.Capabilities,
		"scan_contract":     id.ScanContract,
		"how_to_verify":     id.Verify,
	}
}

// ScanPreflightBlock is injected into chronicle_command(scan) instructions.
func ScanPreflightBlock() string {
	id := Identity()
	return fmt.Sprintf(`MCP PREFLIGHT (mandatory — before discovery):
  1. Call chronicle_mcp_identity (or chronicle_command command=version)
  2. Show the user the banner line verbatim
  3. Confirm release_codename=%q AND fingerprint=%q
  4. Confirm scan_contract keys are all true (especially phase1_review, graph_hygiene, checkout_returns_items_path)
  5. If ANY mismatch → STOP the scan. Tell user MCP is stale — rebuild chronicle MCP and restart Cursor, then retry.

`, id.ReleaseCodename, id.Fingerprint)
}

func fingerprint(codename string, schemaGen int, caps []string) string {
	h := sha256.New()
	h.Write([]byte(codename))
	h.Write([]byte("|"))
	h.Write([]byte(fmt.Sprintf("%d", schemaGen)))
	h.Write([]byte("|"))
	h.Write([]byte(strings.Join(caps, ",")))
	sum := hex.EncodeToString(h.Sum(nil))
	if len(sum) > 12 {
		return sum[:12]
	}
	return sum
}

func hasCapability(caps []string, name string) bool {
	for _, c := range caps {
		if c == name {
			return true
		}
	}
	return false
}

// StampBuildTime sets build time when not injected at compile time.
func StampBuildTime() {
	if BuildTime == "" {
		BuildTime = time.Now().UTC().Format(time.RFC3339)
	}
}
