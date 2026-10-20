package extract

import (
	"fmt"
	"io/fs"
	"strings"

	"github.com/alexdx2/chronicle-core/truth"
	"github.com/alexdx2/chronicle-core/truth/scancfg"
)

// scanConfigFile is the project layer of the declarative scan configuration.
const scanConfigFile = ".depbot/scan.yaml"

// scanConfigFacts runs the project's scan.yaml rules that a human reviewed
// (`reviewed: true`) and returns their channel facts. Unreviewed rules, in
// particular those a model proposed, shape the scan but never the reference:
// the oracle must not grade a rule with its own output. Names come from string
// literals and same-file constants only; configuration lookups are the scan's.
func scanConfigFacts(fsys fs.FS, rep *truth.Report) []truth.Fact {
	raw, err := fs.ReadFile(fsys, scanConfigFile)
	if err != nil {
		return nil
	}
	cfg, err := scancfg.Parse(raw, scancfg.SourceProject)
	if err != nil {
		rep.Warnings = append(rep.Warnings, fmt.Sprintf("%s ignored: %v", scanConfigFile, err))
		return nil
	}
	cfg = cfg.Trusted()
	if len(cfg.Channels) == 0 && len(cfg.Documents) == 0 {
		return nil
	}
	eng, err := scancfg.NewEngine(cfg)
	if err != nil {
		rep.Warnings = append(rep.Warnings, fmt.Sprintf("%s: %v", scanConfigFile, err))
		return nil
	}
	var out []truth.Fact
	_ = fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != "." && isExcluded(d.Name()) {
				return fs.SkipDir
			}
			return nil
		}
		if silentSkip(d.Name()) || IsSamplePath(p) || !cfg.Scope.Allowed(p) {
			return nil
		}
		if info, err := d.Info(); err != nil || info.Size() > MaxFile {
			return nil
		}
		src, err := fs.ReadFile(fsys, p)
		if err != nil {
			return nil
		}
		var consts map[string]string
		facts := eng.Run(p, src, func(_, expr string) (string, string, bool) {
			if consts == nil {
				consts = scancfg.SameFileConstants(src)
			}
			id := expr
			if i := strings.LastIndex(id, "."); i >= 0 {
				id = id[i+1:]
			}
			v, ok := consts[id]
			return v, "constant " + id, ok
		})
		facts = append(facts, scancfg.RunDocuments(cfg, p, src)...)
		for _, f := range facts {
			n := channelName(f.Name)
			if n == "" {
				continue
			}
			out = append(out, truth.Fact{
				Class:   truth.ClassChannels,
				Key:     truth.ChannelKey(f.Kind, n, f.Side),
				Attrs:   map[string]string{"name": f.Name, "kind": f.Kind, "side": f.Side},
				Primary: truth.Evidence{File: f.File, Line: f.Line, Text: f.Text},
				Sources: []string{"scancfg:" + f.Rule},
			})
		}
		return nil
	})
	return out
}
