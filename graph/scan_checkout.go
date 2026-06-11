package graph

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/alexdx2/chronicle-core/extract/deterministic"
)

// Checkout wave sizing — orchestrator should use spawn_count * DefaultBatchSize, capped here.
const (
	DefaultBatchSize = 8
	MaxSpawnWorkers  = 5
	ServerMaxWave    = 40
)

// EffectiveCheckoutLimit caps requested batch size to protect MCP context.
func EffectiveCheckoutLimit(requested int) int {
	if requested <= 0 {
		requested = DefaultBatchSize
	}
	cap := MaxSpawnWorkers * DefaultBatchSize
	if cap > ServerMaxWave {
		cap = ServerMaxWave
	}
	if requested > cap {
		return cap
	}
	return requested
}

// CheckoutItem is one claimed obligation for artifact-pool extraction.
type CheckoutItem struct {
	FilePath                    string `json:"file_path"`
	ObligationID                int64  `json:"obligation_id"`
	VoteGroup                   string `json:"vote_group,omitempty"`
	VoteIndex                   int    `json:"vote_index,omitempty"`
	DeterministicCandidatesPath string `json:"deterministic_candidates_path,omitempty"`
	DeterministicCandidateCount int    `json:"deterministic_candidate_count,omitempty"`
}

// CheckoutBatch is the slim checkout response (shared paths, per-file items).
type CheckoutBatch struct {
	RevisionID       int64          `json:"revision_id"`
	DomainKey        string         `json:"domain_key"`
	OutboxDir        string         `json:"outbox_dir"`
	WorkDir          string         `json:"work_dir"`
	Items            []CheckoutItem `json:"items,omitempty"`
	ItemsPath        string         `json:"items_path,omitempty"`
	ItemCount        int            `json:"item_count"`
	RequestedLimit   int            `json:"requested_limit"`
	EffectiveLimit   int            `json:"effective_limit"`
	Truncated        bool           `json:"truncated"`
	RemainingAfter   int            `json:"remaining_after_checkout,omitempty"`
}

// ScanCheckoutBatches claims up to limit obligations and returns checkout batches for extractors.
func (g *Graph) ScanCheckoutBatches(revisionID int64, obligationType string, limit int) ([]CheckoutBatch, error) {
	requested := limit
	effective := EffectiveCheckoutLimit(limit)

	claimed, err := g.store.ClaimObligations(revisionID, obligationType, effective)
	if err != nil {
		return nil, fmt.Errorf("ScanCheckoutBatches: %w", err)
	}
	if len(claimed) == 0 {
		return nil, nil
	}

	run, err := g.store.GetScanRunByRevision(revisionID)
	if err != nil {
		return nil, fmt.Errorf("ScanCheckoutBatches: %w", err)
	}
	if run == nil {
		return nil, fmt.Errorf("scan run for revision %d not found", revisionID)
	}

	projectRoot := ProjectRoot()
	outboxDir := filepath.Join(projectRoot, ".depbot", "scan-outbox", fmt.Sprintf("%d", revisionID))
	workDir := filepath.Join(projectRoot, ".depbot", "scan-work", fmt.Sprintf("%d", revisionID))
	for _, dir := range []string{outboxDir, workDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("mkdir %s: %w", dir, err)
		}
	}

	waveDir := filepath.Join(workDir, fmt.Sprintf("wave-%d", time.Now().UnixNano()))
	if err := os.MkdirAll(waveDir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir wave: %w", err)
	}
	candidatesDir := filepath.Join(waveDir, "candidates")
	if err := os.MkdirAll(candidatesDir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir candidates: %w", err)
	}

	batch := CheckoutBatch{
		RevisionID:     revisionID,
		DomainKey:      claimed[0].DomainKey,
		OutboxDir:      outboxDir,
		WorkDir:        workDir,
		RequestedLimit: requested,
		EffectiveLimit: effective,
		Truncated:      requested > effective,
	}

	for _, c := range claimed {
		item := CheckoutItem{
			FilePath:     c.TargetKey,
			ObligationID: c.ObligationID,
			VoteGroup:    c.VoteGroup,
			VoteIndex:    c.VoteIndex,
		}

		fullPath := filepath.Join(projectRoot, c.TargetKey)
		if data, err := os.ReadFile(fullPath); err == nil {
			cands := deterministic.ExtractCandidates(c.TargetKey, data)
			if len(cands) > 0 {
				safe := strings.ReplaceAll(c.TargetKey, "/", "__")
				candPath := filepath.Join(candidatesDir, safe+".json")
				payload, _ := json.Marshal(map[string]any{
					"file_path":  c.TargetKey,
					"candidates": cands,
				})
				if err := os.WriteFile(candPath, payload, 0o644); err == nil {
					item.DeterministicCandidatesPath = candPath
					item.DeterministicCandidateCount = len(cands)
				}
			}
		}

		batch.Items = append(batch.Items, item)
	}
	batch.ItemCount = len(batch.Items)

	itemsPath := filepath.Join(waveDir, "items.json")
	if data, err := json.MarshalIndent(batch.Items, "", "  "); err == nil {
		_ = os.WriteFile(itemsPath, data, 0o644)
		batch.ItemsPath = itemsPath
	}

	remaining, _ := g.store.CountPendingObligations(revisionID, obligationType)
	batch.RemainingAfter = remaining

	return []CheckoutBatch{batch}, nil
}

// ProjectRoot returns the current working directory used for scan artifact paths.
func ProjectRoot() string {
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return wd
}

// OutboxArtifactPath returns the expected JSON path for a checked-out file.
func OutboxArtifactPath(outboxDir, filePath string) string {
	safe := strings.ReplaceAll(filePath, "/", "__")
	return filepath.Join(outboxDir, safe+".json")
}
