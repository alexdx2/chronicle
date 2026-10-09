package headless

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/alexdx2/chronicle-core/extract/deterministic"
	"github.com/alexdx2/chronicle-core/gitutil"
	"github.com/alexdx2/chronicle-core/graph"
	"github.com/alexdx2/chronicle-core/llm"
	"github.com/alexdx2/chronicle-core/manifest"
	"github.com/alexdx2/chronicle-core/mcpserver"
	"github.com/alexdx2/chronicle-core/store"
)

// RunInput drives one headless extraction over a file list.
//
// Run reads sources and writes the outbox through the scan pipeline, which
// resolves paths via graph.ProjectRoot(): the caller must have set the project
// root (paths.SetProjectRoot) to RepoRoot. Run verifies the outbox lands under
// ChronicleDir and fails otherwise. mcpserver.CommitScanOutbox does not record
// file coverage (only the MCP file_extracted paths do), so Run does it itself.
type RunInput struct {
	G            *graph.Graph
	RepoRoot     string
	ChronicleDir string // <RepoRoot>/.depbot (outbox lives under it)
	Domain       string
	Manifest     *manifest.Manifest
	HeadSHA      string
	Files        []string
	Backend      llm.Backend
	Concurrency  int // 0 -> 4
	MaxTokens    int // 0 -> 8192
}

// FileUsage is the per-file outcome and token accounting.
type FileUsage struct {
	Path       string    `json:"path"`
	Status     string    `json:"status"` // artifact status, or "provider_error"
	Usage      llm.Usage `json:"usage"`
	DurationMS int64     `json:"duration_ms"`
	Error      string    `json:"error,omitempty"`
}

// RunResult summarizes a headless run.
type RunResult struct {
	RevisionID  int64                           `json:"revision_id"`
	Model       string                          `json:"model"`
	Files       []FileUsage                     `json:"files"`
	Totals      llm.Usage                       `json:"totals"`
	CostUSD     float64                         `json:"cost_usd"`
	CostKnown   bool                            `json:"cost_known"`
	Extracted   int                             `json:"extracted"`
	Failed      int                             `json:"failed"`
	Commit      *mcpserver.OutboxCommitResult   `json:"commit"`
	Resolve     *graph.ResolveExtractionsResult `json:"resolve"`
	Finalize    *graph.FinalizeResult           `json:"finalize"`
	Limitations []string                        `json:"limitations"`
}

var validArtifactStatus = map[string]bool{
	"extracted": true, "no_runtime_architecture": true, "config_only": true,
	"type_only": true, "generated": true, "skipped": true,
}

// parsedAnswer is a validated model answer.
type parsedAnswer struct {
	Status   string          `json:"status"`
	FromType string          `json:"from_type"`
	Facts    json.RawMessage `json:"facts"`
}

