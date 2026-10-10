package llm

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type fakeBackend struct{ dir string }

// NewFake returns a backend that answers from fixture files in dir. The first
// line of Request.User must be "File: <path>" (or "Review: <commit>", file Review__<commit>.json); the reply is
// <dir>/<path with "/" replaced by "__">.json, else <dir>/default.json.
func NewFake(dir string) Backend { return fakeBackend{dir: dir} }

func (fakeBackend) Name() string  { return "fake" }
func (fakeBackend) Model() string { return "fake" }

func (f fakeBackend) Complete(ctx context.Context, req Request) (Response, error) {
	if err := ctx.Err(); err != nil {
		return Response{}, err
	}
	first, _, _ := strings.Cut(req.User, "\n")
	first = strings.TrimRight(first, "\r")
	path, ok := strings.CutPrefix(first, "File: ")
	if !ok {
		// Narration requests are keyed by the head commit: Review__<commit>.json.
		commit, isReview := strings.CutPrefix(first, "Review: ")
		if !isReview {
			return Response{}, fmt.Errorf("fake: first line of user prompt must be %q or %q, got %q", "File: <path>", "Review: <commit>", first)
		}
		path = "Review__" + commit
	}
	data, err := os.ReadFile(filepath.Join(f.dir, strings.ReplaceAll(path, "/", "__")+".json"))
	if os.IsNotExist(err) {
		data, err = os.ReadFile(filepath.Join(f.dir, "default.json"))
		if os.IsNotExist(err) {
			return Response{}, fmt.Errorf("fake: no fixture for %s", path)
		}
	}
	if err != nil {
		return Response{}, fmt.Errorf("fake: %w", err)
	}
	text := string(data)
	return Response{
		Text:  text,
		Model: "fake",
		Usage: Usage{InputTokens: (len(req.System) + len(req.User)) / 4, OutputTokens: len(text) / 4},
	}, nil
}
