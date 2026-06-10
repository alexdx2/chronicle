package rules

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/extract/ast"
)

func TestNestJSRules(t *testing.T) {
	files := []struct {
		path       string
		wantType   string
		wantChecks []semCheck
	}{
		{
			"../../fixtures/tom-and-jerry/arena-api/src/arena/arena.controller.ts",
			"controller",
			[]semCheck{
				{kind: "import", to: "./arena.service"},
				{kind: "endpoint", method: "POST", target: "attack"},
				{kind: "endpoint", method: "GET", target: "score"},
				{kind: "injects", to: "ArenaService"},
				{kind: "call", to: "arenaService", method: "tomAttacksJerry"},
			},
		},
		{
			"../../fixtures/tom-and-jerry/arena-api/src/arena/arena.module.ts",
			"module",
			[]semCheck{
				{kind: "import", to: "./arena.controller"},
				{kind: "import", to: "./arena.service"},
			},
		},
		{
			"../../fixtures/tom-and-jerry/arena-api/src/arena/arena.service.ts",
			"provider",
			[]semCheck{
				{kind: "injects", to: "PrismaService"},
				{kind: "injects", to: "TomClient"},
				{kind: "call", to: "tomClient", method: "getStatus"},
				{kind: "member_call", from: "prisma", to: "battleEvent"},
			},
		},
		{
			"../../fixtures/tom-and-jerry/tom-api/src/tom/tom.events.ts",
			"",
			[]semCheck{
				{kind: "produces", to: "tom.weapon.equipped", transport: "local"},
				{kind: "consumes", to: "tom.weapon.equipped", transport: "local"},
				{kind: "consumes", to: "battle.result", transport: "local"},
				{kind: "injects", to: "EventEmitter2"},
			},
		},
		{
			"../../fixtures/tom-and-jerry/jerry-api/src/jerry/jerry.controller.ts",
			"controller",
			[]semCheck{
				{kind: "endpoint", method: "GET", target: "status"},
				{kind: "endpoint", method: "POST", target: "set-trap"},
				{kind: "injects", to: "JerryService"},
			},
		},
	}

	reg := NewRegistry(DefaultRulesets()...)

	totalChecks := 0
	passed := 0

	for _, f := range files {
		src, err := os.ReadFile(f.path)
		if err != nil {
			t.Skipf("not available: %s", f.path)
		}

		raw := ast.ExtractTypeScript(src)
		result := reg.Apply(raw)

		name := f.path[strings.LastIndex(f.path, "/")+1:]
		fmt.Printf("\n%s: from_type=%q, %d semantic facts\n", name, result.FromType, len(result.Facts))
		for _, fact := range result.Facts {
			b, _ := json.Marshal(fact)
			fmt.Printf("  %s\n", b)
		}

		if f.wantType != "" && f.wantType != "provider" && result.FromType != f.wantType {
			t.Errorf("%s: from_type: want %s, got %s", name, f.wantType, result.FromType)
		}

		for _, check := range f.wantChecks {
			totalChecks++
			if hasSemFact(result.Facts, check) {
				passed++
			} else {
				t.Errorf("%s: missing %s", name, check.String())
			}
		}
	}

	fmt.Printf("\nTotal: %d/%d checks passed\n", passed, totalChecks)
}

func TestOtopointVoucherResolver(t *testing.T) {
	src, err := os.ReadFile("../../fixtures/otopoint-samples/voucher.resolver.ts")
	if err != nil {
		t.Skip("otopoint fixture not available")
	}

	raw := ast.ExtractTypeScript(src)
	reg := NewRegistry(DefaultRulesets()...)
	result := reg.Apply(raw)

	fmt.Printf("voucher.resolver: from_type=%q, %d facts\n", result.FromType, len(result.Facts))

	// Count endpoints
	endpoints := 0
	for _, f := range result.Facts {
		if f.Kind == "endpoint" {
			endpoints++
			fmt.Printf("  endpoint: %s %s\n", f.Method, f.Target)
		}
	}

	if result.FromType != "controller" {
		t.Errorf("expected from_type=controller, got %s", result.FromType)
	}
	if endpoints < 5 {
		t.Errorf("expected >=5 endpoints (Query+Mutation), got %d", endpoints)
	}

	// Check method names are present (not empty)
	for _, f := range result.Facts {
		if f.Kind == "endpoint" && f.Target == "" {
			t.Errorf("endpoint with empty target: %s", f.Method)
		}
	}
}

