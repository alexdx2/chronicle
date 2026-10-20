package scan

import (
	"embed"
	"log"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/alexdx2/chronicle-core/graph"
	"github.com/alexdx2/chronicle-core/truth/scancfg"
)

//go:embed builtin/*.yaml
var builtinScanConfigs embed.FS

// ScanConfigFile is the project layer, relative to the repository root.
const ScanConfigFile = ".depbot/scan.yaml"

// FleetScanConfigDir, when set (or CHRONICLE_SCAN_CONFIG_DIR), holds the fleet
// layer: *.yaml files in the same format, between builtin and project.
var FleetScanConfigDir string

var scanCfgCache sync.Map // root -> cachedScanCfg

type cachedScanCfg struct {
	stamp string
	cfg   *scancfg.Config
}

// LoadScanConfig merges builtin, fleet and project layers for the repository
// at root. A broken layer is logged and skipped: a bad scan.yaml must not stop
// a scan, it must be loud (chronicle scan-config validate shows the reason).
func LoadScanConfig(root string) *scancfg.Config {
	cfg, _ := LoadScanConfigErr(root)
	return cfg
}

// LoadScanConfigErr is LoadScanConfig that also returns the project layer error.
func LoadScanConfigErr(root string) (*scancfg.Config, error) {
	fleet := FleetScanConfigDir
	if fleet == "" {
		fleet = os.Getenv("CHRONICLE_SCAN_CONFIG_DIR")
	}
	proj := filepath.Join(root, filepath.FromSlash(ScanConfigFile))
	stamp := fleet + "|"
	if fi, err := os.Stat(proj); err == nil {
		stamp += fi.ModTime().String() + "|" + string(rune(fi.Size()))
	}
	if fleet != "" {
		if es, _ := os.ReadDir(fleet); len(es) > 0 {
			for _, e := range es {
				if fi, err := e.Info(); err == nil {
					stamp += e.Name() + fi.ModTime().String()
				}
			}
		}
	}
	if v, ok := scanCfgCache.Load(root); ok && v.(cachedScanCfg).stamp == stamp {
		return v.(cachedScanCfg).cfg, nil
	}
	var layers []*scancfg.Config
	entries, _ := builtinScanConfigs.ReadDir("builtin")
	for _, e := range entries {
		b, _ := builtinScanConfigs.ReadFile("builtin/" + e.Name())
		if c, err := scancfg.Parse(b, scancfg.SourceBuiltin); err == nil {
			layers = append(layers, c)
		} else {
			log.Printf("scan: builtin scan config %s: %v", e.Name(), err)
		}
	}
	if fleet != "" {
		es, _ := os.ReadDir(fleet)
		var names []string
		for _, e := range es {
			if !e.IsDir() && (strings.HasSuffix(e.Name(), ".yaml") || strings.HasSuffix(e.Name(), ".yml")) {
				names = append(names, e.Name())
			}
		}
		sort.Strings(names)
		for _, n := range names {
			b, err := os.ReadFile(filepath.Join(fleet, n))
			if err != nil {
				continue
			}
			if c, err := scancfg.Parse(b, scancfg.SourceFleet); err == nil {
				layers = append(layers, c)
			} else {
				log.Printf("scan: fleet scan config %s: %v", n, err)
			}
		}
	}
	var perr error
	if b, err := os.ReadFile(proj); err == nil {
		if c, err := scancfg.Parse(b, scancfg.SourceProject); err == nil {
			layers = append(layers, c)
		} else {
			perr = err
			log.Printf("scan: %s ignored: %v", ScanConfigFile, err)
		}
	}
	cfg := scancfg.Merge(layers...)
	scanCfgCache.Store(root, cachedScanCfg{stamp: stamp, cfg: cfg})
	return cfg, perr
}

// ScanConfigVersion is the string appended to a file's coverage version.
func ScanConfigVersion(root string) string {
	if v := LoadScanConfig(root).CoverageVersion(); v != "" {
		return "+" + v
	}
	return ""
}

var constAssignRe = regexp.MustCompile(`(?m)^\s*(?:export\s+)?(?:public\s+|private\s+|static\s+|final\s+|readonly\s+|const\s+|let\s+|var\s+|val\s+|String\s+|string\s+)*([A-Za-z_]\w*)\s*(?::\s*[\w\[\]., ]+)?\s*[:=]\s*(?:"([^"\n]*)"|'([^'\n]*)'|` + "`([^`\\n]*)`" + `)`)

// sameFileConstants maps IDENT -> literal for simple assignments in one file.
func sameFileConstants(body []byte) map[string]string {
	m := map[string]string{}
	for _, g := range constAssignRe.FindAllStringSubmatch(string(body), -1) {
		for _, v := range g[2:] {
			if v != "" {
				m[g[1]] = v
				break
			}
		}
	}
	return m
}

func looksBinary(b []byte) bool {
	n := len(b)
	if n > 512 {
		n = 512
	}
	for _, c := range b[:n] {
		if c == 0 {
			return true
		}
	}
	return false
}

