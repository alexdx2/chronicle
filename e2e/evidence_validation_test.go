package e2e

import (
	"bufio"
	"os"
	"path/filepath"
	"testing"
)

func TestEvidencePointsToRealCode(t *testing.T) {
	payload := loadGoldenPayload(t)
	fixtureRoot := "../fixtures/orders-domain"

	for i, ev := range payload.Evidence {
		if ev.RepoName == "" || ev.FilePath == "" {
			continue // some evidence may not have file refs
		}

		// Build full path: fixtures/orders-domain/{repo_name}/{file_path}
		fullPath := filepath.Join(fixtureRoot, ev.RepoName, ev.FilePath)

		// Check file exists
		info, err := os.Stat(fullPath)
		if err != nil {
			t.Errorf("evidence[%d]: file %q does not exist (repo=%s, file=%s)", i, fullPath, ev.RepoName, ev.FilePath)
			continue
		}
		if info.IsDir() {
			t.Errorf("evidence[%d]: %q is a directory, not a file", i, fullPath)
			continue
		}

		// Count lines in file
		if ev.LineStart > 0 {
			lineCount, err := countLines(fullPath)
			if err != nil {
				t.Errorf("evidence[%d]: error reading %q: %v", i, fullPath, err)
				continue
			}
			if int(ev.LineStart) > lineCount {
				t.Errorf("evidence[%d]: line_start %d exceeds file line count %d in %q", i, int(ev.LineStart), lineCount, fullPath)
			}
			if int(ev.LineEnd) > 0 && int(ev.LineEnd) > lineCount {
				t.Errorf("evidence[%d]: line_end %d exceeds file line count %d in %q", i, int(ev.LineEnd), lineCount, fullPath)
			}
		}
	}

	t.Logf("Validated %d evidence entries against fixture source files", len(payload.Evidence))
}

func countLines(path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	count := 0
	for scanner.Scan() {
		count++
	}
	return count, scanner.Err()
}
