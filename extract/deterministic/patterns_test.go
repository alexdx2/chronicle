package deterministic

import "testing"

func TestExtractCandidates_CS_HttpAndKafka(t *testing.T) {
	src := `
[ApiController]
[Route("api/[controller]")]
public class BattleController : ControllerBase {
    [HttpGet("{id}")]
    public IActionResult Get(int id) => Ok();
}
`
	cands := ExtractCandidates("src/BattleController.cs", []byte(src))
	if len(cands) < 1 {
		t.Fatalf("expected endpoint candidate, got %d", len(cands))
	}
	found := false
	for _, c := range cands {
		if c.Kind == "endpoint" && c.Method == "GET" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected GET endpoint, got %+v", cands)
	}

	kafka := `await producer.ProduceAsync("battle-results", msg);
consumer.Subscribe("battle-results");`
	kc := ExtractCandidates("src/Consumer.cs", []byte(kafka))
	var produce, consume bool
	for _, c := range kc {
		if c.Kind == "produces" && c.To == "battle-results" {
			produce = true
		}
		if c.Kind == "consumes" && c.To == "battle-results" {
			consume = true
		}
	}
	if !produce || !consume {
		t.Fatalf("expected produce+consume, got %+v", kc)
	}
}

func TestExtractCandidates_CSProj(t *testing.T) {
	src := `<Project><AssemblyName>TomApi</AssemblyName></Project>`
	cands := ExtractCandidates("Tom.Api.csproj", []byte(src))
	if len(cands) != 1 || cands[0].Kind != "declares_service" || cands[0].To != "TomApi" {
		t.Fatalf("unexpected: %+v", cands)
	}
}
