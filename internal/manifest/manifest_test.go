package manifest

import (
	"testing"
)

func TestLoadSingleDomain(t *testing.T) {
	m, err := LoadFile("../../testdata/manifest/valid.yaml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(m.Domains) != 1 {
		t.Fatalf("domains count = %d, want 1", len(m.Domains))
	}
	d := m.Domains[0]
	if d.Name != "tomandjerry" {
		t.Errorf("domain name = %q, want %q", d.Name, "tomandjerry")
	}
	if d.Description != "Tom and Jerry battle simulation" {
		t.Errorf("domain description = %q, want %q", d.Description, "Tom and Jerry battle simulation")
	}
	if d.Owner != "cartoon-team" {
		t.Errorf("domain owner = %q, want %q", d.Owner, "cartoon-team")
	}
	if len(d.Scan.Include) != 5 {
		t.Errorf("scan.include count = %d, want 5", len(d.Scan.Include))
	}
	if len(d.Scan.Exclude) != 2 {
		t.Errorf("scan.exclude count = %d, want 2", len(d.Scan.Exclude))
	}
	if len(m.Tech) != 4 {
		t.Errorf("tech count = %d, want 4", len(m.Tech))
	}
	if m.Tech[0] != "nestjs" {
		t.Errorf("tech[0] = %q, want %q", m.Tech[0], "nestjs")
	}
}

func TestLoadMultiDomain(t *testing.T) {
	m, err := LoadFile("../../testdata/manifest/multi_domain.yaml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(m.Domains) != 3 {
		t.Fatalf("domains count = %d, want 3", len(m.Domains))
	}

	expected := []struct {
		name         string
		includeCount int
	}{
		{"mobile-app", 2},
		{"core-api", 3},
		{"crm", 2},
	}
	for i, exp := range expected {
		d := m.Domains[i]
		if d.Name != exp.name {
			t.Errorf("domains[%d].name = %q, want %q", i, d.Name, exp.name)
		}
		if len(d.Scan.Include) != exp.includeCount {
			t.Errorf("domains[%d].scan.include count = %d, want %d", i, len(d.Scan.Include), exp.includeCount)
		}
	}

	if len(m.Tech) != 2 {
		t.Errorf("tech count = %d, want 2", len(m.Tech))
	}
	if len(m.InstructionPacks) != 1 || m.InstructionPacks[0] != "framework/nestjs" {
		t.Errorf("instruction_packs = %v, want [framework/nestjs]", m.InstructionPacks)
	}
}

func TestLoadMissingDomains(t *testing.T) {
	_, err := LoadFile("../../testdata/manifest/invalid_no_domains.yaml")
	if err == nil {
		t.Fatal("expected error for missing domains, got nil")
	}
}

func TestLoadFileNotFound(t *testing.T) {
	_, err := LoadFile("nonexistent.yaml")
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

func TestMergedScanConfig(t *testing.T) {
	m, err := LoadFile("../../testdata/manifest/multi_domain.yaml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	merged := m.MergedScanConfig()
	// mobile-app has 2 includes, core-api has 3, crm has 2 = total 7
	if len(merged.Include) != 7 {
		t.Errorf("merged include = %d, want 7", len(merged.Include))
	}
}

func TestDomainForFile(t *testing.T) {
	m, err := LoadFile("../../testdata/manifest/multi_domain.yaml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	tests := []struct {
		file   string
		domain string
	}{
		{"mobile-gateway/src/app.ts", "mobile-app"},
		{"push-service/src/push.ts", "mobile-app"},
		{"orders-api/src/orders.ts", "core-api"},
		{"crm-api/src/crm.ts", "crm"},
		{"random/file.ts", "_unassigned"},
	}

	for _, tc := range tests {
		got := m.DomainForFile(tc.file)
		if got != tc.domain {
			t.Errorf("DomainForFile(%q) = %q, want %q", tc.file, got, tc.domain)
		}
	}
}

func TestInfrastructure(t *testing.T) {
	m, err := LoadFile("../../testdata/manifest/multi_domain.yaml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(m.Infrastructure) != 2 {
		t.Fatalf("infrastructure = %d, want 2", len(m.Infrastructure))
	}

	kafka := m.Infrastructure[0]
	if kafka.Name != "events-kafka" {
		t.Errorf("infra[0].name = %q, want %q", kafka.Name, "events-kafka")
	}
	if kafka.Type != "broker" {
		t.Errorf("infra[0].type = %q, want %q", kafka.Type, "broker")
	}
	if kafka.Address != "kafka-events.internal:9092" {
		t.Errorf("infra[0].address = %q, want %q", kafka.Address, "kafka-events.internal:9092")
	}
	if kafka.InfraNodeKey() != "infra:broker:kafka-events.internal:9092" {
		t.Errorf("infra[0].InfraNodeKey() = %q, want %q", kafka.InfraNodeKey(), "infra:broker:kafka-events.internal:9092")
	}

	redis := m.Infrastructure[1]
	if redis.InfraNodeKey() != "infra:cache:redis.internal:6379" {
		t.Errorf("infra[1].InfraNodeKey() = %q, want %q", redis.InfraNodeKey(), "infra:cache:redis.internal:6379")
	}
}

func TestInfraNodeKeyNoAddress(t *testing.T) {
	e := InfraEntry{Name: "my-kafka", Type: "broker"}
	if e.InfraNodeKey() != "infra:broker:my-kafka" {
		t.Errorf("InfraNodeKey() = %q, want %q", e.InfraNodeKey(), "infra:broker:my-kafka")
	}
}
