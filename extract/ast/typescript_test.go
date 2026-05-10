package ast

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

// TestRawExtraction verifies the AST extracts raw syntax facts without framework meaning.
func TestRawExtraction(t *testing.T) {
	files := []struct {
		path   string
		checks []rawCheck
	}{
		{
			"../../fixtures/tom-and-jerry/arena-api/src/arena/arena.controller.ts",
			[]rawCheck{
				{kind: "import", to: "./arena.service"},
				{kind: "decorator", name: "Controller"},
				{kind: "decorator", name: "Post", method: "attack"},
				{kind: "decorator", name: "Get", method: "getHistory"},
				{kind: "constructor_param", to: "ArenaService"},
				{kind: "call", to: "arenaService"},
			},
		},
		{
			"../../fixtures/tom-and-jerry/arena-api/src/arena/arena.module.ts",
			[]rawCheck{
				{kind: "import", to: "./arena.controller"},
				{kind: "import", to: "../prisma/prisma.service"},
				{kind: "decorator", name: "Module"},
			},
		},
		{
			"../../fixtures/tom-and-jerry/arena-api/src/arena/arena.service.ts",
			[]rawCheck{
				{kind: "constructor_param", to: "PrismaService"},
				{kind: "constructor_param", to: "TomClient"},
				{kind: "call", to: "tomClient", method: "getStatus"},
				{kind: "member_call", from: "prisma", to: "battleEvent"},
			},
		},
		{
			"../../fixtures/tom-and-jerry/tom-api/src/tom/tom.events.ts",
			[]rawCheck{
				{kind: "decorator", name: "OnEvent"},
				{kind: "produces_candidate", to: "tom.weapon.equipped"},
				{kind: "constructor_param", to: "EventEmitter2"},
			},
		},
	}

	for _, f := range files {
		src, err := os.ReadFile(f.path)
		if err != nil {
			t.Skipf("not available: %s", f.path)
		}
		result := ExtractTypeScript(src)
		name := f.path[strings.LastIndex(f.path, "/")+1:]

		fmt.Printf("\n%s: %d raw facts\n", name, len(result.Facts))
		for _, fact := range result.Facts {
			b, _ := json.Marshal(fact)
			fmt.Printf("  %s\n", b)
		}

		for _, check := range f.checks {
			if !hasRawFact(result.Facts, check) {
				t.Errorf("%s: missing %s", name, check.String())
			}
		}

		// Verify NO semantic facts (no "endpoint", "injects", "consumes" kinds)
		for _, fact := range result.Facts {
			switch fact.Kind {
			case "endpoint", "injects", "consumes", "produces", "model":
				t.Errorf("%s: AST should not emit semantic kind %q — that's the rules layer", name, fact.Kind)
			}
		}
	}
}

type rawCheck struct {
	kind, name, to, from, method string
}

func (c rawCheck) String() string {
	s := c.kind
	if c.name != "" {
		s += " name=" + c.name
	}
	if c.to != "" {
		s += " to=" + c.to
	}
	if c.from != "" {
		s += " from=" + c.from
	}
	if c.method != "" {
		s += " method=" + c.method
	}
	return s
}

func hasRawFact(facts []RawFact, check rawCheck) bool {
	for _, f := range facts {
		if f.Kind != check.kind {
			continue
		}
		if check.name != "" && f.Name != check.name {
			continue
		}
		if check.to != "" && f.To != check.to {
			continue
		}
		if check.from != "" && f.From != check.from {
			continue
		}
		if check.method != "" && f.Method != check.method {
			continue
		}
		return true
	}
	return false
}
