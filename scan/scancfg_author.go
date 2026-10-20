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
	Hits      int    `json:"golden_hits"`
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

Rules of the format: a call template is matched against comment-free source, whitespace-insensitive. {name} captures the channel name: a string literal, or an identifier that the scanner resolves through same-file constants and configuration keys. {any} skips an argument. A pattern with side define ("{id} = app.topic({name}") binds an identifier to a channel name for the whole repository, and {ref} in another pattern ("@app.agent({ref})", "{ref}.send(") looks that identifier up: use this when the producer or consumer is in another file than the declaration. side is produce, consume or declare; kind is topic, queue, stream, subject, exchange, subscription or bus. Document rules read json/yaml/properties files: {value} is the selected value, {key:N} the Nth segment of its dotted path. Example of a definition across files (Faust: the topic object is created in one module and used in another):
  - id: faust-topics
    when: {contains: ["app.topic", "app.agent"]}
    patterns:
      - {call: "{id} = app.topic({name}", side: define}
      - {call: "@app.agent({ref})", side: consume, kind: topic, transport: kafka}
      - {call: "{ref}.send(", side: produce, kind: topic, transport: kafka}
Do not write document rules over files you were not shown. Do not invent rules for code you were not shown. Prefer a few precise templates with an imports or contains gate over broad ones.`

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
	if isAnswerSheet(p) {
		return true
	}
	for _, s := range []string{"node_modules/", "vendor/", ".lock", "package-lock", ".min.", ".md", ".svg", ".map"} {
		if strings.Contains(l, s) {
			return true
		}
	}
	return false
}

var (
	provenanceLine = regexp.MustCompile(`(?m)^provenance:[^\n]*\n?`)
	versionLine    = regexp.MustCompile(`(?m)^version:[^\n]*\n`)
)

// sideMismatches lists channel names that appear in both lists with a
// different side: the rule found the channel but gave it the wrong role.
func sideMismatches(missing, extra []string) string {
	name := func(k string) string { return strings.SplitN(k, "#", 2)[0] }
	have := map[string]bool{}
	for _, k := range extra {
		have[name(k)] = true
	}
	var out []string
	for _, k := range missing {
		if have[name(k)] {
			out = append(out, name(k))
		}
	}
	return strings.Join(out, ", ")
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
		gold = fmt.Sprintf("\nThe rules do not find these channels yet (key = kind:name#side):\n%s\nThe rules find these that should not exist:\n%s\n",
			strings.Join(base.Golden.ConfigMissing, "\n"), strings.Join(base.Golden.ConfigExtra, "\n"))
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
			ar.Hits, ar.Missing, ar.Extra = dr.Golden.ConfigHits, len(dr.Golden.ConfigMissing), len(dr.Golden.ConfigExtra)
		}
		res.Rounds = append(res.Rounds, ar)
		if dr.Valid {
			score := len(dr.Facts)*2 - (ar.Missing+ar.Extra)*3
			// With an answer sheet a candidate must find more right than wrong
			// facts: rules whose facts are wrong would pollute the graph.
			acceptable := len(dr.Facts) > 0 && (dr.Golden == nil || ar.Hits > ar.Extra)
			if dr.Golden != nil {
				score = ar.Hits*3 - ar.Extra*3 - ar.Missing
			}
			if acceptable && score > bestScore {
				bestScore, best = score, cand
			}
			if len(dr.Facts) > 0 && (dr.Golden == nil || ar.Missing == 0 && ar.Extra == 0) {
				break
			}
			fb := fmt.Sprintf("The dry run found %d facts.", len(dr.Facts))
			if len(dr.Facts) == 0 {
				fb += " That is nothing: a when gate or a template does not match the lines shown above (a gate must hold in every file where the pattern should match, including files that do not import the library)."
			}
			if dr.Golden != nil {
				fb += fmt.Sprintf("\nStill missing: %s\nShould not exist: %s", strings.Join(dr.Golden.ConfigMissing, ", "), strings.Join(dr.Golden.ConfigExtra, ", "))
				if hint := sideMismatches(dr.Golden.ConfigMissing, dr.Golden.ConfigExtra); hint != "" {
					fb += "\nThe name is right but the side is wrong for: " + hint + " (produce = the code sends/writes it, consume = it reads it, declare = it creates or configures it)."
				}
			}
			user = "Your previous answer:\n" + cand + "\n" + fb + "\nFix it. Answer with the YAML only."
		} else {
			user = "Your previous answer:\n" + cand + "\nIt is invalid:\n" + dr.Errors + "\nFix it. Answer with the YAML only."
		}
	}
	if best == "" {
		res.Refused = "no candidate that finds anything in " + fmt.Sprint(len(res.Rounds)) + " rounds"
		return res, nil
	}
	// Stamp provenance. Whatever provenance the answer claims is replaced: a
	// model cannot present its rules as a human's.
	body := provenanceLine.ReplaceAllString(best, "")
	stamp := "provenance: llm:" + b.Model() + "\n"
	stamped := stamp + body
	if loc := versionLine.FindStringIndex(body); loc != nil {
		stamped = body[:loc[1]] + stamp + body[loc[1]:]
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
