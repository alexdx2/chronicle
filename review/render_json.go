package review

import "encoding/json"

// RenderJSON encodes r exactly as encoding/json would on its own — 2-space
// indent, keys in the struct fields' own declared order, no custom
// MarshalJSON and no extra key renaming. This is a deliberate choice, not an
// oversight: ReviewResult and everything it embeds (Provenance, Verdict,
// ReviewFinding, GraphDelta, ...) carry no `json:"..."` struct tags of their
// own today, so the emitted keys are the Go field names verbatim
// (SchemaVersion, Base, Head, ...) rather than the snake_case shape
// §8 of the review-engine design doc sketches. Adding tags to make the two
// match is out of this task's scope (those types belong to Tasks 1/9/11,
// none of them touched here) and the brief's own instruction for this
// function is explicit: "ReviewResult as is ... standard encoding/json" —
// see task-13-report.md for the full reasoning.
func RenderJSON(r *ReviewResult) ([]byte, error) {
	return json.MarshalIndent(r, "", "  ")
}
