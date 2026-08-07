package graph

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/alexdx2/chronicle-core/manifest"
	"github.com/alexdx2/chronicle-core/paths"
	"github.com/alexdx2/chronicle-core/registry"
	"github.com/alexdx2/chronicle-core/store"
)

// boundaryPriority returns a sort priority for boundary-first scan ordering.
// Lower = scanned first. Boundary files (package.json, Dockerfile, schema, main.ts)
// are scanned before regular source files so the agent discovers service boundaries
// and data models before processing controllers/providers.
func boundaryPriority(filePath string) int {
	base := filepath.Base(filePath)
	switch {
	case base == "package.json" || base == "go.mod" || base == "pyproject.toml" || base == "pom.xml" || base == "build.gradle":
		return 0 // App manifest — declares service boundary
	case base == "Dockerfile" || base == "docker-compose.yml" || base == "docker-compose.yaml":
		return 1 // Deployment — confirms service boundary
	case strings.HasSuffix(base, ".prisma") || strings.HasSuffix(base, ".graphql") || strings.HasSuffix(base, ".proto"):
		return 2 // Schema — defines data models and contracts
	case base == "main.ts" || base == "main.go" || base == "main.py" || base == "app.ts" || base == "index.ts":
		return 3 // Entry point — confirms app boundary
	case strings.Contains(filePath, "module") || strings.HasSuffix(base, ".module.ts"):
		return 4 // Module — structural containment
	default:
		return 5 // Regular source files
	}
}

// DiscoverResult holds files found during project discovery.
type DiscoverResult struct {
	Files        []string          `json:"files"`
	TotalFiles   int               `json:"total_files"`
	TotalGit     int               `json:"total_git_files"`     // all git-tracked files before filtering
	Excluded     int               `json:"excluded"`            // files excluded by patterns
	ByDirectory  map[string]int    `json:"by_directory"`        // file count per top-level directory
	ByExtension  map[string]int    `json:"by_extension"`        // file count per extension
	ScanConfig   map[string]any    `json:"scan_config"`
}

// ErrNoScanConfig is returned by DiscoverFilesOpts when the resolved scan
// config has neither include nor exclude patterns and the discovered file
// count exceeds the safety cap (200). SQ-Contract 4: this refusal used to be
// decided by the MCP handler AFTER DiscoverFilesOpts had already written
// obligations, infra nodes, service nodes, and evidence — an invalid/absent
// config left those writes orphaned (only chronicle_reset_db could clear
// them). The decision moves here, before any write, using the same
// ScanConfigFor(domainKey)-with-fallback config the discovery loop itself
// resolved — so CLI and MCP callers both get atomicity, and there is no
// second, disagreeing MergedScanConfig computed downstream.
type ErrNoScanConfig struct {
	TotalFiles int
}

func (e *ErrNoScanConfig) Error() string {
	return fmt.Sprintf("no scan config found and %d files discovered (limit 200) — call chronicle_save_manifest first to define scan.include/exclude patterns", e.TotalFiles)
}

// DiscoverOpts controls optional parameters for DiscoverFilesOpts.
type DiscoverOpts struct {
	// VotesNeeded is the number of independent extraction passes per file (default 1).
	VotesNeeded int
	// Scope is an optional list of glob patterns that further narrow discovered files.
	// Only files matching at least one Scope pattern (after manifest include/exclude) are returned.
	// Empty Scope means no additional filtering.
	Scope []string
}