// parseAnswer extracts and validates the model's JSON answer. A top-level
// array is wrapped as {"status":"extracted","facts":<array>}.
func parseAnswer(text string) (*parsedAnswer, error) {
	raw, err := llm.ExtractJSON(text)
	if err != nil {
		return nil, err
	}
	var a parsedAnswer
	if len(raw) > 0 && raw[0] == '[' {
		a = parsedAnswer{Status: "extracted", Facts: raw}
	} else if err := json.Unmarshal(raw, &a); err != nil {
		return nil, fmt.Errorf("answer is not a JSON object: %w", err)
	}
	if !validArtifactStatus[a.Status] {
		return nil, fmt.Errorf("invalid status %q", a.Status)
	}
	if len(a.Facts) == 0 || string(a.Facts) == "null" {
		if a.Status == "extracted" {
			return nil, fmt.Errorf("status extracted but facts missing")
		}
		a.Facts = json.RawMessage("[]")
	}
	if a.Facts[0] != '[' {
		return nil, fmt.Errorf("facts must be a JSON array")
	}
	return &a, nil
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// Run extracts Files with the LLM backend through the scan pipeline: claim a
// revision, discover, check out obligations, write outbox artifacts, commit,
// resolve and finalize.
func Run(ctx context.Context, in RunInput) (*RunResult, error) {
	if in.G == nil || in.Backend == nil || in.Manifest == nil {
		return nil, fmt.Errorf("headless: Run needs a graph, a backend and a manifest")
	}
	conc := in.Concurrency
	if conc <= 0 {
		conc = 4
	}
	maxTokens := in.MaxTokens
	if maxTokens <= 0 {
		maxTokens = llm.DefaultMaxTokens
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	g, st := in.G, in.G.Store()
	model := in.Backend.Name() + ":" + in.Backend.Model()

	// trigger_kind is CHECK-constrained; "ci_pipeline" is the closest allowed
	// value and the metadata kind carries the real origin.
	rev, _, err := st.ClaimRevision(store.RevisionClaim{
		DomainKey: in.Domain, AfterSHA: in.HeadSHA, TriggerKind: "ci_pipeline", Mode: "incremental",
		Metadata: fmt.Sprintf(`{"kind":"headless","model":%q}`, model),
	})
	if err != nil {
		return nil, fmt.Errorf("headless: claim revision: %w", err)
	}
	runID, err := st.CreateScanRun(rev, in.Domain, 1)
	if err != nil {
		return nil, fmt.Errorf("headless: create scan run: %w", err)
	}
	disc, err := g.DiscoverFilesOpts(in.RepoRoot, in.Domain, rev, in.Manifest, graph.DiscoverOpts{VotesNeeded: 1, Scope: in.Files})
	if err != nil {
		return nil, fmt.Errorf("headless: discover: %w", err)
	}
	if err := st.TransitionScanRun(runID, "phase1_extract", len(disc.Files)); err != nil {
		return nil, fmt.Errorf("headless: scan run: %w", err)
	}

	res := &RunResult{RevisionID: rev, Model: model, Commit: &mcpserver.OutboxCommitResult{}}
	discovered := map[string]bool{}
	for _, f := range disc.Files {
		discovered[f] = true
	}
	for _, f := range in.Files {
		if !discovered[f] {
			res.Files = append(res.Files, FileUsage{Path: f, Status: "skipped", Error: "not discoverable"})
		}
	}

	guide := g.ExtractionGuide(in.Manifest.Tech...)
	wantOutbox := filepath.Join(in.ChronicleDir, "scan-outbox", fmt.Sprintf("%d", rev))
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		batches, err := g.ScanCheckoutBatches(rev, "scan_file", 1000)
		if err != nil {
			return nil, fmt.Errorf("headless: checkout: %w", err)
		}
		if len(batches) == 0 {
			break
		}
		for _, b := range batches {
			if filepath.Clean(b.OutboxDir) != filepath.Clean(wantOutbox) {
				return nil, fmt.Errorf("headless: scan outbox %s is not under %s (set the project root to the repo root)", b.OutboxDir, wantOutbox)
			}
			items := b.Items
			if len(items) == 0 && b.ItemsPath != "" {
				data, err := os.ReadFile(b.ItemsPath)
				if err != nil {
					return nil, fmt.Errorf("headless: read checkout items: %w", err)
				}
				if err := json.Unmarshal(data, &items); err != nil {
					return nil, fmt.Errorf("headless: decode checkout items: %w", err)
				}
			}
			files, err := runWave(ctx, in, b, items, guide, conc, maxTokens)
			res.Files = append(res.Files, files...)
			if err != nil {
				return nil, err
			}
			cr, err := mcpserver.CommitScanOutbox(g, in.ChronicleDir, in.Domain, rev, true)
			if err != nil {
				return nil, fmt.Errorf("headless: commit outbox: %w", err)
			}
			res.Commit.Imported += cr.Imported
			res.Commit.RowsWritten += cr.RowsWritten
			res.Commit.Deduped += cr.Deduped
			res.Commit.Errors += cr.Errors
			res.Commit.Dir = cr.Dir
			res.Commit.ErrorMsgs = append(res.Commit.ErrorMsgs, cr.ErrorMsgs...)
		}
	}

	var includeDev bool
	if cfg, ok := in.Manifest.ScanConfigFor(in.Domain); ok && cfg != nil {
		includeDev = cfg.IncludeDevDeps
	}
	// Record what was observed (with the blob SHA of the file on disk) so
	// freshness sees the new blob. Same extractor identity as the MCP paths.
	for _, f := range res.Files {
		if f.Status == "failed" || f.Status == "provider_error" || (f.Status == "skipped" && f.Error != "") {
			continue
		}
		blob, _ := gitutil.FileBlobSHA(in.RepoRoot, f.Path)
		if err := st.UpsertCoverage(store.CoverageRow{
			DomainKey: in.Domain, FilePath: f.Path, ExtractorID: "chronicle-scan",
			ExtractorVersion: fmt.Sprintf("fact-schema@%d", store.FactSchemaVersion),
			Status:           f.Status, BlobSHA: blob,
		}); err != nil {
			return nil, fmt.Errorf("headless: record coverage: %w", err)
		}
	}
	if res.Resolve, err = g.ResolveExtractionsWithOptions(in.Domain, rev, graph.ResolveOptions{AllowDegraded: true, IncludeDevDeps: includeDev}); err != nil {
		return nil, fmt.Errorf("headless: resolve: %w", err)
	}
	if err := st.CompleteScanRun(runID); err != nil {
		return nil, fmt.Errorf("headless: complete scan run: %w", err)
	}
	if res.Finalize, err = g.FinalizeIncrementalScan(in.Domain, rev); err != nil {
		return nil, fmt.Errorf("headless: finalize: %w", err)
	}

	sort.Slice(res.Files, func(i, j int) bool { return res.Files[i].Path < res.Files[j].Path })
	for _, f := range res.Files {
		res.Totals = res.Totals.Add(f.Usage)
		switch f.Status {
		case "extracted":
			res.Extracted++
		case "failed", "provider_error":
			res.Failed++
		}
	}
	res.CostUSD, res.CostKnown = llm.Cost(in.Backend.Model(), res.Totals)
	res.Limitations = []string{"reconcile_endpoints phase not run", "trace_flow phase not run"}
	return res, nil
}

// runWave extracts one checkout batch with a bounded worker pool and writes
// one outbox artifact per item.
func runWave(ctx context.Context, in RunInput, b graph.CheckoutBatch, items []graph.CheckoutItem, guide string, conc, maxTokens int) ([]FileUsage, error) {
	out := make([]FileUsage, len(items))
	errs := make([]error, len(items))
	jobs := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < conc; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				out[i], errs[i] = extractItem(ctx, in, b, items[i], guide, maxTokens)
			}
		}()
	}
	for i := range items {
		if ctx.Err() != nil {
			break
		}
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	for i, err := range errs {
		if err != nil {
			return nil, fmt.Errorf("headless: %s: %w", items[i].FilePath, err)
		}
	}
	return out, nil
}

