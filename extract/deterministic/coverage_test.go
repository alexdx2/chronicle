package deterministic

import "testing"

func kinds(cs []Candidate) map[string]int {
	m := map[string]int{}
	for _, c := range cs {
		m[c.Kind]++
	}
	return m
}

func hasTarget(cs []Candidate, kind, target string) bool {
	for _, c := range cs {
		if c.Kind == kind && (c.Target == target || c.To == target) {
			return true
		}
	}
	return false
}

func TestExtractCandidates_NonSourceFile(t *testing.T) {
	if got := ExtractCandidates("README.md", []byte("hello")); len(got) != 0 {
		t.Errorf("non-source file should yield no candidates: %+v", got)
	}
}

func TestExtractCandidates_CSProjFilenameFallback(t *testing.T) {
	// No AssemblyName → falls back to the csproj filename.
	cs := ExtractCandidates("svc/ScoreboardApi.csproj", []byte("<Project Sdk=\"Microsoft.NET.Sdk\"></Project>"))
	if !hasTarget(cs, "declares_service", "ScoreboardApi") {
		t.Errorf("expected declares_service from filename: %+v", cs)
	}
}

func TestExtractCandidates_NestModuleAndMessaging(t *testing.T) {
	src := `
@Module({ providers: [X] })
export class AppModule {}
await _producer.ProduceAsync("battle-events", msg);
consumer.Subscribe("score-updates");
@EventPattern('tom.weapon.equipped')
handleEquip() {}
`
	k := kinds(ExtractCandidates("app.module.ts", []byte(src)))
	if k["provides"] == 0 {
		t.Error("expected @Module → provides")
	}
	if k["produces"] == 0 {
		t.Error("expected ProduceAsync → produces")
	}
	if k["consumes"] < 2 {
		t.Errorf("expected Subscribe + @EventPattern → 2 consumes, got %d", k["consumes"])
	}
}

func TestExtractCandidates_EFCoreAndSignalR(t *testing.T) {
	src := `
public DbSet<BattleEvent> BattleEvents { get; set; }
await Clients.All.SendAsync("BattleUpdate", payload);
`
	cs := ExtractCandidates("Db.cs", []byte(src))
	k := kinds(cs)
	if k["model"] == 0 {
		t.Errorf("expected DbSet → model: %+v", cs)
	}
	if k["produces"] == 0 {
		t.Errorf("expected SendAsync → produces: %+v", cs)
	}
}

func TestExtractCandidates_AspNetRoutingAndDI(t *testing.T) {
	src := `
[Route("api/[controller]")]
public class ScoreController : ControllerBase {
  [HttpGet("leaderboard")]
  public IActionResult Get() => Ok();
}
app.MapGet("/health", () => "ok");
app.MapHub<BattleHub>("/battle-hub");
builder.Services.AddScoped<IScoreService, ScoreService>();
`
	cs := ExtractCandidates("ScoreController.cs", []byte(src))
	k := kinds(cs)
	if k["endpoint"] < 3 {
		t.Errorf("expected >=3 endpoints (HttpGet+MapGet+MapHub), got %d: %+v", k["endpoint"], cs)
	}
	// [controller] token resolves against the controller class → "score".
	if !hasTarget(cs, "endpoint", "/api/score/leaderboard") {
		t.Errorf("expected [controller] resolution to /api/score/leaderboard: %+v", cs)
	}
	// MapHub → WS endpoint.
	foundWS := false
	for _, c := range cs {
		if c.Kind == "endpoint" && c.Method == "WS" {
			foundWS = true
		}
	}
	if !foundWS {
		t.Errorf("expected MapHub WS endpoint: %+v", cs)
	}
}