// applyScanConfig runs the declarative configuration over the tracked files:
// extra config files and key meanings, channel rules and document rules,
// clients, noise and aliases. It runs after the built-in config pass so keys
// and values it records are visible to name resolution.
func applyScanConfig(t *graph.ConfigHosts, root string, files []string, cfg *scancfg.Config, parsed *[]string) {
	if cfg == nil || cfg.Empty() {
		return
	}
	// config files named by the project
	for _, p := range files {
		for _, cf := range cfg.Config.Files {
			if !scancfg.GlobMatch(cf.Glob, p) {
				continue
			}
			body, ok := readSmall(root, p)
			if !ok {
				break
			}
			switch strings.ToLower(cf.Format) {
			case "dotenv":
				parseDotenv(t, p, body)
			case "json":
				parseAppsettings(t, p, body)
			case "xml-appsettings", "hocon", "toml", "yaml", "properties", "auto", "":
				parseGenericConfig(t, p, body)
			}
			*parsed = append(*parsed, p)
			break
		}
	}
	// key meanings, over every config file read so far
	if len(cfg.Config.Keys) > 0 {
		seenFile := map[string]bool{}
		for _, p := range append(append([]string(nil), *parsed...), files...) {
			if seenFile[p] {
				continue
			}
			seenFile[p] = true
			if !IsConfigFile(p) && !isGenericConfigFile(p) && !matchesConfigFile(cfg.Config.Files, p) {
				continue
			}
			body, ok := readSmall(root, p)
			if !ok {
				continue
			}
			doc, ok := scancfg.DecodeDocument(p, body)
			if !ok {
				continue
			}
			keys := make([]string, 0, len(doc))
			for k := range doc {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				for _, km := range cfg.Config.Keys {
					if !scancfg.GlobMatch(strings.ToLower(km.Match), strings.ToLower(k)) {
						continue
					}
					switch km.As {
					case "channel":
						t.AddChannelValue(k, doc[k], p)
					case "address":
						t.AddAddressValue(k, doc[k], p)
					}
					break
				}
			}
		}
		var ignore []string
		for _, km := range cfg.Config.Keys {
			if km.As == "ignore" {
				ignore = append(ignore, strings.ToLower(km.Match))
			}
		}
		if len(ignore) > 0 {
			t.DropKeys(func(key string) bool {
				for _, g := range ignore {
					if scancfg.GlobMatch(g, strings.ToLower(key)) {
						return true
					}
				}
				return false
			})
		}
	}
	for _, n := range cfg.Noise {
		if n.Glob != "" && n.HTTP == "ignore" {
			g := n.Glob
			out := t.Entries[:0]
			for _, e := range t.Entries {
				if e.URL != "" && scancfg.GlobMatch(g, e.File) {
					continue
				}
				out = append(out, e)
			}
			t.Entries = out
		}
	}

	if len(cfg.Channels) == 0 && len(cfg.Documents) == 0 && len(cfg.Clients) == 0 {
		return
	}
	eng, err := scancfg.NewEngine(cfg)
	if err != nil {
		log.Printf("scan: scan config engine: %v", err)
		return
	}
	clientEng, _ := scancfg.NewClientEngine(cfg)
	sorted := append([]string(nil), files...)
	sort.Strings(sorted)
	var facts []scancfg.Fact
	for _, p := range sorted {
		if !cfg.Scope.Allowed(p) {
			continue
		}
		body, ok := readSmall(root, p)
		if !ok || looksBinary(body) {
			continue
		}
		var consts map[string]string
		resolve := func(file, expr string) (string, string, bool) {
			if consts == nil {
				consts = sameFileConstants(body)
			}
			id := expr
			if i := strings.LastIndex(id, "."); i >= 0 {
				id = id[i+1:]
			}
			if v, ok := consts[id]; ok {
				return v, "constant " + id, true
			}
			if name, key, ok := t.ChannelValue(file, expr); ok {
				return name, "config " + key, true
			}
			return "", "", false
		}
		facts = append(facts, eng.Run(p, body, resolve)...)
		facts = append(facts, scancfg.RunDocuments(cfg, p, body)...)
		if clientEng != nil {
			for _, c := range clientEng.RunClients(p, body) {
				t.AddUse(graph.ConfigUse{File: c.File, Line: c.Line, Text: c.Name, Callee: "scan-config", Symbol: c.Name})
			}
		}
	}
	for _, f := range facts {
		if noisyChannel(cfg, f.Name) {
			continue
		}
		t.RuleChannels = append(t.RuleChannels, graph.RuleChannel{
			Rule: f.Rule, Source: f.Source, File: f.File, Line: f.Line, Text: evidenceFor(f),
			Name: f.Name, Side: f.Side, Kind: f.Kind, Transport: f.Transport,
		})
	}
	for _, a := range cfg.Aliases {
		t.Aliases = append(t.Aliases, graph.HostAlias{Host: strings.ToLower(a.Host), Service: a.Service})
	}
	for _, r := range cfg.Routes {
		if r.Hint != "" {
			t.Hints = append(t.Hints, r.Hint)
		}
	}
}

// evidenceFor: the literal name when the code spells it, else the matched call.
func evidenceFor(f scancfg.Fact) string {
	if f.Via == "" {
		return f.Name
	}
	return f.Text
}

func noisyChannel(cfg *scancfg.Config, name string) bool {
	for _, n := range cfg.Noise {
		if n.Name != "" && scancfg.GlobMatch(n.Name, name) {
			return true
		}
	}
	return false
}

func matchesConfigFile(fs []scancfg.ConfigFile, p string) bool {
	for _, f := range fs {
		if scancfg.GlobMatch(f.Glob, p) {
			return true
		}
	}
	return false
}

func readSmall(root, p string) ([]byte, bool) {
	fp := filepath.Join(root, filepath.FromSlash(path.Clean(p)))
	fi, err := os.Lstat(fp)
	if err != nil || !fi.Mode().IsRegular() || fi.Size() > maxConfigBytes {
		return nil, false
	}
	b, err := os.ReadFile(fp)
	return b, err == nil
}
