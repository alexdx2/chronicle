package scan

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/alexdx2/chronicle-core/deps"
	"github.com/alexdx2/chronicle-core/gitdiff"
	"github.com/alexdx2/chronicle-core/graph/prompts"
	"github.com/alexdx2/chronicle-core/llm"
	"github.com/alexdx2/chronicle-core/manifest"
	"github.com/alexdx2/chronicle-core/paths"
)

// ProfileOptions steer the profile layer of a snapshot build.
type ProfileOptions struct {
	// Backend asks the model to profile the repository. nil or Off: the
	// deterministic seed (a full scan) or the base's profile (an incremental one).
	Backend llm.Backend
	Off     bool
	// OffReason is the seed reason written when Off is set; "" = the default.
	OffReason string
	// Catalog supplies the packs; nil = built-in packs plus the project's.
	Catalog *prompts.Catalog
	// ForceReprofile asks the model on an incremental build although no
	// manifest or top-level directory changed.
	ForceReprofile bool
	// OnProfile is called once after the profile is known and before
	// extraction. The caller may author packs for technologies with Pack ==
	// "new" (changing p) and return the catalog to extract with (nil = keep).
	// Errors are recorded as a limitation, never fatal.
	OnProfile func(ctx context.Context, p *Profile) (*prompts.Catalog, error)
}

// profileStage is what the layers before the scan settled: inventory,
// dependencies, profile, catalog.
type profileStage struct {
	inv         *Inventory
	deps        deps.Report
	file        ProfileFile
	catalog     *prompts.Catalog
	limitations []string
	// writeManifest, when set, writes the profiled manifest (the tree has none
	// and the profile is fresh); nil leaves the base's manifest or the bootstrap.
	writeManifest func(manPath string) error
	// scopeFromBase: the base's manifest, not the profile, scopes this build
	// (finish copies that scope into profile.json, source "base").
	scopeFromBase bool
}

// buildProfileStage runs inventory, dependencies and the profile decision.
// treeHasManifest says the commit being built carries its own manifest, which
// then owns the scope (ScopeSource "repo").
//
// resume, when set, is the profile a restored build state saved: it is used
// as is (no model call, no OnProfile), so the restored manifest matches it.
func buildProfileStage(ctx context.Context, in BuildInput, wt, domainKey string, base *Base, treeHasManifest bool, resume *stateProfile) (*profileStage, error) {
	st := &profileStage{}
	opts := in.Profile

	st.catalog = opts.Catalog
	if st.catalog == nil {
		st.catalog = prompts.BuiltinCatalog()
		// A project pack that cannot be read costs that pack, not the build.
		if err := st.catalog.AddProjectPacks(filepath.Join(paths.DirAt(wt), "packs")); err != nil {
			st.limitations = append(st.limitations, "project packs: "+err.Error())
		}
	}
	inv, err := BuildInventory(wt, defaultMaxFileBytes)
	if err != nil {
		return nil, err
	}
	st.inv = inv
	if st.deps, err = deps.Scan(wt, inv.Manifests); err != nil {
		return nil, err
	}
	topDirs := SortedTopDirs(inv)

	var prev *ProfileFile
	manifestsChanged := false
	if base != nil {
		if len(base.Files.Profile) > 0 {
			if pf, perr := ParseProfileFile(base.Files.Profile); perr == nil {
				prev = &pf
			}
		}
		changed, cerr := gitdiff.ChangedFiles(wt, base.CodeSha, in.Sha)
		if cerr != nil {
			return nil, cerr
		}
		for _, c := range changed {
			if deps.IsManifest(c.Path) {
				manifestsChanged = true
				break
			}
		}
	}
	full := base == nil
	needModel := opts.Backend != nil && !opts.Off &&
		(full || prev == nil || opts.ForceReprofile || manifestsChanged || !sameStringSet(topDirs, prev.TopDirs))

	seed := Seed(wt, inv.Paths())
	pf := ProfileFile{Deps: st.deps, TopDirs: topDirs, ProfiledAt: in.Sha}
	fresh := true
	inheritPrev := func(pf *ProfileFile, p *ProfileFile) {
		pf.Profile, pf.ScopeSource, pf.ScopeProposed = p.Profile, p.ScopeSource, p.ScopeProposed
		pf.Seeded, pf.SeedReason, pf.ProfiledAt = p.Seeded, p.SeedReason, p.ProfiledAt
	}
	switch {
	case resume != nil:
		fresh = false
		inheritPrev(&pf, &resume.File)
		pf.Limitations = resume.File.Limitations
		st.limitations = append(st.limitations, resume.Limitations...)
	case needModel:
		window := 0
		if wb, ok := opts.Backend.(llm.WindowedBackend); ok {
			window = wb.ContextWindow()
		}
		req, dropped := BuildProfilePrompt(inv, st.deps, seed, st.catalog.Metas(), window)
		if dropped > 0 {
			note := fmt.Sprintf("profile prompt truncated: %d dependency lines dropped", dropped)
			pf.Limitations = append(pf.Limitations, note)
			st.limitations = append(st.limitations, note)
		}
		p, rerr := RequestProfileWith(ctx, opts.Backend, req, st.catalog.Known())
		if rerr != nil {
			if cerr := ctx.Err(); cerr != nil {
				return nil, cerr
			}
			if prev != nil && !full {
				// Nothing better than the base's profile (and its manifest).
				fresh = false
				inheritPrev(&pf, prev)
				st.limitations = append(st.limitations, "profile taken from the previous snapshot: "+rerr.Error())
				break
			}
			st.seedProfile(&pf, seed, rerr.Error())
			break
		}
		pf.Profile = p
		pf.ScopeSource = "model"
		if !treeHasManifest {
			scope, source, proposed, warn := GuardScope(p, seed, inv)
			pf.Profile.Scope, pf.ScopeSource, pf.ScopeProposed = scope, source, proposed
			if warn != "" {
				st.limitations = append(st.limitations, warn)
			}
		}
	case prev != nil && !full:
		fresh = false
		inheritPrev(&pf, prev)
		if pf.Seeded {
			st.limitations = append(st.limitations, "profile taken from the seed: "+pf.SeedReason)
		}
	default:
		reason := "no profile model configured"
		if opts.Off && opts.OffReason != "" {
			reason = opts.OffReason
		}
		st.seedProfile(&pf, seed, reason)
	}
	if treeHasManifest {
		pf.ScopeSource, pf.ScopeProposed = "repo", nil
	}

	if fresh && opts.OnProfile != nil {
		cat, herr := opts.OnProfile(ctx, &pf.Profile)
		if herr != nil {
			st.limitations = append(st.limitations, "pack authoring: "+herr.Error())
		}
		if cat != nil {
			st.catalog = cat
		}
	}

	// The manifest is written from the profile only when the tree has none and
	// the profile is new: the model answered, or a full scan (which has no
	// manifest to keep). An incremental build never replaces the base's
	// manifest by the seed.
	if fresh && !treeHasManifest && (full || !pf.Seeded) {
		cfg, tech := ScopeToScanConfig(pf.Profile.Scope), techIDs(pf.Profile)
		st.writeManifest = func(manPath string) error { return WriteProfiledManifest(manPath, domainKey, cfg, tech) }
	}
	// A fresh seed on an incremental build leaves the base's manifest in
	// charge: the seed's globs were never applied, so profile.json must not
	// claim them as the scope.
	if fresh && !treeHasManifest && st.writeManifest == nil && pf.ScopeSource == "seed" {
		st.scopeFromBase = true
		pf.ScopeSource = "base"
	}
	st.file = pf
	return st, nil
}

