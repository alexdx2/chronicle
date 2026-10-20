package compare

import (
	"path"
	"sort"
	"strings"

	"github.com/alexdx2/chronicle-core/snapshot"
	"github.com/alexdx2/chronicle-core/truth"
)

// Compare judges every fact of rep against snap. It never panics: a nil
// snapshot yields all facts "missing" with the warning "no snapshot".
func Compare(rep *truth.Report, snap *snapshot.Snapshot) *truth.Verdict {
	v := &truth.Verdict{Facts: []truth.JudgedFact{}}
	if rep == nil {
		rep = &truth.Report{}
	}
	v.SkippedFiles = append(v.SkippedFiles, rep.SkippedFiles...)
	seenW := map[string]bool{}
	addWarn := func(w string) {
		if !seenW[w] {
			seenW[w] = true
			v.Warnings = append(v.Warnings, w)
		}
	}
	for _, w := range rep.Warnings {
		addWarn(w)
	}
	if snap == nil {
		addWarn("no snapshot")
	}
	ix := buildIndex(snap)
	haveCoverage := len(ix.coverage) > 0
	if snap != nil && !haveCoverage {
		addWarn("snapshot has no coverage records; scope unknown")
	}
	matched := map[truth.Class]map[string]bool{}
	for _, c := range truth.Classes {
		matched[c] = map[string]bool{}
	}

	for _, f := range rep.Facts {
		jf := truth.JudgedFact{Fact: f}
		status, node := ix.match(f)
		for _, w := range ix.warns {
			addWarn(w)
		}
		ix.warns = nil
		if status == "" {
			switch {
			case f.Class == truth.ClassDeps && (truth.DevSection(f.Attrs["section"]) || truth.Noise(f.Attrs["ecosystem"], depName(f))):
				status = truth.Ignored
			case haveCoverage && scoped(f.Class) && !ix.coverage[strings.TrimPrefix(f.Primary.File, "./")]:
				status = truth.OutOfScope
			default:
				status = truth.Missing
			}
		} else {
			matched[f.Class][node] = true
		}
		jf.Status, jf.GraphNodeKey = status, node
		v.Facts = append(v.Facts, jf)
	}

	v.Uncovered = ix.uncovered(matched)
	v.Classes = Summaries(v.Facts, v.Uncovered)
	return v
}

func scoped(c truth.Class) bool {
	return c == truth.ClassEndpoints || c == truth.ClassHTTP || c == truth.ClassChannels
}

func depName(f truth.Fact) string {
	if n := f.Attrs["name"]; n != "" {
		return n
	}
	if i := strings.Index(f.Key, ":"); i >= 0 {
		return f.Key[i+1:]
	}
	return f.Key
}

func depEco(f truth.Fact) string {
	if e := f.Attrs["ecosystem"]; e != "" {
		return e
	}
	if i := strings.Index(f.Key, ":"); i >= 0 {
		return f.Key[:i]
	}
	return ""
}

// match returns the status and graph node key of the best match, or "" status.
func (ix *index) match(f truth.Fact) (truth.Status, string) {
	switch f.Class {
	case truth.ClassEndpoints:
		return ix.matchEndpoint(f)
	case truth.ClassHTTP:
		return ix.matchHTTP(f)
	case truth.ClassChannels:
		return ix.matchChannel(f)
	case truth.ClassDeps:
		return ix.matchDep(f)
	case truth.ClassServices:
		return ix.matchService(f)
	case truth.ClassPackages:
		return ix.matchPackage(f)
	}
	return "", ""
}

