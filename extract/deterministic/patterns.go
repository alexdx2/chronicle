// Package deterministic extracts architecture candidates via regex/heuristics
// before LLM extraction. Candidates are hints — agents may accept or refine them.
package deterministic

import (
	"regexp"
	"strings"
)

// Candidate is a pre-extracted fact hint attached to a checkout item.
type Candidate struct {
	Kind     string `json:"kind"`
	To       string `json:"to,omitempty"`
	Method   string `json:"method,omitempty"`
	Target   string `json:"target,omitempty"`
	FromType string `json:"from_type,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

var (
	reHttpAttr    = regexp.MustCompile(`\[(HttpGet|HttpPost|HttpPut|HttpPatch|HttpDelete)(?:\("([^"]*)"\))?\]`)
	reRouteAttr   = regexp.MustCompile(`\[Route\("([^"]+)"\)\]`)
	reMapHub      = regexp.MustCompile(`MapHub<(\w+)>\("([^"]+)"\)`)
	reMapGet      = regexp.MustCompile(`Map(Get|Post|Put|Patch|Delete)\("([^"]+)"`)
	reProduce     = regexp.MustCompile(`ProduceAsync\("([^"]+)"`)
	reSubscribe   = regexp.MustCompile(`Subscribe\("([^"]+)"`)
	reEventPattern = regexp.MustCompile(`@EventPattern\('([^']+)'\)`)
	reDeclaresSvc = regexp.MustCompile(`<AssemblyName>([^<]+)</AssemblyName>`)
	reAddScoped   = regexp.MustCompile(`Add(Scoped|Singleton|Transient)<([^>]+)>`)
)

// ExtractCandidates returns regex-derived fact hints for a source file.
func ExtractCandidates(filePath string, content []byte) []Candidate {
	text := string(content)
	lower := strings.ToLower(filePath)
	var out []Candidate

	if strings.HasSuffix(lower, ".csproj") {
		if m := reDeclaresSvc.FindStringSubmatch(text); len(m) > 1 {
			out = append(out, Candidate{Kind: "declares_service", To: m[1], Reason: "AssemblyName in csproj"})
		} else {
			base := filePath
			if i := strings.LastIndex(base, "/"); i >= 0 {
				base = base[i+1:]
			}
			base = strings.TrimSuffix(base, ".csproj")
			if base != "" {
				out = append(out, Candidate{Kind: "declares_service", To: base, Reason: "csproj filename"})
			}
		}
		return out
	}

	if !strings.HasSuffix(lower, ".cs") && !strings.HasSuffix(lower, ".ts") {
		return out
	}

	// NestJS module
	if strings.Contains(text, "@Module") {
		out = append(out, Candidate{Kind: "provides", FromType: "module", Reason: "@Module decorator"})
	}

	// Kafka / messaging
	for _, m := range reProduce.FindAllStringSubmatch(text, -1) {
		out = append(out, Candidate{Kind: "produces", To: m[1], Method: "ProduceAsync", Reason: "Confluent.Kafka ProduceAsync"})
	}
	for _, m := range reSubscribe.FindAllStringSubmatch(text, -1) {
		out = append(out, Candidate{Kind: "consumes", To: m[1], Method: "Subscribe", Reason: "Confluent.Kafka Subscribe"})
	}
	for _, m := range reEventPattern.FindAllStringSubmatch(text, -1) {
		out = append(out, Candidate{Kind: "consumes", To: m[1], Reason: "@EventPattern"})
	}

	// ASP.NET routing
	routePrefix := ""
	if m := reRouteAttr.FindStringSubmatch(text); len(m) > 1 {
		routePrefix = strings.TrimSuffix(m[1], "/")
	}
	for _, m := range reHttpAttr.FindAllStringSubmatch(text, -1) {
		method := strings.ToUpper(strings.TrimPrefix(m[1], "Http"))
		path := m[2]
		if path == "" {
			path = "/"
		}
		if routePrefix != "" && !strings.HasPrefix(path, "/") {
			path = routePrefix + "/" + path
		} else if routePrefix != "" && path == "/" {
			path = routePrefix
		}
		if !strings.HasPrefix(path, "/") {
			path = "/" + path
		}
		out = append(out, Candidate{
			Kind:     "endpoint",
			Method:   method,
			Target:   path,
			FromType: "controller",
			Reason:   "Http* attribute",
		})
	}
	for _, m := range reMapGet.FindAllStringSubmatch(text, -1) {
		out = append(out, Candidate{
			Kind:     "endpoint",
			Method:   strings.ToUpper(m[1]),
			Target:   m[2],
			FromType: "controller",
			Reason:   "Minimal API Map*",
		})
	}
	for _, m := range reMapHub.FindAllStringSubmatch(text, -1) {
		out = append(out, Candidate{
			Kind:     "endpoint",
			Method:   "WS",
			Target:   m[2],
			FromType: "controller",
			Reason:   "SignalR MapHub",
		})
	}

	// DI registration hints (for agent context, not graph edges directly)
	for _, m := range reAddScoped.FindAllStringSubmatch(text, -1) {
		out = append(out, Candidate{Kind: "injects", To: m[2], Reason: "Add" + m[1] + " registration"})
	}

	return dedupeCandidates(out)
}

func dedupeCandidates(in []Candidate) []Candidate {
	seen := make(map[string]bool)
	var out []Candidate
	for _, c := range in {
		key := c.Kind + "|" + c.To + "|" + c.Method + "|" + c.Target
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, c)
	}
	return out
}
