package scan

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/alexdx2/chronicle-core/llm"
	"github.com/alexdx2/chronicle-core/truth/scancfg"
)

// AuthorOptions bounds a model-written project layer.
type AuthorOptions struct {
	MaxRounds int // default 3
	Snippets  int // evidence snippets in the prompt, default 30
}

// AuthorRound is one iteration: the candidate and what the dry run said.
type AuthorRound struct {
	Candidate string `json:"candidate"`
	Valid     bool   `json:"valid"`
	Errors    string `json:"errors,omitempty"`
	Facts     int    `json:"facts"`
	Missing   int    `json:"golden_missing"`
	Extra     int    `json:"golden_extra"`
}

// AuthorResult is the outcome of ScanConfigAuthor.
type AuthorResult struct {
	Rounds  []AuthorRound         `json:"rounds"`
	Saved   bool                  `json:"saved"`
	Refused string                `json:"refused,omitempty"`
	Usage   llm.Usage             `json:"usage"`
	Final   *ScanConfigSaveResult `json:"final,omitempty"`
}

const authorSystem = `You write the scan configuration (.depbot/scan.yaml, format version 1) of one repository so a deterministic scanner finds message channels (topics, queues, streams, subjects, exchanges) that its built-in rules miss. Answer with the YAML file only, no prose, no code fences.

Format:
version: 1
channels:
  - id: lowercase-id
    when: {ext: [.py], imports: [libname], contains: [token], glob: ["svc/**"]}
    patterns:
      - {call: "client.send({name}", side: produce, kind: topic, transport: kafka}
    ignore_names: ["test-*"]
documents:
  - id: lowercase-id
    glob: "**/topics.yaml"
    select: "topics.*"
    emit:
      - {name: "{value}", side: declare, kind: topic, transport: kafka}
config:
  keys:
    - {match: "*_TOPIC", as: channel}
noise:
  - {name: "tmp-*"}

Rules of the format: a call template is matched against comment-free source, whitespace-insensitive. {name} captures the channel name: a string literal, or an identifier that the scanner resolves through same-file constants and configuration keys. {any} skips an argument. A pattern with side define ("{id} = app.topic({name}") binds an identifier to a channel name for the whole repository, and {ref} in another pattern ("@app.agent({ref})", "{ref}.send(") looks that identifier up: use this when the producer or consumer is in another file than the declaration. side is produce, consume or declare; kind is topic, queue, stream, subject, exchange, subscription or bus. Document rules read json/yaml/properties files: {value} is the selected value, {key:N} the Nth segment of its dotted path. Do not invent rules for code you were not shown. Prefer a few precise templates with an imports or contains gate over broad ones.`

var channelWordRe = regexp.MustCompile(`(?i)\b(publish|send|emit|produce|subscribe|consume|listen|topic|queue|stream|channel|exchange|subject|bus)\w*\b`)
var literalNameRe = regexp.MustCompile(`["']([A-Za-z][\w.\-/]{2,60})["']`)

// authorEvidence picks lines that look like channel use the configuration does
// not find yet: a channel-ish word and a name-shaped literal on one line.
func authorEvidence(root string, files []string, known map[string]bool, limit int) []string {
	var out []string
	for _, p := range files {
		if len(out) >= limit {
			break
		}
		if isLikelyNonSource(p) {
			continue
		}
		body, ok := readSmall(root, p)
		if !ok || looksBinary(body) {
			continue
		}
		lines := strings.Split(string(body), "\n")
		for i, ln := range lines {
			if len(out) >= limit {
				break
			}
			if !channelWordRe.MatchString(ln) {
				continue
			}
			m := literalNameRe.FindStringSubmatch(ln)
			if m == nil || known[strings.ToLower(m[1])] {
				continue
			}
			lo, hi := max(0, i-1), min(len(lines), i+2)
			out = append(out, fmt.Sprintf("%s:%d\n%s", p, i+1, strings.TrimRight(strings.Join(lines[lo:hi], "\n"), "\n")))
		}
	}
	return out
}

func isLikelyNonSource(p string) bool {
	l := strings.ToLower(p)
	for _, s := range []string{"node_modules/", "vendor/", ".lock", "package-lock", ".min.", ".md", ".svg", ".map"} {
		if strings.Contains(l, s) {
			return true
		}
	}
	return false
}

func stripFences(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		if i := strings.Index(s, "\n"); i >= 0 {
			s = s[i+1:]
		}
		s = strings.TrimSuffix(strings.TrimSpace(s), "```")
	}
	return strings.TrimSpace(s) + "\n"
}