// matchPackage confirms a published package: the graph has a package node
// that is the target of a PUBLISHES edge, or a service node of the same
// canonical name that publishes. Manifests are always in scope.
func (ix *index) matchPackage(f truth.Fact) (truth.Status, string) {
	eco, name := depEco(f), depName(f)
	best := ""
	for _, i := range ix.pkgByDep[truth.CanonDep(eco, name)] {
		if p := ix.pkgs[i]; p.own && (best == "" || p.key < best) {
			best = p.key
		}
	}
	if best != "" {
		return truth.Confirmed, best
	}
	c := truth.CanonName(name)
	for _, s := range ix.services {
		if s.typ == "service" && s.publishes && c != "" && s.names[c] && (best == "" || s.key < best) {
			best = s.key
		}
	}
	if best != "" {
		return truth.Confirmed, best
	}
	return "", ""
}

func splitEndpointKey(key string) (method, path string) {
	if i := strings.Index(key, " "); i >= 0 {
		return strings.ToUpper(key[:i]), key[i+1:]
	}
	return "GET", key
}

func (ix *index) matchEndpoint(f truth.Fact) (truth.Status, string) {
	m, p := splitEndpointKey(f.Key)
	p = truth.CanonPath(p)
	sameMethod := func(g epNode) bool { return m == "ANY" || g.method == "ANY" || g.method == m }
	for _, g := range ix.endpoints {
		if sameMethod(g) && g.path == p {
			return truth.Confirmed, g.key
		}
	}
	// Loose match: one canonical path is a suffix of the other at a "/"
	// boundary; a graph-side ANY method is accepted like a fact-side one.
	best, bestDiff := "", -1
	for _, g := range ix.endpoints {
		if !sameMethod(g) || g.path == p || g.path == "/" || p == "/" {
			continue
		}
		long, short := g.path, p
		if len(short) > len(long) {
			long, short = short, long
		}
		// A shorter path made only of parameters ("/{}") would match any
		// endpoint ending in a parameter; it must carry a literal segment.
		if strings.HasSuffix(long, short) && hasLiteralSegment(short) {
			if d := len(long) - len(short); bestDiff < 0 || d < bestDiff || (d == bestDiff && g.key < best) {
				best, bestDiff = g.key, d
			}
		}
	}
	if best != "" {
		return truth.ConfirmedLoose, best
	}
	return "", ""
}

func hasLiteralSegment(p string) bool {
	for _, s := range strings.Split(p, "/") {
		if s != "" && s != "{}" {
			return true
		}
	}
	return false
}

// channelKindOK: every channel kind (topic, queue, stream, subject, exchange,
// subscription, bus, channel) is one named channel, and the graph mints one
// node type for all of them (a topic node; a queue node exists where a manifest
// declared one). The fact matches a node of either type; the kind it was read
// as is information, not a condition.
func channelKindOK(kind, typ string) bool {
	if typ != "topic" && typ != "queue" {
		return false
	}
	return true
}

func (ix *index) matchChannel(f truth.Fact) (truth.Status, string) {
	c := truth.CanonName(f.Attrs["name"])
	if c == "" {
		return "", ""
	}
	best := ""
	for _, g := range ix.channels {
		if g.canon != c || !channelKindOK(f.Attrs["kind"], g.typ) {
			continue
		}
		var ok bool
		switch f.Attrs["side"] {
		case "produce":
			ok = g.produce
		case "consume":
			ok = g.consume
		default: // declare: the node existing is enough
			ok = true
		}
		if ok && (best == "" || g.key < best) {
			best = g.key
		}
	}
	if best != "" {
		return truth.Confirmed, best
	}
	return "", ""
}