// DiscoverFilesOpts finds all scannable files using git ls-files + manifest include/exclude rules,
// with optional scope filtering. Only git-tracked files are considered.
// If manifest is provided, uses the requested domain's scan config (merged view for unknown domains) and DomainForFile for per-file domain.
// Falls back to domainKey for all files when manifest is nil.
func (g *Graph) DiscoverFilesOpts(rootDir, domainKey string, revisionID int64, m *manifest.Manifest, opts DiscoverOpts) (*DiscoverResult, error) {
	// Get git-tracked files
	gitFiles, err := gitTrackedFiles(rootDir)
	if err != nil {
		return nil, err
	}

	// Build scan config from manifest (or nil for no filtering). A domain that
	// is declared in the manifest scopes discovery to ITS OWN include/exclude —
	// merging every domain's includes turns a single-domain scan into a
	// whole-workspace one. Unknown domains (no manifest entry) keep the merged
	// view for backward compatibility.
	var scanCfg *manifest.ScanConfig
	if m != nil {
		if cfg, ok := m.ScanConfigFor(domainKey); ok {
			scanCfg = cfg
		} else {
			merged := m.MergedScanConfig()
			scanCfg = &merged
		}
	}

	// Apply include/exclude filters from manifest
	var filtered []string
	for _, f := range gitFiles {
		if shouldInclude(f, scanCfg) {
			filtered = append(filtered, f)
		}
	}

	// Apply optional scope filter: intersect with scope globs
	if len(opts.Scope) > 0 {
		var scoped []string
		for _, f := range filtered {
			for _, pattern := range opts.Scope {
				if manifest.MatchGlob(f, pattern) {
					scoped = append(scoped, f)
					break
				}
			}
		}
		filtered = scoped
	}

	// Boundary-first ordering: package/build/deployment/schema files before source
	sort.SliceStable(filtered, func(i, j int) bool {
		return boundaryPriority(filtered[i]) < boundaryPriority(filtered[j])
	})

	// Build summary stats
	byDir := map[string]int{}
	byExt := map[string]int{}
	for _, f := range filtered {
		// Top-level directory (first path component)
		dir := f
		if i := strings.Index(f, "/"); i >= 0 {
			dir = f[:i]
		}
		byDir[dir]++

		ext := filepath.Ext(f)
		if ext == "" {
			ext = "(no ext)"
		}
		byExt[ext]++
	}

	result := &DiscoverResult{
		Files:       filtered,
		TotalFiles:  len(filtered),
		TotalGit:    len(gitFiles),
		Excluded:    len(gitFiles) - len(filtered),
		ByDirectory: byDir,
		ByExtension: byExt,
	}

	if scanCfg != nil {
		result.ScanConfig = map[string]any{
			"include": scanCfg.Include,
			"exclude": scanCfg.Exclude,
		}
	}

	// SQ-Contract 4: validate fully, then write once. This gate used to run
	// in the MCP handler AFTER this function had already written obligations,
	// infra, and service nodes — see ErrNoScanConfig's doc comment. It runs
	// here now, before any write, against the exact scanCfg the discovery
	// loop above already resolved.
	if scanCfg == nil || (len(scanCfg.Include) == 0 && len(scanCfg.Exclude) == 0) {
		if result.TotalFiles > 200 {
			return nil, &ErrNoScanConfig{TotalFiles: result.TotalFiles}
		}
	}

	// Resolve votes_needed (default 1)
	vn := opts.VotesNeeded
	if vn < 1 {
		vn = 1
	}

	if revisionID > 0 {
		if err := g.store.WithTx(func(tx *store.Store) error {
			g2 := *g
			g2.store = tx

			// Idempotent re-run: a second discover_files call for the same
			// revision replaces its scan_file obligations instead of
			// appending duplicates (scan_obligations has no UNIQUE
			// constraint, and the original insert errors were discarded, so
			// re-runs piled up rows silently). Scoped to obligation_type =
			// 'scan_file' only — verify_file (chronicle_invalidate_changed)
			// and trace_flow (phase-2 flow tracing) obligations live on the
			// same revision_id and must not be deleted by a re-discovery.
			if _, err := tx.DeleteScanFileObligationsForRevision(revisionID); err != nil {
				return fmt.Errorf("clear prior scan_file obligations for revision %d: %w", revisionID, err)
			}

			// Create scan_file obligations for each discovered file —
			// per-file domain from manifest. When votes_needed > 1, create N
			// obligations per file with vote metadata.
			for _, f := range filtered {
				domain := domainKey
				if m != nil {
					domain = m.DomainForFile(f)
				}
				if vn <= 1 {
					// votes=1: single obligation, no vote metadata (existing behavior)
					if _, err := tx.CreateObligation(revisionID, domain, "scan_file", f, "git-tracked, matches scan config"); err != nil {
						return fmt.Errorf("create obligation for %s: %w", f, err)
					}
				} else {
					// votes>1: create N obligations per file with vote metadata
					hash := sha256.Sum256([]byte(f))
					shortHash := hex.EncodeToString(hash[:6]) // 12 hex chars
					voteGroup := fmt.Sprintf("%d:%s:%s", revisionID, domain, shortHash)
					for i := 1; i <= vn; i++ {
						if _, err := tx.CreateObligationWithVote(revisionID, domain, "scan_file", f, "git-tracked, matches scan config", voteGroup, i); err != nil {
							return fmt.Errorf("create obligation (vote %d) for %s: %w", i, f, err)
						}
					}
				}
			}

			if m == nil {
				return nil
			}

			// Create infrastructure nodes from manifest. They belong to the
			// SCAN's domain — consulting m.Domains[0] used to stamp the
			// domain DISPLAY name ("Tom and Jerry") as domain_key when the
			// manifest key was absent.
			for _, infra := range m.Infrastructure {
				// SQ-Contract 3: one spelling per key. These raw store.UpsertNode
				// calls bypass ensureNodeID, so they must canonicalize here or the
				// manifest becomes a second writer with its own spelling. The
				// key's type segment and the node_type column must agree, so
				// both come from the same registryValidInfraType mapping.
				validType := registryValidInfraType(g.reg, infra.Type)
				keyEntry := infra
				keyEntry.Type = validType
				nodeKey := canonicalNodeKey(keyEntry.InfraNodeKey(domainKey))
				// Infra nodes must be born with the scan's revision on both
				// first/last-seen — left at the zero value, last_seen_revision_id
				// (0) is less than every real revision, so the FIRST
				// chronicle_stale_mark call after any scan marked every manifest
				// infra node stale forever, regardless of whether it was still
				// declared.
				if _, err := tx.UpsertNode(store.NodeRow{
					NodeKey:  nodeKey,
					Layer:    "infra",
					NodeType: validType,
					// QualifiedName carries the RAW address/name verbatim (pre
					// dash-folding) — ownHostsForDomain reads it instead of
					// reverse-parsing NodeKey, whose name segment is
					// canonicalized (port colon -> dash, dots/case folded by
					// validate.NormalizeNodeKey) and can no longer be turned
					// back into a bare hostname for isExternalHost matching.
					QualifiedName:       infra.AddressOrName(),
					DomainKey:           domainKey,
					Name:                infra.Name,
					Status:              "active",
					FirstSeenRevisionID: revisionID,
					LastSeenRevisionID:  revisionID,
				}); err != nil {
					return fmt.Errorf("upsert infra node %s: %w", nodeKey, err)
				}
				if err := g2.addCreationEvidence(nodeKey, revisionID, infra.Name,
					filepath.Join(paths.ConfiguredDir(), "chronicle.domain.yaml"), "chronicle:manifest", "manifest_key"); err != nil {
					return err
				}
			}

			// Create service nodes from manifest — ONLY explicitly declared ones.
			// Inferred services (from include-glob path segments) are not evidence:
			// real service nodes come from declares_service facts (package.json,
			// .csproj) during resolve.
			for _, svc := range m.Services {
				svcDomain := domainKey
				if len(m.Domains) > 0 && m.Domains[0].Key != "" {
					svcDomain = m.Domains[0].Key
				}
				// SQ-Contract 3: the resolver's calls_service/declares_service
				// lookups canonicalize their key (resolve_extractions.go), so a
				// manifest key spelled "tom.api" or "TomApi" must be stored under
				// the same canonical "tom-api" — otherwise the lookup misses and
				// resolve mints a twin for a service the manifest already declared.
				nodeKey := canonicalNodeKey("service:service:" + svcDomain + ":" + svc.Key)
				if _, err := tx.UpsertNode(store.NodeRow{
					NodeKey:            nodeKey,
					Layer:              "service",
					NodeType:           "service",
					DomainKey:          svcDomain,
					Name:               svc.Key,
					Status:             "active",
					LastSeenRevisionID: revisionID,
					Confidence:         1.0,
					Freshness:          1.0,
					TrustScore:         1.0,
					Metadata:           "{}",
				}); err != nil {
					return fmt.Errorf("upsert service node %s: %w", nodeKey, err)
				}
				if err := g2.addCreationEvidence(nodeKey, revisionID, svc.Key,
					filepath.Join(paths.ConfiguredDir(), "chronicle.domain.yaml"), "chronicle:manifest", "manifest_key"); err != nil {
					return err
				}
			}

			return nil
		}); err != nil {
			return nil, err
		}
	}

	return result, nil
}

