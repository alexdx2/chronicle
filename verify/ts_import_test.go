package verify

import (
	"encoding/json"
	"testing"
)

func TestTSImportVerifier_SubpathImport(t *testing.T) {
	content := []byte(`import { createOidcClient } from '@okeep/auth-client/server'` + "\n")
	assertion := json.RawMessage(`{"module":"@okeep/auth-client"}`)
	v := &TSImportVerifier{}
	res, err := v.Verify(content, assertion, nil)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if res.Status != "valid" {
		t.Fatalf("subpath import must satisfy package assertion; got status %q reason %q", res.Status, res.Reason)
	}
}

func TestTSImportVerifier_DifferentPackageStillMissing(t *testing.T) {
	// "@okeep/auth-client2" must NOT match assertion "@okeep/auth-client".
	content := []byte(`import { x } from '@okeep/auth-client2'` + "\n")
	assertion := json.RawMessage(`{"module":"@okeep/auth-client"}`)
	v := &TSImportVerifier{}
	res, _ := v.Verify(content, assertion, nil)
	if res.Status != "missing" {
		t.Fatalf("prefix without '/' boundary must not match; got %q", res.Status)
	}
}
