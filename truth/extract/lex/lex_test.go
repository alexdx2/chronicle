package lex

import (
	"strings"
	"testing"
)

func code(t *testing.T, src string, f Family) Result {
	t.Helper()
	r := Scan([]byte(src), f)
	for _, l := range r.Lines {
		if len(l.Code) != len(l.Raw) {
			t.Fatalf("line %d: Code length %d != Raw length %d", l.Num, len(l.Code), len(l.Raw))
		}
	}
	return r
}

func TestCLikeLineComment(t *testing.T) {
	r := code(t, `[HttpGet("/a")] // [HttpGet("/b")]`, CLike)
	c := r.Lines[0].Code
	if !strings.Contains(c, `"/a"`) || strings.Contains(c, "/b") {
		t.Fatalf("got %q", c)
	}
}

func TestCLikeBlockCommentMultiline(t *testing.T) {
	r := code(t, "a\n/* x\n[HttpGet(\"/obsolete\")]\n y */ b", CLike)
	if len(r.Lines) != 4 {
		t.Fatalf("lines=%d", len(r.Lines))
	}
	for i := 1; i <= 3; i++ {
		if strings.Contains(r.Lines[i].Code, "obsolete") || (strings.Contains(r.Lines[i].Code, "x") && i == 1) {
			t.Fatalf("line %d not blanked: %q", i+1, r.Lines[i].Code)
		}
	}
	if strings.TrimSpace(r.Lines[2].Code) != "" {
		t.Fatalf("got %q", r.Lines[2].Code)
	}
	if strings.TrimSpace(r.Lines[3].Code) != "b" {
		t.Fatalf("got %q", r.Lines[3].Code)
	}
}

func TestCLikeStringKeepsSlashes(t *testing.T) {
	r := code(t, `var s = "// not a comment";`, CLike)
	if r.Lines[0].Code != r.Lines[0].Raw {
		t.Fatalf("got %q", r.Lines[0].Code)
	}
}

func TestCLikeTemplateLiteral(t *testing.T) {
	r := code(t, "var s = `a\n/* x */ b`;\n// gone", CLike)
	if r.Lines[1].Code != r.Lines[1].Raw {
		t.Fatalf("got %q", r.Lines[1].Code)
	}
	if strings.TrimSpace(r.Lines[2].Code) != "" {
		t.Fatalf("got %q", r.Lines[2].Code)
	}
}

func TestCSharpVerbatim(t *testing.T) {
	r := code(t, `var p = @"C:\path\"" "; // c`, CLike)
	c := r.Lines[0].Code
	if !strings.Contains(c, `@"C:\path\"" "`) || strings.Contains(c, "// c") {
		t.Fatalf("got %q", c)
	}
}

func TestCSharpRawString(t *testing.T) {
	r := code(t, "var s = \"\"\"\n// inside\n\"\"\"; // out", CLike)
	if r.Lines[1].Code != "// inside" || strings.Contains(r.Lines[2].Code, "out") {
		t.Fatalf("got %q / %q", r.Lines[1].Code, r.Lines[2].Code)
	}
}

func TestCLikeEscape(t *testing.T) {
	r := code(t, `x = "a\" // still string"; // c`, CLike)
	c := r.Lines[0].Code
	if !strings.Contains(c, "still string") || strings.Contains(c, "// c") {
		t.Fatalf("got %q", c)
	}
}

func TestPython(t *testing.T) {
	r := code(t, "# route(\"/x\")\n\"\"\"\n@app.get(\"/y\")\n\"\"\"\nx = '#'  # c", Python)
	if strings.TrimSpace(r.Lines[0].Code) != "" {
		t.Fatalf("line1 %q", r.Lines[0].Code)
	}
	if strings.TrimSpace(r.Lines[2].Code) != "" {
		t.Fatalf("triple-quoted text is blanked: %q", r.Lines[2].Code)
	}
	if !strings.Contains(r.Lines[4].Code, "'#'") || strings.Contains(r.Lines[4].Code, "# c") {
		t.Fatalf("line5 %q", r.Lines[4].Code)
	}
}