func (ix *index) matchDep(f truth.Fact) (truth.Status, string) {
	eco, name := depEco(f), depName(f)
	project := strings.EqualFold(eco, "project")
	pick := func(idx []int, crossCheck bool) string {
		best := ""
		for _, i := range idx {
			p := ix.pkgs[i]
			if !p.incoming && !(project && p.own) {
				continue
			}
			if crossCheck && eco != "" && !project && p.eco != "" && !strings.EqualFold(p.eco, "project") && !strings.EqualFold(p.eco, eco) {
				continue
			}
			if best == "" || p.key < best {
				best = p.key
			}
		}
		return best
	}
	if project {
		c := truth.CanonName(name)
		if k := pick(ix.pkgByName[c], false); k != "" {
			return truth.Confirmed, k
		}
		for _, s := range ix.services {
			if s.names[c] {
				return truth.Confirmed, s.key
			}
		}
		return "", ""
	}
	if k := pick(ix.pkgByDep[truth.CanonDep(eco, name)], false); k != "" {
		return truth.Confirmed, k
	}
	if k := pick(ix.pkgByName[truth.CanonName(name)], true); k != "" {
		return truth.Confirmed, k
	}
	return "", ""
}

func (ix *index) matchService(f truth.Fact) (truth.Status, string) {
	cands := []string{truth.CanonName(f.Attrs["name"]), truth.CanonName(truth.StripScope(f.Attrs["name"]))}
	if f.Attrs["kind"] == "infra" || f.Attrs["kind"] == "infrastructure" {
		cands = append(cands, truth.CanonName(f.Attrs["tech"]))
	}
	best := ""
	for _, s := range ix.services {
		// An own service is a deployable of this repo: it confirms only
		// against a service node, never an external system.
		if f.Attrs["kind"] == "own" && s.typ != "service" {
			continue
		}
		for _, c := range cands {
			if c != "" && s.names[c] && (best == "" || s.key < best) {
				best = s.key
			}
		}
	}
	if best != "" {
		return truth.Confirmed, best
	}
	// An own service whose name differs from the graph's (a package name
	// versus a directory name): the service node whose directory contains the
	// fact's file is the same deployable. The deepest directory wins.
	if f.Attrs["kind"] == "own" && f.Primary.File != "" {
		file := strings.TrimPrefix(strings.ReplaceAll(f.Primary.File, "\\", "/"), "./")
		bestDir := ""
		for _, s := range ix.services {
			if s.typ != "service" || s.file == "" {
				continue
			}
			dir := path.Dir(strings.TrimPrefix(strings.ReplaceAll(s.file, "\\", "/"), "./"))
			if dir == "." || dir == "/" || !strings.HasPrefix(file, dir+"/") {
				continue
			}
			if len(dir) > len(bestDir) || (len(dir) == len(bestDir) && s.key < best) {
				bestDir, best = dir, s.key
			}
		}
		if best != "" {
			return truth.Confirmed, best
		}
	}
	return "", ""
}

func (ix *index) matchHTTP(f truth.Fact) (truth.Status, string) {
	host := truth.CanonHost(f.Attrs["host"])
	if host == "" && !strings.HasPrefix(f.Key, "var:") {
		host = truth.CanonHost(f.Key)
	}
	varName := f.Attrs["var"]
	if varName == "" {
		varName = f.Attrs["config_key"]
	}
	if varName == "" && strings.HasPrefix(f.Key, "var:") {
		varName = strings.TrimPrefix(f.Key, "var:")
	}
	var names []string
	if host != "" && !strings.Contains(host, ".") { // single-label host only
		names = append(names, truth.CanonName(host))
	}
	if varName != "" {
		names = append(names, truth.CanonName(truth.HostFromVar(varName)))
	}
	best := ""
	for _, n := range ix.http {
		ok := host != "" && n.hosts[host]
		for _, c := range names {
			if c != "" && n.names[c] {
				ok = true
			}
		}
		if ok && (best == "" || n.key < best) {
			best = n.key
		}
	}
	if best != "" {
		return truth.Confirmed, best
	}
	// Loose tier: var-derived facts and single-label hosts only; a dotted
	// literal host never loosens. Exactly one candidate node may match.
	var lk []string
	if varName != "" {
		if t := truth.LooseTokens(truth.HostFromVar(varName)); t != "" {
			lk = append(lk, t)
		}
	}
	if host != "" && !strings.Contains(host, ".") {
		if t := truth.LooseTokens(host); t != "" {
			lk = append(lk, t)
		}
	}
	var cands []string
	for _, n := range ix.http {
		for _, k := range lk {
			if n.loose[k] {
				cands = append(cands, n.key)
				break
			}
		}
	}
	sort.Strings(cands)
	switch len(cands) {
	case 0:
	case 1:
		return truth.ConfirmedLoose, cands[0]
	default:
		key := f.Key
		ix.warns = append(ix.warns, "ambiguous loose http match for "+key+": "+strings.Join(cands, ", "))
	}
	return "", ""
}

