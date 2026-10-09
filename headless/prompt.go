package headless

import (
	"encoding/json"

	"github.com/alexdx2/chronicle-core/extract/deterministic"
	"github.com/alexdx2/chronicle-core/llm"
)

const outputContract = "\n\n## Output contract\nRespond with exactly one JSON object and nothing else: " +
	`{"status": one of extracted|no_runtime_architecture|config_only|type_only|generated|skipped, "from_type": string, "facts": [facts per the schema above]}` +
	". No markdown fences, no prose."

// BuildPrompt renders the single-turn extraction request for one file. The
// first line of User is "File: <path>" — the contract the fake backend keys on.
// It is exported so the plan output can show the exact prompt size.
func BuildPrompt(guide, path string, content []byte, candidates []deterministic.Candidate) llm.Request {
	cands := "none"
	if len(candidates) > 0 {
		if b, err := json.Marshal(candidates); err == nil {
			cands = string(b)
		}
	}
	return llm.Request{
		System: guide + outputContract,
		User: "File: " + path + "\n\nDeterministic candidates (merge the confident ones, drop the wrong ones):\n" +
			cands + "\n\n<<<FILE\n" + string(content) + "\nFILE>>>",
	}
}