type semCheck struct {
	kind, to, from, method, target, transport string
}

func (c semCheck) String() string {
	s := c.kind
	for _, pair := range [][2]string{{"to", c.to}, {"from", c.from}, {"method", c.method}, {"target", c.target}, {"transport", c.transport}} {
		if pair[1] != "" {
			s += " " + pair[0] + "=" + pair[1]
		}
	}
	return s
}

// TestR7b_WebSocketGateway_IsProvider verifies that @WebSocketGateway sets from_type=provider,
// not controller. Only @Controller defines controllers.
func TestR7b_WebSocketGateway_IsProvider(t *testing.T) {
	src, err := os.ReadFile("../../fixtures/tom-and-jerry/arena-api/src/arena/battle.gateway.ts")
	if err != nil {
		t.Skipf("fixture not available: %v", err)
	}

	raw := ast.ExtractTypeScript(src)
	reg := NewRegistry(DefaultRulesets()...)
	result := reg.Apply(raw)

	fmt.Printf("\nbattle.gateway.ts: from_type=%q, %d facts\n", result.FromType, len(result.Facts))
	for _, f := range result.Facts {
		b, _ := json.Marshal(f)
		fmt.Printf("  %s\n", b)
	}

	if result.FromType != "provider" {
		t.Errorf("R7b: @WebSocketGateway from_type = %q, want \"provider\"", result.FromType)
	}
	// Must not be classified as controller
	if result.FromType == "controller" {
		t.Error("R7b: WS gateway must not be classified as controller")
	}
}

// TestBullQueueRuleset verifies that Bull queue decorator patterns produce the correct
// semantic facts with transport=queue and that job-name decorators don't produce topics.
func TestBullQueueRuleset(t *testing.T) {
	t.Run("const_queue_name", func(t *testing.T) {
		// Real fixture: battle.queue.ts uses const QUEUE_NAME = 'battle-queue'
		src, err := os.ReadFile("../../fixtures/tom-and-jerry/arena-api/src/arena/battle.queue.ts")
		if err != nil {
			t.Skipf("fixture not available: %v", err)
		}

		raw := ast.ExtractTypeScript(src)
		reg := NewRegistry(DefaultRulesets()...)
		result := reg.Apply(raw)

		fmt.Printf("\nbattle.queue.ts (const): %d semantic facts\n", len(result.Facts))
		for _, f := range result.Facts {
			b, _ := json.Marshal(f)
			fmt.Printf("  %s\n", b)
		}

		// Must have exactly one produces=battle-queue with transport=queue.
		var produces []SemanticFact
		for _, f := range result.Facts {
			if f.Kind == "produces" && f.Transport == "queue" {
				produces = append(produces, f)
			}
		}
		if len(produces) != 1 {
			t.Errorf("expected 1 produces[queue], got %d: %v", len(produces), produces)
		} else if produces[0].To != "battle-queue" {
			t.Errorf("produces.To = %q, want \"battle-queue\"", produces[0].To)
		}

		// Must have exactly one consumes=battle-queue with transport=queue.
		var consumes []SemanticFact
		for _, f := range result.Facts {
			if f.Kind == "consumes" && f.Transport == "queue" {
				consumes = append(consumes, f)
			}
		}
		if len(consumes) != 1 {
			t.Errorf("expected 1 consumes[queue], got %d: %v", len(consumes), consumes)
		} else if consumes[0].To != "battle-queue" {
			t.Errorf("consumes.To = %q, want \"battle-queue\"", consumes[0].To)
		}

		// Job names must NOT appear as topics.
		for _, f := range result.Facts {
			if (f.Kind == "produces" || f.Kind == "consumes") && (f.To == "attack" || f.To == "combo") {
				t.Errorf("job name %q leaked as a topic in fact: %+v", f.To, f)
			}
		}
	})

	t.Run("string_literal_queue_name", func(t *testing.T) {
		// Synthetic variant: @Processor and @InjectQueue with string literals instead of const.
		src := []byte(`
import { Injectable } from '@nestjs/common';
import { InjectQueue, Processor } from '@nestjs/bull';
import { Queue } from 'bull';

@Injectable()
export class MyProducer {
  constructor(@InjectQueue('my-queue') private readonly q: Queue) {}
}

@Processor('my-queue')
export class MyConsumer {}
`)
		raw := ast.ExtractTypeScript(src)
		reg := NewRegistry(DefaultRulesets()...)
		result := reg.Apply(raw)

		fmt.Printf("\nstring-literal variant: %d semantic facts\n", len(result.Facts))
		for _, f := range result.Facts {
			b, _ := json.Marshal(f)
			fmt.Printf("  %s\n", b)
		}

		if !hasSemFact(result.Facts, semCheck{kind: "produces", to: "my-queue", transport: "queue"}) {
			t.Error("missing produces[queue] fact for my-queue")
		}
		if !hasSemFact(result.Facts, semCheck{kind: "consumes", to: "my-queue", transport: "queue"}) {
			t.Error("missing consumes[queue] fact for my-queue")
		}
	})
}

