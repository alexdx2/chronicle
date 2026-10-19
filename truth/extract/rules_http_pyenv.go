package extract

import (
	"regexp"

	"github.com/alexdx2/chronicle-core/truth"
)

func init() {
	Rules = append(Rules,
		funcRule{"python.env_default", func(p string) bool { return hasExt(p, ".py") }, applyPyEnvDefault},
	)
}

// pyEnvDefault matches a configuration read whose default is a string literal:
// os.getenv("X", "http://h:1"), os.environ.get("X", "..."), Airflow
// Variable.get("x", default_var="..."), and the settings-style helpers
// env("X", default="...") / config("X", default="..."). Capture 1 is the
// variable name, capture 2 the default. A receiver-less env/config call must
// not be a method of something else (app.config(...)).
var pyEnvDefault = regexp.MustCompile(`(?:\bos\.getenv|(?:^|[^\w.])getenv|\bos\.environ\.get|\benviron\.get|\bVariable\.get|(?:^|[^\w.])(?:env|config)(?:\.str)?)\(\s*["']([A-Za-z_][\w.\-]*)["']\s*,\s*(?:default(?:_var)?\s*=\s*)?["']([^"']*)["']`)

// applyPyEnvDefault: the default of an environment/Variable read is the host
// the service talks to when nothing overrides it. Only absolute http(s)/grpc
// URLs with a literal, non-local host count; the call may span lines.
func applyPyEnvDefault(f *FileCtx) []truth.Fact {
	if f.IsTest {
		return nil
	}
	var out []truth.Fact
	for i, l := range f.Lines {
		text := codeWindow(f.Lines, i, 3)
		for _, ix := range pyEnvDefault.FindAllStringSubmatchIndex(text, -1) {
			if ix[0] >= len(l.Code) { // the call must start on this line
				continue
			}
			key, val := text[ix[2]:ix[3]], text[ix[4]:ix[5]]
			if !anyServiceURL(key, val) {
				continue
			}
			if fc, ok := httpFact("python.env_default", f, l, val, key); ok {
				out = append(out, fc)
			}
		}
	}
	return out
}