// DiscoverFiles finds all scannable files using git ls-files + manifest include/exclude rules.
// Only git-tracked files are considered — no temp files, no untracked junk.
// If manifest is provided, uses MergedScanConfig for filtering and DomainForFile for per-file domain.
// Falls back to domainKey for all files when manifest is nil.
// Deprecated: prefer DiscoverFilesOpts for explicit options.
func (g *Graph) DiscoverFiles(rootDir, domainKey string, revisionID int64, m *manifest.Manifest, votesNeeded ...int) (*DiscoverResult, error) {
	vn := 1
	if len(votesNeeded) > 0 && votesNeeded[0] > 1 {
		vn = votesNeeded[0]
	}
	return g.DiscoverFilesOpts(rootDir, domainKey, revisionID, m, DiscoverOpts{VotesNeeded: vn})
}

// gitTrackedFiles returns all files tracked by git in the given directory.
func gitTrackedFiles(rootDir string) ([]string, error) {
	cmd := exec.Command("git", "ls-files")
	cmd.Dir = rootDir
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	var files []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line != "" {
			files = append(files, line)
		}
	}
	return files, nil
}

// alwaysExclude are patterns that are ALWAYS excluded regardless of manifest.
// These are directories that never contain scannable architecture.
var alwaysExclude = []string{
	"**/node_modules/**",
	"**/.history/**",
	"**/.git/**",
	"**/dist/**",
	"**/build/**",
	"**/@generated/**",
	"**/generated/**",
	"**/__pycache__/**",
	"**/.venv/**",
	"**/vendor/**",
	"**/*.min.js",
	"**/*.map",
	"**/*.lock",
	"**/migrations/**",
}

