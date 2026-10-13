package verify

import (
	"encoding/json"
	"testing"
)

func TestManifestLine_ValidMovedMissing(t *testing.T) {
	v := ManifestLineVerifier{}
	if v.Kind() != "manifest_line" {
		t.Fatal(v.Kind())
	}
	body := []byte("a\n<PackageReference Include=\"Confluent.Kafka\" Version=\"2.3.0\" />\nc\n")
	a := json.RawMessage(`{"line":2,"text":"PackageReference Include=\"Confluent.Kafka\""}`)
	r, err := v.Verify(body, a, nil)
	if err != nil || r.Status != "valid" || r.ChangeType != "" || r.Confidence != 1.0 || r.NewLocator == nil || r.NewLocator.LineStart != 2 {
		t.Fatalf("verified: %+v %v", r, err)
	}
	crlf := []byte("a\r\n<PackageReference Include=\"Confluent.Kafka\" Version=\"2.3.0\" />\r\nc\r\n")
	r, err = v.Verify(crlf, a, nil)
	if err != nil || r.Status != "valid" || r.ChangeType != "" || r.NewLocator.LineStart != 2 {
		t.Fatalf("crlf: %+v %v", r, err)
	}
	moved := []byte("x\ny\n<PackageReference Include=\"Confluent.Kafka\" Version=\"2.4.0\" />\n")
	r, _ = v.Verify(moved, a, nil)
	if r.Status != "valid" || r.ChangeType != "moved" || r.NewLocator.LineStart != 3 {
		t.Fatalf("moved: %+v", r)
	}
	r, _ = v.Verify([]byte("nothing\n"), a, nil)
	if r.Status != "missing" || r.SuggestedAction != "mark_stale" {
		t.Fatalf("broken: %+v", r)
	}
	if _, err := v.Verify(body, json.RawMessage(`{"line":0}`), nil); err == nil {
		t.Fatal("empty text must be rejected")
	}
}

func TestDefaultRegistry_HasManifestLine(t *testing.T) {
	if DefaultRegistry().Get("manifest_line") == nil {
		t.Fatal("manifest_line not registered")
	}
}
