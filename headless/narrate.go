package headless

import (
	"fmt"
	"strings"

	"github.com/alexdx2/chronicle-core/llm"
	"github.com/alexdx2/chronicle-core/review"
)

// narrateUserCap bounds the user text of a narration request, in characters.
const narrateUserCap = 24000

// BuildNarratePrompt renders the deterministic review r as a compact model
// input. changed is the changed-file list (nil falls back to r.ChangedFiles).
// The first line of User is "Review: <head commit>" — the key the fake backend
// looks fixtures up by. The result is capped at 24 000 characters by
// truncating the findings list.
func BuildNarratePrompt(r *review.ReviewResult, changed []string, maxWords int) llm.Request {
	if maxWords <= 0 {
		maxWords = 120
	}
	system := fmt.Sprintf("You read a deterministic architecture review of a merge request and write a short paragraph for the reviewer. "+
		"Facts are given; do not invent relationships, systems or files that are not in the input. "+
		"Say what the change means for the system, what may be affected and why, and what to test. "+
		"At most %d words, plain prose, no headings, no lists, no markdown.", maxWords)

	if changed == nil {
		for _, f := range r.ChangedFiles {
			changed = append(changed, f.Path)
		}
	}

	var head strings.Builder
	fmt.Fprintf(&head, "Review: %s\n", r.Head.Commit)
	v := r.Verdict
	fmt.Fprintf(&head, "Verdict: systems affected %d, repositories %d, integrations added %d, dependencies removed %d, contracts changed %d, unknown files %d, no system change %t\n",
		v.SystemsAffected, v.Repositories, v.IntegrationsAdded, v.DependenciesRemoved, v.ContractsChanged, v.UnknownFiles, v.NoSystemChange)
	for _, w := range r.Warnings {
		fmt.Fprintf(&head, "Warning: %s\n", w)
	}

	var tail strings.Builder
	if v.Learned > 0 {
		fmt.Fprintf(&tail, "\n%d facts were learned from unchanged neighbour files; they are not part of this change\n", v.Learned)
	}
	if len(changed) > 0 {
		tail.WriteString("\nChanged files:\n")
		for _, p := range changed {
			tail.WriteString(p + "\n")
		}
	}

	var lines []string
	line := func(f review.ReviewFinding) string {
		sys := f.System.Name
		if sys == "" {
			sys = f.System.Key
		}
		if f.System.Repo != "" {
			sys += " (" + f.System.Repo + ")"
		}
		return fmt.Sprintf("%s | %s | %s | %s | %s", f.Severity, f.Kind, sys, f.Reason, findingPath(f))
	}
	for _, f := range r.Findings {
		if f.Severity == review.SeverityInformational && f.Reason == review.ReasonLearnedOutsideDiff {
			continue
		}
		lines = append(lines, line(f))
		for _, d := range f.Downstream {
			lines = append(lines, line(d))
		}
	}
	var rels []string
	for _, f := range r.ChangedRelationships() {
		rels = append(rels, fmt.Sprintf("%s | %s | %s", f.Kind, f.System.Key, f.Counterpart))
	}

	budget := narrateUserCap - head.Len() - tail.Len()
	var body strings.Builder
	if len(rels) > 0 {
		body.WriteString("\nChanged relationships (kind | system | counterpart):\n")
		for _, l := range rels {
			// Relationships are priority content but still bounded.
			if body.Len()+len(l)+1 > budget/3 {
				body.WriteString("… and more relationships\n")
				break
			}
			body.WriteString(l + "\n")
		}
	}
	if len(lines) > 0 {
		body.WriteString("\nFindings (severity | kind | system (repo) | reason | path):\n")
		const reserve = 40
		for i, l := range lines {
			if body.Len()+len(l)+1+reserve > budget {
				fmt.Fprintf(&body, "… and %d more\n", len(lines)-i)
				break
			}
			body.WriteString(l + "\n")
		}
	}
	user := head.String() + body.String() + tail.String()
	if len(user) > narrateUserCap {
		// The changed-file list alone can be huge; cut it, never the head.
		user = user[:narrateUserCap-20]
		if i := strings.LastIndexByte(user, '\n'); i > 0 {
			user = user[:i+1]
		}
		user += "… and more\n"
	}
	return llm.Request{System: system, User: user, MaxTokens: 1024}
}

func findingPath(f review.ReviewFinding) string {
	if f.File != "" {
		return f.File
	}
	for _, e := range f.Evidence {
		if e.File != "" {
			return e.File
		}
	}
	if n := len(f.Path); n > 0 {
		return f.Path[n-1].Node
	}
	return "-"
}
