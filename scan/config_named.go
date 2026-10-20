package scan

import (
	"regexp"
	"strings"

	"github.com/alexdx2/chronicle-core/graph"
	"github.com/alexdx2/chronicle-core/internal/srccomment"
)

// Named HTTP clients (.NET IHttpClientFactory): a registration binds a name to
// an address, a use elsewhere asks the factory for that name.
//
//	services.AddHttpClient("billing", c => c.BaseAddress = new Uri(cfg["Services:BillingApi"]!));
//	var http = _factory.CreateClient("billing");
//
// The two sit in different files, so the config pass joins them after it has
// read every file: each use of a registered name becomes a ConfigUse of the
// registration's address.

var (
	namedRegRe = regexp.MustCompile(`AddHttpClient\s*\(\s*"([^"]+)"`)
	namedUseRe = regexp.MustCompile(`CreateClient\s*\(\s*"([^"]+)"\s*\)`)
)

type namedReg struct{ name, key, lit, file string }
type namedUse struct {
	name, file, text string
	line             int
}

func scanNamedClients(file string, body []byte) (regs []namedReg, uses []namedUse) {
	src := string(srccomment.Strip(file, body))
	if !strings.Contains(src, "AddHttpClient") && !strings.Contains(src, "CreateClient") {
		return nil, nil
	}
	locs := namedRegRe.FindAllStringSubmatchIndex(src, -1)
	for i, loc := range locs {
		end := len(src)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		if end > loc[1]+1500 {
			end = loc[1] + 1500
		}
		seg := src[loc[1]:end]
		bm := baseAddrRe.FindStringIndex(seg)
		if bm == nil {
			continue
		}
		expr := strings.TrimSpace(balancedArg(seg[bm[1]:]))
		if expr == "" {
			continue
		}
		r := namedReg{name: src[loc[2]:loc[3]], file: file}
		if len(expr) > 1 && (expr[0] == '"' || expr[0] == '\'') {
			if e := strings.IndexByte(expr[1:], expr[0]); e >= 0 {
				r.lit = expr[1 : 1+e]
			}
		} else if kind, k := graph.ClassifyCallBase(strings.TrimSuffix(expr, "!")); (kind == graph.SymConfig || kind == graph.SymEnv) && k != "" {
			r.key = k
		}
		if r.key != "" || r.lit != "" {
			regs = append(regs, r)
		}
	}
	for _, m := range namedUseRe.FindAllStringSubmatchIndex(src, -1) {
		uses = append(uses, namedUse{
			name: src[m[2]:m[3]], file: file, line: 1 + strings.Count(src[:m[0]], "\n"),
			text: strings.TrimSpace(src[m[0]:m[1]]),
		})
	}
	return regs, uses
}

// joinNamedClients adds a ConfigUse for every use whose name has exactly one
// registration. A literal address becomes a table entry of its own so the use
// has a key to resolve.
func joinNamedClients(t *graph.ConfigHosts, regs []namedReg, uses []namedUse) {
	byName := map[string][]namedReg{}
	for _, r := range regs {
		byName[strings.ToLower(r.name)] = append(byName[strings.ToLower(r.name)], r)
	}
	for _, u := range uses {
		rs := byName[strings.ToLower(u.name)]
		if len(rs) != 1 {
			continue
		}
		r := rs[0]
		key := r.key
		if key == "" {
			key = "HttpClient:" + r.name
			t.Add(key, r.lit, r.file)
		}
		t.AddUse(graph.ConfigUse{File: u.file, Line: u.line, Text: u.text, Callee: "IHttpClientFactory.CreateClient", Symbol: `Configuration["` + key + `"]`})
	}
}
