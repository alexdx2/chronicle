package manifest

import (
	"testing"
)

func TestLoadValid(t *testing.T) {
	m, err := LoadFile("../../testdata/manifest/valid.yaml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Domain != "orders" {
		t.Errorf("domain = %q, want %q", m.Domain, "orders")
	}
	if len(m.Repositories) != 2 {
		t.Errorf("repos = %d, want 2", len(m.Repositories))
	}
	if m.Repositories[0].Name != "orders-api" {
		t.Errorf("repo[0].name = %q, want %q", m.Repositories[0].Name, "orders-api")
	}
	if m.Owner != "checkout-team" {
		t.Errorf("owner = %q, want %q", m.Owner, "checkout-team")
	}
	tags := m.Repositories[0].Tags
	if len(tags) != 3 || tags[0] != "nestjs" {
		t.Errorf("repo[0].tags = %v, want [nestjs graphql kafka-producer]", tags)
	}
}

func TestLoadMissingDomain(t *testing.T) {
	_, err := LoadFile("../../testdata/manifest/invalid_no_domain.yaml")
	if err == nil {
		t.Fatal("expected error for missing domain, got nil")
	}
}

func TestLoadFileNotFound(t *testing.T) {
	_, err := LoadFile("nonexistent.yaml")
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}
