package graph

import (
	"encoding/json"
	"testing"
)

// --- TestVoteConfidence_HardFact ---

func TestVoteConfidence_HardFact(t *testing.T) {
	hardFact := map[string]any{"kind": "model", "to": "User"}
	totalVotes := 3

	cases := []struct {
		name       string
		count      int
		wantConf   float64
		wantAccept bool
	}{
		{"1/3 votes", 1, 0.60, true},
		{"2/3 votes", 2, 0.80, true},
		{"3/3 votes", 3, 0.95, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			conf := voteConfidence(tc.count, totalVotes, hardFact)
			if conf != tc.wantConf {
				t.Errorf("voteConfidence(%d, %d, hard) = %.2f, want %.2f", tc.count, totalVotes, conf, tc.wantConf)
			}
			if (conf > 0) != tc.wantAccept {
				t.Errorf("accepted = %v, want %v", conf > 0, tc.wantAccept)
			}
		})
	}
}

// --- TestVoteConfidence_InferredFact ---

func TestVoteConfidence_InferredFact(t *testing.T) {
	inferredFact := map[string]any{"kind": "calls_service", "to": "OrderService"}
	totalVotes := 3

	cases := []struct {
		name       string
		count      int
		wantConf   float64
		wantAccept bool
	}{
		{"1/3 votes — dropped", 1, 0, false},
		{"2/3 votes", 2, 0.70, true},
		{"3/3 votes", 3, 0.85, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			conf := voteConfidence(tc.count, totalVotes, inferredFact)
			if conf != tc.wantConf {
				t.Errorf("voteConfidence(%d, %d, inferred) = %.2f, want %.2f", tc.count, totalVotes, conf, tc.wantConf)
			}
			if (conf > 0) != tc.wantAccept {
				t.Errorf("accepted = %v, want %v", conf > 0, tc.wantAccept)
			}
		})
	}
}

// --- TestIsHardFact_Classification ---

func TestIsHardFact_Classification(t *testing.T) {
	hardKinds := []string{
		"model", "enum", "endpoint", "provides", "injects",
		"declares_service", "produces", "consumes", "decorator", "import",
	}
	for _, kind := range hardKinds {
		t.Run("hard_"+kind, func(t *testing.T) {
			fact := map[string]any{"kind": kind, "to": "X"}
			if !isHardFact(fact) {
				t.Errorf("isHardFact(%q) = false, want true", kind)
			}
		})
	}

	inferredKinds := []string{
		"calls_service", "http_call", "calls_endpoint", "uses_model",
	}
	for _, kind := range inferredKinds {
		t.Run("inferred_"+kind, func(t *testing.T) {
			fact := map[string]any{"kind": kind, "to": "X"}
			if isHardFact(fact) {
				t.Errorf("isHardFact(%q) = true, want false", kind)
			}
		})
	}
}

// --- TestNormalizeFacts_EnrichedFormat ---

func TestNormalizeFacts_EnrichedFormat(t *testing.T) {
	input := `{"candidate_decisions":[{"candidate_id":"c1","decision":"accept","fact":{"kind":"injects","to":"OrderService"}}],"additional_facts":[{"kind":"model","to":"User"}]}`

	result := normalizeFacts(input)

	var facts []map[string]any
	if err := json.Unmarshal([]byte(result), &facts); err != nil {
		t.Fatalf("unmarshal result: %v\nraw: %s", err, result)
	}

	if len(facts) != 2 {
		t.Fatalf("len(facts) = %d, want 2\nraw: %s", len(facts), result)
	}

	// First fact: unwrapped from candidate_decisions
	kind0, _ := facts[0]["kind"].(string)
	to0, _ := facts[0]["to"].(string)
	if kind0 != "injects" || to0 != "OrderService" {
		t.Errorf("fact[0] = kind=%q to=%q, want injects/OrderService", kind0, to0)
	}

	// Second fact: from additional_facts
	kind1, _ := facts[1]["kind"].(string)
	to1, _ := facts[1]["to"].(string)
	if kind1 != "model" || to1 != "User" {
		t.Errorf("fact[1] = kind=%q to=%q, want model/User", kind1, to1)
	}
}

// --- TestNormalizeFacts_FieldAliases ---

func TestNormalizeFacts_FieldAliases(t *testing.T) {
	t.Run("url mapped to target", func(t *testing.T) {
		input := `[{"kind":"http_call","url":"http://example.com/api"}]`
		result := normalizeFacts(input)
		var facts []map[string]any
		json.Unmarshal([]byte(result), &facts)
		if len(facts) != 1 {
			t.Fatalf("len = %d", len(facts))
		}
		if facts[0]["target"] != "http://example.com/api" {
			t.Errorf("target = %v, want http://example.com/api", facts[0]["target"])
		}
		if _, has := facts[0]["url"]; has {
			t.Error("url field should be removed after normalization")
		}
	})

	t.Run("service_call kind mapped to calls_service", func(t *testing.T) {
		input := `[{"kind":"service_call","to":"OrderService"}]`
		result := normalizeFacts(input)
		var facts []map[string]any
		json.Unmarshal([]byte(result), &facts)
		if len(facts) != 1 {
			t.Fatalf("len = %d", len(facts))
		}
		if facts[0]["kind"] != "calls_service" {
			t.Errorf("kind = %v, want calls_service", facts[0]["kind"])
		}
	})

	t.Run("module_controller kind dropped", func(t *testing.T) {
		input := `[{"kind":"module_controller","to":"SomeController"},{"kind":"model","to":"User"}]`
		result := normalizeFacts(input)
		var facts []map[string]any
		json.Unmarshal([]byte(result), &facts)
		if len(facts) != 1 {
			t.Fatalf("len = %d, want 1 (module_controller should be dropped)", len(facts))
		}
		if facts[0]["kind"] != "model" {
			t.Errorf("remaining fact kind = %v, want model", facts[0]["kind"])
		}
	})

	t.Run("from alias mapped to to for import", func(t *testing.T) {
		input := `[{"kind":"import","from":"./services/order"}]`
		result := normalizeFacts(input)
		var facts []map[string]any
		json.Unmarshal([]byte(result), &facts)
		if len(facts) != 1 {
			t.Fatalf("len = %d", len(facts))
		}
		if facts[0]["to"] != "./services/order" {
			t.Errorf("to = %v, want ./services/order", facts[0]["to"])
		}
	})

	t.Run("queue alias mapped to to for produces", func(t *testing.T) {
		input := `[{"kind":"produces","queue":"order.created"}]`
		result := normalizeFacts(input)
		var facts []map[string]any
		json.Unmarshal([]byte(result), &facts)
		if len(facts) != 1 {
			t.Fatalf("len = %d", len(facts))
		}
		if facts[0]["to"] != "order.created" {
			t.Errorf("to = %v, want order.created", facts[0]["to"])
		}
	})
}
