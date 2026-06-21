package rules

import (
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/extract/ast"
)

func TestRulesetsForTech(t *testing.T) {
	if rs := RulesetsForTech([]string{"nestjs"}); len(rs) == 0 {
		t.Error("nestjs should yield rulesets")
	}
	if rs := RulesetsForTech([]string{"graphql"}); len(rs) == 0 {
		t.Error("graphql should yield rulesets")
	}
	if rs := RulesetsForTech([]string{"socket.io"}); len(rs) == 0 {
		t.Error("socketio should yield rulesets")
	}
	if rs := RulesetsForTech([]string{"bullmq"}); len(rs) == 0 {
		t.Error("bull should yield rulesets")
	}
	// Unknown tech → defaults.
	def := RulesetsForTech([]string{"cobol"})
	if len(def) == 0 {
		t.Error("unknown tech should fall back to defaults")
	}
	if len(def) != len(DefaultRulesets()) {
		t.Errorf("unknown tech should equal DefaultRulesets (%d vs %d)", len(def), len(DefaultRulesets()))
	}
}

func TestResolveTarget(t *testing.T) {
	f := ast.RawFact{Target: "topicA", Method: "handleX"}
	if got := resolveTarget(DecoratorRule{TargetFrom: "first_string_arg"}, f); got != "topicA" {
		t.Errorf("first_string_arg: %q", got)
	}
	if got := resolveTarget(DecoratorRule{TargetFrom: "method_name"}, f); got != "handleX" {
		t.Errorf("method_name: %q", got)
	}
	if got := resolveTarget(DecoratorRule{TargetFrom: "method_name_if_no_string_arg"}, f); got != "topicA" {
		t.Errorf("method_name_if_no_string_arg with target: %q", got)
	}
	noTarget := ast.RawFact{Method: "handleX"}
	if got := resolveTarget(DecoratorRule{TargetFrom: "method_name_if_no_string_arg"}, noTarget); got != "handleX" {
		t.Errorf("method_name_if_no_string_arg without target: %q", got)
	}
	if got := resolveTarget(DecoratorRule{TargetFrom: "unknown"}, f); got != "topicA" {
		t.Errorf("default branch: %q", got)
	}
}

func TestApplyResult_FactsJSON(t *testing.T) {
	empty := &ApplyResult{}
	if empty.FactsJSON() != "[]" {
		t.Errorf("empty FactsJSON should be []: %q", empty.FactsJSON())
	}
	r := &ApplyResult{Facts: []SemanticFact{{Kind: "produces", To: "topicA"}}}
	js := r.FactsJSON()
	if !strings.Contains(js, "produces") || !strings.Contains(js, "topicA") {
		t.Errorf("FactsJSON: %q", js)
	}
}
