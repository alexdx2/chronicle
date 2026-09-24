package live

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"

	"github.com/alexdx2/chronicle-core/graph/viewmodel"
)

const (
	// MaxScreenBytes bounds a screen report body.
	MaxScreenBytes = 64 << 10
	maxTitle       = 120
	maxTab         = 32
	maxCrumbs      = 20
	maxKeyLen      = 512
	maxKeys        = 50
)

var clientIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// ParseScreen validates and cleans a screen report from the browser. The
// report is untrusted — it ends up in an agent's context — so unknown
// fields are dropped (the spec decodes into viewmodel.ViewSpec), strings are
// cleaned and bounded, and the spec's marks are normalized.
func ParseScreen(raw []byte) (Screen, error) {
	if len(raw) > MaxScreenBytes {
		return Screen{}, fmt.Errorf("screen report: larger than %d bytes", MaxScreenBytes)
	}
	var s Screen
	if err := json.NewDecoder(bytes.NewReader(raw)).Decode(&s); err != nil {
		return Screen{}, fmt.Errorf("screen report: %w", err)
	}
	if !clientIDPattern.MatchString(s.ClientID) {
		return Screen{}, fmt.Errorf("screen report: client_id must be 1-64 characters of [A-Za-z0-9_-]")
	}
	s.Tab = clip(viewmodel.CleanText(s.Tab), maxTab)
	s.Title = clip(viewmodel.CleanText(s.Title), maxTitle)
	if len(s.Breadcrumbs) > maxCrumbs {
		return Screen{}, fmt.Errorf("screen report: at most %d breadcrumbs", maxCrumbs)
	}
	for i, c := range s.Breadcrumbs {
		s.Breadcrumbs[i] = clip(viewmodel.CleanText(c), maxTitle)
	}
	if sel := s.Selected; sel != nil {
		if sel.Kind != "node" && sel.Kind != "edge" {
			return Screen{}, fmt.Errorf("screen report: selected.kind must be node or edge")
		}
		if len(sel.Keys) > maxKeys {
			return Screen{}, fmt.Errorf("screen report: at most %d selected keys", maxKeys)
		}
		for _, k := range append([]string{sel.Key}, sel.Keys...) {
			if len(k) > maxKeyLen {
				return Screen{}, fmt.Errorf("screen report: selected key longer than %d", maxKeyLen)
			}
		}
	}
	if s.Spec != nil {
		marks, err := viewmodel.NormalizeMarks(s.Spec.Marks)
		if err != nil {
			return Screen{}, fmt.Errorf("screen report: %w", err)
		}
		s.Spec.Marks = marks
	}
	return s, nil
}

// clip keeps the first n characters of s.
func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