// extractItem asks the model about one file and writes the artifact. A
// non-nil error means the artifact could not be written (infrastructure);
// model and provider failures become failed artifacts instead.
func extractItem(ctx context.Context, in RunInput, b graph.CheckoutBatch, item graph.CheckoutItem, guide string, maxTokens int) (FileUsage, error) {
	start := time.Now()
	fu := FileUsage{Path: item.FilePath}
	art := map[string]any{
		"file_path": item.FilePath, "obligation_id": item.ObligationID,
		"vote_group": item.VoteGroup, "vote_index": item.VoteIndex,
		"revision_id": b.RevisionID, "domain": b.DomainKey,
	}
	fail := func(status, msg string) {
		fu.Status = status
		fu.Error = msg
		art["status"] = "failed"
		art["error_message"] = msg
	}

	content, err := os.ReadFile(filepath.Join(in.RepoRoot, item.FilePath))
	if err != nil {
		fail("failed", "read: "+err.Error())
	} else {
		var cands []deterministic.Candidate
		if item.DeterministicCandidatesPath != "" {
			if data, err := os.ReadFile(item.DeterministicCandidatesPath); err == nil {
				var payload struct {
					Candidates []deterministic.Candidate `json:"candidates"`
				}
				if json.Unmarshal(data, &payload) == nil {
					cands = payload.Candidates
				}
			}
		}
		req := BuildPrompt(guide, item.FilePath, content, cands)
		req.MaxTokens = maxTokens
		firstUser := req.User
		var parsed *parsedAnswer
		var lastText string
		for attempt := 0; attempt < 2 && parsed == nil && fu.Status == ""; attempt++ {
			resp, err := in.Backend.Complete(ctx, req)
			fu.Usage = fu.Usage.Add(resp.Usage)
			if err != nil {
				fail("provider_error", err.Error())
				break
			}
			lastText = resp.Text
			a, perr := parseAnswer(resp.Text)
			if perr == nil {
				parsed = a
				break
			}
			req.User = firstUser + "\n\nYour previous answer was not the required JSON object (" + perr.Error() + "). Answer again with only the JSON object."
			if attempt == 1 {
				fail("failed", truncate(lastText, 2000))
			}
		}
		if parsed != nil {
			fu.Status = parsed.Status
			art["status"] = parsed.Status
			art["from_type"] = parsed.FromType
			art["facts"] = parsed.Facts
		}
	}
	fu.DurationMS = time.Since(start).Milliseconds()

	data, err := json.Marshal(art)
	if err != nil {
		return fu, err
	}
	return fu, writeAtomic(graph.OutboxArtifactPath(b.OutboxDir, item.FilePath), data)
}

// writeAtomic writes data to path via a temp file and rename so the commit
// never reads a half-written artifact. The temp name does not end in .json.
func writeAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}