// uncovered lists graph entities of the reference classes that no fact matched.
func (ix *index) uncovered(matched map[truth.Class]map[string]bool) []truth.GraphFact {
	out := []truth.GraphFact{}
	add := func(c truth.Class, key, name string) {
		if !matched[c][key] {
			out = append(out, truth.GraphFact{Class: c, NodeKey: key, Name: name})
		}
	}
	for _, e := range ix.endpoints {
		name := e.name
		if name == "" {
			name = truth.CanonEndpoint(e.method, e.path)
		}
		add(truth.ClassEndpoints, e.key, name)
	}
	for _, h := range ix.http {
		add(truth.ClassHTTP, h.key, h.name)
	}
	for _, c := range ix.channels {
		add(truth.ClassChannels, c.key, c.name)
	}
	for _, p := range ix.pkgs {
		if p.own {
			add(truth.ClassPackages, p.key, p.name)
		} else {
			add(truth.ClassDeps, p.key, p.name)
		}
	}
	for _, s := range ix.services {
		if s.typ != "external_system" {
			add(truth.ClassServices, s.key, s.name)
		}
	}
	order := map[truth.Class]int{}
	for i, c := range truth.Classes {
		order[c] = i
	}
	sort.SliceStable(out, func(i, j int) bool {
		if order[out[i].Class] != order[out[j].Class] {
			return order[out[i].Class] < order[out[j].Class]
		}
		return out[i].NodeKey < out[j].NodeKey
	})
	return out
}

func tally(c *truth.Counts, s truth.Status) {
	switch s {
	case truth.Confirmed:
		c.Confirmed++
	case truth.ConfirmedLoose:
		c.Loose++
	case truth.Missing:
		c.Missing++
	case truth.OutOfScope:
		c.OutOfScope++
	case truth.Ignored:
		c.Ignored++
	}
}

func finish(c *truth.Counts) {
	c.Known = c.Confirmed + c.Loose + c.Missing + c.OutOfScope
}

// Summaries aggregates judged facts into one ClassSummary per class values in
// truth.Classes order, with metrics and a per-source breakdown.
func Summaries(facts []truth.JudgedFact, uncovered []truth.GraphFact) []truth.ClassSummary {
	out := make([]truth.ClassSummary, 0, len(truth.Classes))
	for _, cl := range truth.Classes {
		cs := truth.ClassSummary{Class: cl, BySource: []truth.SourceSummary{}}
		bySrc := map[string]*truth.Counts{}
		for _, f := range facts {
			if f.Class != cl {
				continue
			}
			tally(&cs.Counts, f.Status)
			for _, s := range f.Sources {
				c := bySrc[s]
				if c == nil {
					c = &truth.Counts{}
					bySrc[s] = c
				}
				tally(c, f.Status)
			}
		}
		for _, u := range uncovered {
			if u.Class == cl {
				cs.Uncovered++
			}
		}
		finish(&cs.Counts)
		cs.Metrics = cs.Counts.Compute()
		names := make([]string, 0, len(bySrc))
		for s := range bySrc {
			names = append(names, s)
		}
		sort.Strings(names)
		for _, s := range names {
			c := *bySrc[s]
			finish(&c)
			cs.BySource = append(cs.BySource, truth.SourceSummary{Source: s, Counts: c, Metrics: c.Compute()})
		}
		out = append(out, cs)
	}
	return out
}