// shouldInclude checks if a file matches the manifest scan config.
// If no config → include everything (Claude decides per-file).
// If include patterns exist → file must match at least one.
// If exclude patterns exist → file must NOT match any.
// alwaysExclude patterns are applied regardless of config.
func shouldInclude(filePath string, cfg *manifest.ScanConfig) bool {
	// Always-exclude patterns (safety net)
	for _, pattern := range alwaysExclude {
		if manifest.MatchGlob(filePath, pattern) {
			return false
		}
	}

	if cfg == nil || (len(cfg.Include) == 0 && len(cfg.Exclude) == 0) {
		return true // no config = include all
	}

	// Check excludes first
	for _, pattern := range cfg.Exclude {
		if manifest.MatchGlob(filePath, pattern) {
			return false
		}
	}

	// If no includes specified, everything passes (after excludes)
	if len(cfg.Include) == 0 {
		return true
	}

	// Must match at least one include
	for _, pattern := range cfg.Include {
		if manifest.MatchGlob(filePath, pattern) {
			return true
		}
	}
	return false
}

// registryValidInfraType maps common manifest spellings onto registered infra
// node types; unknown types fall back to the generic "infrastructure" so a
// loose manifest never plants unregistered types in the graph.
func registryValidInfraType(reg *registry.Registry, t string) string {
	if reg.IsValidNodeType("infra", t) {
		return t
	}
	aliases := map[string]string{
		"message_broker": "broker",
		"message-broker": "broker",
		"kafka":          "broker",
		"redis":          "cache",
		"postgres":       "database",
		"postgresql":     "database",
		"mysql":          "database",
	}
	if mapped, ok := aliases[strings.ToLower(t)]; ok && reg.IsValidNodeType("infra", mapped) {
		return mapped
	}
	return "infrastructure"
}

// RegistryValidInfraType exposes registryValidInfraType to writers outside
// this package (mcpserver's save_manifest handler mints infra nodes of its
// own — the historical bug there was skipping this mapping entirely, storing
// the raw manifest type on node_type while discover.go stored the mapped
// one, so the same infra entry disagreed with itself depending on which
// writer touched it last).
func RegistryValidInfraType(reg *registry.Registry, t string) string {
	return registryValidInfraType(reg, t)
}
