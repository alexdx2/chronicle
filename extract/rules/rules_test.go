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