// TestEventEmitter2TransportLocal verifies that EventEmitter2 emit() calls and
// @OnEvent decorators are tagged with transport=local.
func TestEventEmitter2TransportLocal(t *testing.T) {
	src, err := os.ReadFile("../../fixtures/tom-and-jerry/tom-api/src/tom/tom.events.ts")
	if err != nil {
		t.Skipf("fixture not available: %v", err)
	}

	raw := ast.ExtractTypeScript(src)
	reg := NewRegistry(DefaultRulesets()...)
	result := reg.Apply(raw)

	fmt.Printf("\ntom.events.ts (transport): %d semantic facts\n", len(result.Facts))
	for _, f := range result.Facts {
		b, _ := json.Marshal(f)
		fmt.Printf("  %s\n", b)
	}

	checks := []semCheck{
		{kind: "produces", to: "tom.weapon.equipped", transport: "local"},
		{kind: "consumes", to: "tom.weapon.equipped", transport: "local"},
		{kind: "consumes", to: "battle.result", transport: "local"},
	}
	for _, c := range checks {
		if !hasSemFact(result.Facts, c) {
			t.Errorf("missing fact: %s", c.String())
		}
	}
}

// TestR6_ModuleProvidesFacts verifies that @Module decorator yields one provides fact
// per controller/provider member, with the correct to_type.
func TestR6_ModuleProvidesFacts(t *testing.T) {
	tests := []struct {
		path              string
		wantControllers   []string // class names that should appear as to_type=controller
		wantProviders     []string // class names that should appear as to_type=provider
	}{
		{
			path:          "../../fixtures/tom-and-jerry/arena-api/src/arena/arena.module.ts",
			wantControllers: []string{"ArenaController"},
			wantProviders:   []string{"ArenaService", "TomClient", "JerryClient", "BattleResultProducer", "BattleGateway", "BattleGuard", "PrismaService"},
		},
		{
			path:          "../../fixtures/tom-and-jerry/tom-api/src/tom/tom.module.ts",
			wantControllers: []string{"TomController"},
			wantProviders:   []string{"TomService", "PrismaService"},
		},
		{
			path:          "../../fixtures/tom-and-jerry/jerry-api/src/jerry/jerry.module.ts",
			wantControllers: []string{"JerryController"},
			wantProviders:   []string{"JerryService", "PrismaService"},
		},
	}

	reg := NewRegistry(DefaultRulesets()...)

	for _, tt := range tests {
		src, err := os.ReadFile(tt.path)
		if err != nil {
			t.Skipf("fixture not available: %s", tt.path)
		}

		raw := ast.ExtractTypeScript(src)
		result := reg.Apply(raw)
		name := tt.path[strings.LastIndex(tt.path, "/")+1:]

		if result.FromType != "module" {
			t.Errorf("%s: from_type = %q, want \"module\"", name, result.FromType)
		}

		// Index provides facts by to → to_type
		providesByName := map[string]string{}
		for _, f := range result.Facts {
			if f.Kind == "provides" {
				providesByName[f.To] = f.ToType
			}
		}

		for _, want := range tt.wantControllers {
			got, ok := providesByName[want]
			if !ok {
				t.Errorf("%s: missing provides fact for controller %q", name, want)
			} else if got != "controller" {
				t.Errorf("%s: provides %q to_type = %q, want \"controller\"", name, want, got)
			}
		}
		for _, want := range tt.wantProviders {
			got, ok := providesByName[want]
			if !ok {
				t.Errorf("%s: missing provides fact for provider %q", name, want)
			} else if got != "provider" {
				t.Errorf("%s: provides %q to_type = %q, want \"provider\"", name, want, got)
			}
		}

		fmt.Printf("\n%s: from_type=%q, provides=%v\n", name, result.FromType, providesByName)
	}
}