func TestHash(t *testing.T) {
	r := code(t, `url: "http://a#b" # c`, Hash)
	c := r.Lines[0].Code
	if !strings.Contains(c, "a#b") || strings.Contains(c, "# c") {
		t.Fatalf("got %q", c)
	}
	r = code(t, "a: \"open\n# real comment", Hash)
	if r.Unterminated || strings.TrimSpace(r.Lines[1].Code) != "" {
		t.Fatalf("hash quote must close at EOL: %+v", r)
	}
}

func TestUnterminated(t *testing.T) {
	r := code(t, "a\n/* never\nb\nc", CLike)
	if !r.Unterminated {
		t.Fatal("want Unterminated")
	}
	if strings.TrimSpace(r.Lines[2].Code) != "" || strings.TrimSpace(r.Lines[3].Code) != "" {
		t.Fatalf("rest must be blank: %+v", r.Lines)
	}
	if r.Lines[0].Code != "a" {
		t.Fatalf("got %q", r.Lines[0].Code)
	}
}

func TestNoneAndCRLF(t *testing.T) {
	r := code(t, "<a>// x</a>\r\n", None)
	if len(r.Lines) != 1 || r.Lines[0].Code != r.Lines[0].Raw || !strings.HasSuffix(r.Lines[0].Raw, "\r") {
		t.Fatalf("%+v", r.Lines)
	}
}

func TestFamilyFor(t *testing.T) {
	cases := map[string]Family{
		"a/b.cs": CLike, "x.TSX": CLike, "x.mjs": CLike, "App.jsx": CLike, "x.py": Python,
		"Dockerfile": Hash, "Dockerfile.dev": Hash, ".env": Hash, ".env.local": Hash,
		"A.kt": CLike, "build.gradle.kts": CLike, "build.gradle": CLike, "application.conf": Hash, "a.yml": Hash, "a.tf": Hash, "a.toml": Hash, "a.json": None, "a.csproj": None, "a.xml": None, "a.zzz": None,
	}
	for p, want := range cases {
		if got := FamilyFor(p); got != want {
			t.Errorf("%s: got %v want %v", p, got, want)
		}
	}
}

func TestPythonUnclosedTriple(t *testing.T) {
	r := code(t, "x = 1\n\"\"\"\n@app.get(\"/z\")", Python)
	if !r.Unterminated || strings.TrimSpace(r.Lines[2].Code) != "" || r.Lines[0].Code != "x = 1" {
		t.Fatalf("%+v", r)
	}
}

func TestStrayApostropheBeforeBlockComment(t *testing.T) {
	r := code(t, "<p>Don't go {/*\n<Link to=\"/signup\"/>\n*/}</p>", CLike)
	if strings.TrimSpace(r.Lines[1].Code) != "" {
		t.Fatalf("line2 %q", r.Lines[1].Code)
	}
	if r.Lines[2].Code != "  }</p>" || r.Unterminated {
		t.Fatalf("line3 %q %v", r.Lines[2].Code, r.Unterminated)
	}
}

func TestStrayApostropheBeforeBacktick(t *testing.T) {
	r := code(t, "const s = it's `\nfoo\n`;", CLike)
	if r.Unterminated || r.Lines[1].Code != "foo" {
		t.Fatalf("%+v", r)
	}
}

func TestHashStrayApostrophe(t *testing.T) {
	r := code(t, "name: don't worry # c", Hash)
	if r.Lines[0].Code != "name: don't worry    " {
		t.Fatalf("%q", r.Lines[0].Code)
	}
}

func TestUnterminatedBacktickAndVerbatim(t *testing.T) {
	for _, src := range []string{"a\n`open\nb", "a\nx = @\"open\nb"} {
		r := code(t, src, CLike)
		if !r.Unterminated || strings.TrimSpace(r.Lines[2].Code) != "" {
			t.Fatalf("%q: %+v", src, r)
		}
	}
}

func TestCLikeCRLFComment(t *testing.T) {
	r := code(t, "a // c\r\nb\r\n", CLike)
	if len(r.Lines) != 2 || r.Lines[0].Code != "a     \r" || r.Lines[1].Code != "b\r" {
		t.Fatalf("%q", r.Lines)
	}
}

func TestJSXCommentsBlanked(t *testing.T) {
	r := code(t, "const a = 1 // api.get('/x')\n", FamilyFor("App.jsx"))
	if strings.Contains(r.Lines[0].Code, "api.get") {
		t.Fatalf("comment leaked: %q", r.Lines[0].Code)
	}
}
