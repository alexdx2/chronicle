package manifest

import "testing"

func TestReplaceDomainsWithClone(t *testing.T) {
	m := &Manifest{Domains: []DomainEntry{
		{Key: "tom-and-jerry", Name: "TJ", Scan: ScanConfig{Include: []string{"a/**"}, Exclude: []string{"b/**"}}},
		{Key: "other", Name: "O", Scan: ScanConfig{Include: []string{"c/**"}}},
	}}
	ok := m.ReplaceDomainsWithClone("tom-and-jerry", "tom-and-jerry__lab1")
	if !ok {
		t.Fatal("expected clone to succeed")
	}
	if len(m.Domains) != 1 || m.Domains[0].Key != "tom-and-jerry__lab1" {
		t.Fatalf("domains after clone: %+v", m.Domains)
	}
	if m.Domains[0].Scan.Include[0] != "a/**" || m.Domains[0].Scan.Exclude[0] != "b/**" {
		t.Errorf("scan config not inherited: %+v", m.Domains[0].Scan)
	}
	if m.ReplaceDomainsWithClone("missing", "x") {
		t.Error("expected false for unknown base domain")
	}
}
