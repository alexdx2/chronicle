package extract

import (
	"fmt"
	"io/fs"
	"path"
	"strings"

	"github.com/alexdx2/chronicle-core/truth"
	"github.com/alexdx2/chronicle-core/truth/extract/lex"
)

func isExcluded(name string) bool {
	for _, e := range Excluded {
		if e == name {
			return true
		}
	}
	return false
}

func silentSkip(name string) bool {
	return strings.HasSuffix(name, ".min.js") || strings.HasSuffix(name, ".map") || strings.HasSuffix(name, ".d.ts")
}

// Extract walks fsys, applies every matching rule and merges the facts.
func Extract(fsys fs.FS) (*truth.Report, error) {
	rep := &truth.Report{}
	tree := fsTree{fsys}
	var all []truth.Fact
	skipped := map[string]bool{}

	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != "." && isExcluded(d.Name()) {
				return fs.SkipDir
			}
			return nil
		}
		if silentSkip(d.Name()) {
			return nil
		}
		var matched []Rule
		for _, r := range Rules {
			if r.Match(p) {
				matched = append(matched, r)
			}
		}
		if len(matched) == 0 {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Size() > MaxFile {
			skipped[p] = true
			rep.SkippedFiles = append(rep.SkippedFiles, p)
			return nil
		}
		raw, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		ctx := &FileCtx{
			Path:   p,
			Dir:    path.Dir(p),
			Lines:  lex.Scan(raw, lex.FamilyFor(p)).Lines,
			Raw:    raw,
			IsTest: IsTestPath(p),
			Tree:   tree,
		}
		rep.Files++
		for _, r := range matched {
			facts, perr := applySafe(r, ctx)
			if perr != nil {
				rep.Warnings = append(rep.Warnings, fmt.Sprintf("panic in %s on %s: %v", r.Name(), p, perr.v))
				if !skipped[p] {
					skipped[p] = true
					rep.SkippedFiles = append(rep.SkippedFiles, p)
				}
				continue
			}
			all = append(all, facts...)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	merged, warns := Merge(all)
	rep.Facts = merged
	rep.Warnings = append(rep.Warnings, warns...)
	return rep, nil
}

type panicVal struct{ v any }

func applySafe(r Rule, f *FileCtx) (facts []truth.Fact, pv *panicVal) {
	defer func() {
		if x := recover(); x != nil {
			facts, pv = nil, &panicVal{x}
		}
	}()
	return r.Apply(f), nil
}
