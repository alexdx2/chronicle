package wiring

import (
	"sort"
	"testing"
)

type fakeAdapter struct{ id string }

func (f *fakeAdapter) ID() string          { return f.id }
func (f *fakeAdapter) DisplayName() string { return "Fake " + f.id }
func (f *fakeAdapter) Detect() Detection {
	return Detection{Confidence: DetectionConfirmed, Evidence: []string{"test"}}
}
func (f *fakeAdapter) Plan() (*Plan, error)   { return &Plan{Agent: f.id}, nil }
func (f *fakeAdapter) Verify() Verification   { return Verification{Level: VerifyNone, Health: HealthNotInstalled} }
func (f *fakeAdapter) Remove() (*Plan, error) { return &Plan{Agent: f.id}, nil }

func TestRegistryRegisterLookupSorted(t *testing.T) {
	resetAdapters() // test hook
	RegisterAdapter(&fakeAdapter{id: "zeta"})
	RegisterAdapter(&fakeAdapter{id: "alpha"})
	if _, ok := AdapterByID("zeta"); !ok {
		t.Fatal("zeta not found")
	}
	if _, ok := AdapterByID("nope"); ok {
		t.Fatal("nope should not resolve")
	}
	ids := AdapterIDs()
	if !sort.StringsAreSorted(ids) || len(ids) != 2 {
		t.Fatalf("ids = %v", ids)
	}
}