// ScanConfigAuthor asks a model for a project layer, checks each answer with a
// dry run (against truth-expected.json when the repository has one), feeds the
// problems back, and saves the best valid candidate with provenance
// "llm:<model>". Its rules are unreviewed: they shape the scan but not the
// reference, until a human sets reviewed: true.
func ScanConfigAuthor(ctx context.Context, b llm.Backend, root string, opts AuthorOptions) (AuthorResult, error) {
	if opts.MaxRounds <= 0 {
		opts.MaxRounds = 3
	}
	if opts.Snippets <= 0 {
		opts.Snippets = 30
	}
	var res AuthorResult
	files := trackedFiles(root)
	current := "version: 1\n"
	if bts, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(ScanConfigFile))); err == nil {
		current = string(bts)
	}
	known := map[string]bool{}
	for _, f := range ScanConfigDryRun(root, current).Facts {
		if i := strings.Index(f.Key, ":"); i >= 0 {
			n := f.Key[i+1:]
			known[strings.ToLower(strings.SplitN(n, "#", 2)[0])] = true
		}
	}
	base := ScanConfigDryRun(root, current)
	ev := authorEvidence(root, files, known, opts.Snippets)
	var gold string
	if base.Golden != nil {
		gold = fmt.Sprintf("\nThe reference still misses these channels (key = kind:name#side):\n%s\nIt has these that should not exist:\n%s\n",
			strings.Join(base.Golden.Missing, "\n"), strings.Join(base.Golden.Extra, "\n"))
	}
	user := "Current project layer:\n" + current + "\nLines of this repository that look like channel use the configuration does not find yet:\n\n" +
		strings.Join(ev, "\n---\n") + gold + "\nWrite the improved scan.yaml."
	bestScore, best := -1<<30, ""
	for round := 0; round < opts.MaxRounds; round++ {
		r, err := b.Complete(ctx, llm.Request{System: authorSystem, User: user, MaxTokens: 4000, Text: true})
		if err != nil {
			return res, err
		}
		res.Usage = res.Usage.Add(r.Usage)
		cand := stripFences(r.Text)
		dr := ScanConfigDryRun(root, cand)
		ar := AuthorRound{Candidate: cand, Valid: dr.Valid, Errors: dr.Errors, Facts: len(dr.Facts)}
		if dr.Golden != nil {
			ar.Missing, ar.Extra = len(dr.Golden.Missing), len(dr.Golden.Extra)
		}
		res.Rounds = append(res.Rounds, ar)
		if dr.Valid {
			score := len(dr.Facts)*2 - (ar.Missing+ar.Extra)*3
			if score > bestScore {
				bestScore, best = score, cand
			}
			if dr.Golden != nil && ar.Missing == 0 && ar.Extra == 0 || dr.Golden == nil && len(dr.Facts) > 0 {
				break
			}
			fb := fmt.Sprintf("The dry run found %d facts.", len(dr.Facts))
			if dr.Golden != nil {
				fb += fmt.Sprintf("\nStill missing: %s\nShould not exist: %s", strings.Join(dr.Golden.Missing, ", "), strings.Join(dr.Golden.Extra, ", "))
			}
			user = "Your previous answer:\n" + cand + "\n" + fb + "\nFix it. Answer with the YAML only."
		} else {
			user = "Your previous answer:\n" + cand + "\nIt is invalid:\n" + dr.Errors + "\nFix it. Answer with the YAML only."
		}
	}
	if best == "" {
		res.Refused = "no valid candidate in " + fmt.Sprint(len(res.Rounds)) + " rounds"
		return res, nil
	}
	// stamp provenance; the candidate must still parse as written
	stamped := strings.Replace(best, "version: 1", "version: 1\nprovenance: llm:"+b.Model(), 1)
	if !strings.Contains(stamped, "provenance:") {
		stamped = "version: 1\nprovenance: llm:" + b.Model() + "\n" + strings.TrimPrefix(best, "version: 1\n")
	}
	if _, err := scancfg.Parse([]byte(stamped), scancfg.SourceProject); err != nil {
		res.Refused = "stamped candidate invalid: " + err.Error()
		return res, nil
	}
	sv, err := ScanConfigSave(root, stamped, false)
	res.Saved, res.Final = sv.Saved, &sv
	res.Refused = sv.Refused
	return res, err
}