// stateProfile is what a build state keeps of the profile layer.
type stateProfile struct {
	File        ProfileFile `json:"file"`
	Limitations []string    `json:"limitations,omitempty"`
}

// stateProfileFile is the profile's file in the build state directory.
const stateProfileFile = "profile.json"

// readStateProfile returns the profile saved in a restored build state.
func readStateProfile(stateDir string) *stateProfile {
	raw, err := os.ReadFile(filepath.Join(stateDir, stateProfileFile))
	if err != nil {
		return nil
	}
	var sp stateProfile
	if json.Unmarshal(raw, &sp) != nil || len(sp.File.Profile.Technologies) == 0 {
		return nil
	}
	return &sp
}

// saveStateProfile writes the stage's profile into the state directory
// (atomically); checkpoint calls it before state.json is renamed in.
func (st *profileStage) saveStateProfile(stateDir string) error {
	raw, err := json.Marshal(stateProfile{File: st.file, Limitations: st.limitations})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return err
	}
	tmp := filepath.Join(stateDir, stateProfileFile+".tmp")
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	if err := renameFile(tmp, filepath.Join(stateDir, stateProfileFile)); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// seedProfile fills pf with the deterministic profile and says why.
func (st *profileStage) seedProfile(pf *ProfileFile, seed SeedResult, reason string) {
	pf.Profile = SeedProfile(seed, st.deps)
	pf.ScopeSource, pf.ScopeProposed = "seed", nil
	pf.Seeded, pf.SeedReason = true, reason
	st.limitations = append(st.limitations, "profile taken from the seed: "+reason)
}

// finish completes profile.json after the scan: the layer checks and the packs
// the build's manifest technologies resolved to. It returns the file's bytes.
func (st *profileStage) finish(manPath string, checks []LayerCheck) ([]byte, []string, error) {
	pf := &st.file
	pf.Checks = checks
	var tech []string
	if m, err := manifest.LoadFile(manPath); err == nil {
		tech = m.Tech
		if (pf.ScopeSource == "repo" || st.scopeFromBase) && len(m.Domains) > 0 {
			if cfg, ok := m.ScanConfigFor(m.Domains[0].Name); ok {
				pf.Profile.Scope = scopeOf(cfg)
			}
		}
	}
	pf.Packs = prompts.Refs(st.catalog.ForTech(tech))
	b, err := json.MarshalIndent(pf, "", "  ")
	return b, checkLimitations(checks), err
}

func scopeOf(c *manifest.ScanConfig) Scope {
	rules := func(globs []string) []ScopeRule {
		var out []ScopeRule
		for _, g := range globs {
			out = append(out, ScopeRule{Glob: g, Reason: "repository manifest"})
		}
		return out
	}
	return Scope{Include: rules(c.Include), Exclude: rules(c.Exclude), Tests: rules(c.Tests)}
}

// techIDs are the catalog packs the profile names, in technology order
// (the manifest's tech list selects packs by id).
func techIDs(p Profile) []string {
	var out []string
	seen := map[string]bool{}
	for _, t := range p.Technologies {
		if t.Pack != "" && t.Pack != "new" && !seen[t.Pack] {
			seen[t.Pack] = true
			out = append(out, t.Pack)
		}
	}
	return out
}

func sameStringSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	a, b = append([]string(nil), a...), append([]string(nil), b...)
	sort.Strings(a)
	sort.Strings(b)
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
