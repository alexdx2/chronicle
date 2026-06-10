package version

import (
	"testing"
)

func TestIdentity_FingerprintStable(t *testing.T) {
	id := Identity()
	if id.Fingerprint == "" {
		t.Fatal("fingerprint empty")
	}
	if len(id.Fingerprint) != 12 {
		t.Fatalf("fingerprint length want 12, got %d", len(id.Fingerprint))
	}
	if id.ReleaseCodename != ReleaseCodename {
		t.Fatalf("codename mismatch")
	}
	// Golden — bump intentionally when ReleaseCodename, SchemaGeneration, or Capabilities change.
	const golden = "1f8b19d6ea28"
	if id.Fingerprint != golden {
		t.Fatalf("fingerprint changed to %q — update golden if release contract changed", id.Fingerprint)
	}
}

func TestIdentity_ScanContract(t *testing.T) {
	id := Identity()
	for k, v := range id.ScanContract {
		if !v {
			t.Errorf("scan_contract[%s] = false", k)
		}
	}
}

func TestScanPreflightBlock_MentionsCodename(t *testing.T) {
	block := ScanPreflightBlock()
	if !contains(block, ReleaseCodename) {
		t.Fatalf("preflight block missing codename")
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
