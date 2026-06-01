// graph/quality_test.go
package graph

import (
	"strings"
	"testing"
)

func TestCheckServiceNodes(t *testing.T) {
	warnings := CheckServiceNodes(
		[]string{"arena-api", "tom-api", "jerry-api"},
		[]string{"service:service:d:arena-api", "service:service:d:tom-api"},
	)
	if len(warnings) != 1 {
		t.Errorf("want 1 warning for missing jerry-api, got %d", len(warnings))
	}
	if len(warnings) > 0 && !strings.Contains(warnings[0].Message, "jerry-api") {
		t.Errorf("warning should mention jerry-api, got: %s", warnings[0].Message)
	}
}

func TestCheckServiceNodesAllPresent(t *testing.T) {
	warnings := CheckServiceNodes(
		[]string{"arena-api", "tom-api"},
		[]string{"service:service:d:arena-api", "service:service:d:tom-api"},
	)
	if len(warnings) != 0 {
		t.Errorf("want 0 warnings when all services present, got %d", len(warnings))
	}
}

func TestCheckDataModels(t *testing.T) {
	w1 := CheckDataModels(true, 0)
	if len(w1) != 1 {
		t.Errorf("want 1 warning when prisma files but no models, got %d", len(w1))
	}

	w2 := CheckDataModels(true, 5)
	if len(w2) != 0 {
		t.Errorf("want 0 warnings when models exist, got %d", len(w2))
	}

	w3 := CheckDataModels(false, 0)
	if len(w3) != 0 {
		t.Errorf("want 0 warnings when no prisma files, got %d", len(w3))
	}
}

func TestCheckNoiseRatio(t *testing.T) {
	w1 := CheckNoiseRatio(10, 6)
	if len(w1) != 1 {
		t.Errorf("want 1 warning when 6/10 are support, got %d", len(w1))
	}

	w2 := CheckNoiseRatio(10, 3)
	if len(w2) != 0 {
		t.Errorf("want 0 warnings when 3/10 are support, got %d", len(w2))
	}
}

func TestCheckEndpointPrefixes(t *testing.T) {
	endpoints := []EndpointInfo{
		{NodeKey: "ep1", Path: "/arena/attack", ControllerPrefix: "arena"}, // OK
		{NodeKey: "ep2", Path: "/health", ControllerPrefix: ""},            // OK — no prefix
		{NodeKey: "ep3", Path: "/attack", ControllerPrefix: "arena"},       // BUG
	}
	warnings := CheckEndpointPrefixes(endpoints)
	if len(warnings) != 1 {
		t.Errorf("want 1 warning for /attack with prefix arena, got %d", len(warnings))
	}
	if len(warnings) > 0 && warnings[0].NodeKey != "ep3" {
		t.Errorf("warning should be for ep3, got %s", warnings[0].NodeKey)
	}
}
