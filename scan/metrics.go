package scan

import (
	"time"

	"github.com/alexdx2/chronicle-core/headless"
)

// FileMetric is what one file cost and produced in one extraction run.
type FileMetric struct {
	Path       string    `json:"path"`
	Status     string    `json:"status"`
	Model      string    `json:"model"`
	DurationMS int64     `json:"duration_ms"`
	Bytes      int64     `json:"bytes"`
	Parts      int       `json:"parts"` // number of window parts; 1 = the file whole
	Facts      int       `json:"facts"`
	At         time.Time `json:"at"`
}

// MetricsFrom turns the per-file outcomes of a headless run into metrics, all
// stamped with the same observation time.
func MetricsFrom(model string, files []headless.FileUsage, at time.Time) []FileMetric {
	out := make([]FileMetric, 0, len(files))
	for _, f := range files {
		out = append(out, FileMetric{Path: f.Path, Status: f.Status, Model: model,
			DurationMS: f.DurationMS, Bytes: f.Bytes, Parts: f.Parts, Facts: f.Facts, At: at})
	}
	return out
}
