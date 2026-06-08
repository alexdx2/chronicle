package mcp

import "testing"

func TestParseOutboxArtifact_FactsArray(t *testing.T) {
	data := []byte(`{
		"file_path": "a.ts",
		"status": "extracted",
		"facts": [{"kind":"import","to":"x"}],
		"obligation_id": 1,
		"revision_id": 1,
		"domain": "d"
	}`)
	ext, err := parseOutboxArtifact(data)
	if err != nil {
		t.Fatal(err)
	}
	facts, err := factsJSONString(ext.Facts)
	if err != nil {
		t.Fatal(err)
	}
	if facts != `[{"kind":"import","to":"x"}]` {
		t.Fatalf("facts: got %q", facts)
	}
}

func TestParseOutboxArtifact_FactsString(t *testing.T) {
	data := []byte(`{
		"file_path": "a.ts",
		"status": "extracted",
		"facts": "[{\"kind\":\"import\",\"to\":\"x\"}]"
	}`)
	ext, err := parseOutboxArtifact(data)
	if err != nil {
		t.Fatal(err)
	}
	facts, err := factsJSONString(ext.Facts)
	if err != nil {
		t.Fatal(err)
	}
	if facts != `[{"kind":"import","to":"x"}]` {
		t.Fatalf("facts: got %q", facts)
	}
}
