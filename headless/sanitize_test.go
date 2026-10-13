package headless

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSanitizeFacts(t *testing.T) {
	in := `[{"kind":"declares_service","to":"x"},{"kind":"dependency","to":"lodash"},
{"kind":"endpoint","method":"POST","target":"https://api.openai.com/v1/responses"},
{"kind":"calls_endpoint","method":"GET","target":"HTTP://host/x"},
{"kind":"endpoint","method":"GET","target":"/v1/lines"},
{"kind":"calls_service","method":"getDb","to":"getDb"},
{"kind":"calls_service","method":"getOdds","to":"OddsClient"},
{"kind":"model","to":"M"}]`
	got, dropped := sanitizeFacts("src/a.ts", nil, json.RawMessage(in))
	var fs []map[string]string
	if err := json.Unmarshal(got, &fs); err != nil {
		t.Fatal(err)
	}
	if dropped != 3 || len(fs) != 5 {
		t.Fatalf("dropped %d, kept %d: %s", dropped, len(fs), got)
	}
	if fs[0]["kind"] != "http_call" || fs[0]["method"] != "POST" || fs[1]["kind"] != "http_call" {
		t.Errorf("absolute URLs must become http_call: %v %v", fs[0], fs[1])
	}
	if fs[2]["kind"] != "endpoint" || fs[3]["to"] != "OddsClient" || fs[4]["kind"] != "model" {
		t.Errorf("legitimate facts changed: %v", fs)
	}

	inj := json.RawMessage(`[{"kind":"injects","to":"readForm"},{"kind":"injects","to":"PrismaService"}]`)
	if g, d := sanitizeFacts("src/a.ts", nil, inj); d != 1 || strings.Contains(string(g), "readForm") {
		t.Errorf("lowercase injects in TS is an imported function: dropped %d: %s", d, g)
	}
	if _, d := sanitizeFacts("app/deps.py", nil, inj); d != 0 {
		t.Errorf("the rule is for JS/TS only (python DI targets are functions), dropped %d", d)
	}

	// The deps pass reads these manifests: its dependency facts are the only
	// ones; the model's declares_service stays.
	for _, p := range []string{"package.json", "svc/go.mod", "Api/Api.csproj", "requirements-dev.txt", "a/pom.xml"} {
		g, d := sanitizeFacts(p, nil, json.RawMessage(`[{"kind":"declares_service","to":"x"},{"kind":"dependency","to":"y"},{"kind":"publishes_package","to":"z"}]`))
		if d != 2 || !strings.Contains(string(g), "declares_service") || strings.Contains(string(g), "dependency") || strings.Contains(string(g), "publishes_package") {
			t.Errorf("%s: deps pass owns dependency and publishes_package, dropped %d: %s", p, d, g)
		}
	}
	// A manifest the pass does not read keeps the model's dependency facts.
	if g, d := sanitizeFacts("Cargo.toml", nil, json.RawMessage(`[{"kind":"declares_service","to":"x"},{"kind":"dependency","to":"y"}]`)); d != 0 || !strings.Contains(string(g), "dependency") {
		t.Errorf("Cargo.toml is not read by the deps pass, dropped %d: %s", d, g)
	}
	// A source file may emit neither (today's rule).
	if g, d := sanitizeFacts("src/x.ts", nil, json.RawMessage(`[{"kind":"dependency","to":"y"}]`)); d != 1 || strings.Contains(string(g), "dependency") {
		t.Errorf("source file dependency must be dropped, dropped %d: %s", d, g)
	}

	same := json.RawMessage(`[ {"kind":"model","to":"M"} ]`)
	if g, d := sanitizeFacts("a.ts", nil, same); d != 0 || string(g) != string(same) {
		t.Errorf("untouched facts must keep their bytes: %s", g)
	}
	if g, d := sanitizeFacts("a.ts", nil, json.RawMessage(`not json`)); d != 0 || string(g) != "not json" {
		t.Errorf("garbage passes through: %s", g)
	}
}

func TestSanitizeFacts_TypesAreNotServicesOrModels(t *testing.T) {
	src := []byte("import type { Prisma, Row } from 'x'\nimport { getDb, type Fact, type Old as Renamed } from './db'\n" +
		"export interface StaleCallDeps { finalize: () => void }\nexport type LineState = 'a' | 'b'\n" +
		"export class VoiceService {}\n")
	facts := json.RawMessage(`[
{"kind":"calls_service","method":"finalize","to":"StaleCallDeps"},
{"kind":"injects","to":"StaleCallDeps"},
{"kind":"model","to":"LineState"},
{"kind":"uses_model","to":"Row"},
{"kind":"uses_model","to":"Fact"},
{"kind":"model_relation","from":"Renamed","to":"X"},
{"kind":"calls_service","method":"x","to":"VoiceService"},
{"kind":"uses_model","to":"Tenant"},
{"kind":"enum","to":"LineState"}]`)
	got, dropped := sanitizeFacts("src/a.ts", src, facts)
	var fs []map[string]string
	_ = json.Unmarshal(got, &fs)
	if dropped != 6 || len(fs) != 3 {
		t.Fatalf("dropped %d kept %d: %s", dropped, len(fs), got)
	}
	if fs[0]["to"] != "VoiceService" || fs[1]["to"] != "Tenant" || fs[2]["kind"] != "enum" {
		t.Errorf("kept the wrong facts: %v", fs)
	}
	// the same facts in a Go file are not touched: the rule is TS-only
	if _, d := sanitizeFacts("a.go", src, facts); d != 0 {
		t.Errorf("non-JS file dropped %d", d)
	}
}