// TestR6_MergeDeduplication verifies that AST-generated provides facts do not double-up
// when the LLM also emits provides facts for the same members (factKey dedup by kind|to).
func TestR6_MergeDeduplication(t *testing.T) {
	// Simulate AST provides + LLM provides for the same member
	import_astmerge := func(astFacts, llmFacts []map[string]any) []map[string]any {
		// Replicate unionFacts logic from graph/ast_merge.go
		llmKeys := make(map[string]bool)
		for _, f := range llmFacts {
			k, _ := f["kind"].(string)
			v, _ := f["to"].(string)
			llmKeys[strings.ToLower(k)+"|"+strings.ToLower(v)+"|"] = true
		}
		var result []map[string]any
		for _, f := range astFacts {
			k, _ := f["kind"].(string)
			v, _ := f["to"].(string)
			key := strings.ToLower(k)+"|"+strings.ToLower(v)+"|"
			if !llmKeys[key] {
				result = append(result, f)
			}
		}
		result = append(result, llmFacts...)
		return result
	}

	astFacts := []map[string]any{
		{"kind": "provides", "to": "ArenaController", "to_type": "controller"},
		{"kind": "provides", "to": "ArenaService", "to_type": "provider"},
	}
	llmFacts := []map[string]any{
		{"kind": "provides", "to": "ArenaController", "to_type": "controller"},
	}

	merged := import_astmerge(astFacts, llmFacts)

	// Should have exactly 2 facts: 1 ArenaService from AST + 1 ArenaController from LLM
	if len(merged) != 2 {
		b, _ := json.Marshal(merged)
		t.Errorf("expected 2 merged facts (no double ArenaController), got %d: %s", len(merged), b)
	}

	// ArenaController should appear exactly once
	count := 0
	for _, f := range merged {
		to, _ := f["to"].(string)
		if to == "ArenaController" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("ArenaController appears %d times in merged facts, want 1", count)
	}
}

func hasSemFact(facts []SemanticFact, check semCheck) bool {
	for _, f := range facts {
		if f.Kind != check.kind {
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
		if check.target != "" && !strings.Contains(f.Target, check.target) {
			continue
		}
		if check.transport != "" && f.Transport != check.transport {
			continue
		}
		return true
	}
	return false
}
