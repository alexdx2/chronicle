package extract

import (
	"testing"

	"github.com/alexdx2/chronicle-core/truth"
)

func TestCsprojWorkerAndLegacyWeb(t *testing.T) {
	legacy := func(body string) string {
		return `<Project ToolsVersion="15.0" xmlns="http://schemas.microsoft.com/developer/msbuild/2003">
  <PropertyGroup>
    <OutputType>Library</OutputType>
    <AssemblyName>Shop.Web</AssemblyName>
  </PropertyGroup>
  <ItemGroup>
` + body + `
  </ItemGroup>
</Project>`
	}
	cases := []struct{ name, path, src, want string }{
		{"worker sdk", "w/Ledger.csproj", `<Project Sdk="Microsoft.NET.Sdk.Worker"><PropertyGroup><AssemblyName>LedgerWorker</AssemblyName></PropertyGroup></Project>`, "own:ledgerworker"},
		{"worker sdk stem", "w/Ledger.csproj", `<Project Sdk="Microsoft.NET.Sdk.Worker"></Project>`, "own:ledger"},
		{"worker commented", "w/Ledger.csproj", `<!-- <Project Sdk="Microsoft.NET.Sdk.Worker"> -->` + "\n<Project Sdk=\"Microsoft.NET.Sdk\"></Project>", ""},
		{"worker test project", "w/Ledger.csproj", `<Project Sdk="Microsoft.NET.Sdk.Worker"><ItemGroup><PackageReference Include="Microsoft.NET.Test.Sdk" Version="1"/></ItemGroup></Project>`, ""},
		{"legacy System.Web ref", "m/Shop.csproj", legacy(`<Reference Include="System.Web" />`), "own:shopweb"},
		{"legacy webapi ref", "m/Shop.csproj", legacy(`<Reference Include="System.Web.Http, Version=5.2.9.0">`), "own:shopweb"},
		{"legacy web guid", "m/Shop.csproj", `<Project ToolsVersion="4.0"><PropertyGroup><ProjectTypeGuids>{349c5851-65df-11da-9384-00065b846f21};{fae04ec0-301f-11d3-bf4b-00c04f79efbc}</ProjectTypeGuids></PropertyGroup></Project>`, "own:shop"},
		{"legacy plain library", "m/Lib.csproj", legacy(`<Reference Include="System.Xml" />`), ""},
		{"legacy test project", "m/Shop.Tests.csproj", legacy(`<Reference Include="System.Web" /><Reference Include="nunit.framework" />`), ""},
		{"legacy web ref commented", "m/Shop.csproj", legacy(`<!-- <Reference Include="System.Web" /> -->`), ""},
		{"sdk library with System.Web ref", "m/Lib.csproj", `<Project Sdk="Microsoft.NET.Sdk"><ItemGroup><Reference Include="System.Web" /></ItemGroup></Project>`, ""},
	}
	for _, c := range cases {
		if got := keysOf(t, map[string]string{c.path: c.src}, truth.ClassServices); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}

func TestPackagesConfig(t *testing.T) {
	cfg := `<?xml version="1.0" encoding="utf-8"?>
<packages>
  <package id="Newtonsoft.Json" version="13.0.3" targetFramework="net48" />
  <!-- <package id="Commented.Out" version="1.0" /> -->
  <package id="log4net" version="2.0.15" targetFramework="net48"></package>
  <package version="1.0" />
</packages>`
	cases := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{"basic", map[string]string{"app/packages.config": cfg}, "nuget:log4net,nuget:newtonsoft.json"},
		{"lock beside wins", map[string]string{"app/packages.config": cfg, "app/packages.lock.json": `{"version":1,"dependencies":{}}`}, ""},
		{"not a packages file", map[string]string{"app/packages.config": `<configuration><package id="X" version="1"/></configuration>`}, ""},
		{"other name ignored", map[string]string{"app/packages.config.bak": cfg}, ""},
	}
	for _, c := range cases {
		if got := keysOf(t, c.files, truth.ClassDeps); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}

func TestWebAPI2AttributeRouting(t *testing.T) {
	hdr := "using System.Web.Http;\nnamespace A {\n"
	cases := []struct{ name, src, want string }{
		{"prefix plus route and verbs", hdr + `[RoutePrefix("api/orders")]
public class OrdersController : ApiController
{
    [HttpGet]
    [Route("{id:int}")]
    public IHttpActionResult Get(int id) { return Ok(); }

    [HttpPost]
    [Route("")]
    public IHttpActionResult Create() { return Ok(); }

    [HttpPut, Route("{id:int}/lines/{line:guid}")]
    public IHttpActionResult Put(int id) { return Ok(); }

    [HttpDelete][Route("{id}")]
    public IHttpActionResult Remove(int id) { return Ok(); }
}}`, "DELETE /api/orders/{},GET /api/orders/{},POST /api/orders,PUT /api/orders/{}/lines/{}"},
		{"route without prefix", hdr + `public class PingController : ApiController
{
    [HttpGet]
    [Route("full/path/{x}")]
    public string Ping(string x) { return x; }
}}`, "GET /full/path/{}"},
		{"verb from method name", hdr + `[RoutePrefix("api/items")]
public class ItemsController : ApiController
{
    [Route("{id}")]
    public Item GetItem(int id) { return null; }

    [Route("")]
    public IHttpActionResult PostItem(Item i) { return Ok(); }

    [Route("all")]
    public IHttpActionResult Getaway() { return Ok(); }
}}`, "GET /api/items/{},POST /api/items"},
		{"verb attribute alone under prefix", hdr + `[RoutePrefix("api/health")]
public class HealthController : ApiController
{
    [HttpGet]
    public string Check() { return "ok"; }
}}`, "GET /api/health"},
		{"tilde route overrides prefix", hdr + `[RoutePrefix("api/a")]
public class AController : ApiController
{
    [HttpGet, Route("~/status")]
    public string S() { return ""; }
}}`, "GET /status"},
		{"commented route", hdr + `[RoutePrefix("api/a")]
public class AController : ApiController
{
    // [HttpDelete][Route("{id}")]
    /* [HttpPost] [Route("x")] */
    [HttpGet][Route("ok")]
    public string S() { return ""; }
}}`, "GET /api/a/ok"},
		{"route in a string", hdr + `[RoutePrefix("api/a")]
public class AController : ApiController
{
    public string Doc() { return "[HttpGet][Route(\"nope\")]"; }
}}`, ""},
		{"no routing attributes", hdr + `public class BController : ApiController
{
    public string GetAll() { return ""; }
}}`, ""},
		{"unknown base keeps relative route closed", hdr + `public class CController : MyBase
{
    [HttpGet][Route("x")]
    public string S() { return ""; }
}}`, ""},
	}
	for _, c := range cases {
		if got := keysOf(t, map[string]string{"Controllers/X.cs": c.src}, truth.ClassEndpoints); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
	// test files stay silent
	if got := keysOf(t, map[string]string{"Api.Tests/XTests.cs": cases[0].src}, truth.ClassEndpoints); got != "" {
		t.Errorf("test file: %q", got)
	}
}

func TestCSharpWebClientLiteral(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{"DownloadString", `var s = new WebClient().DownloadString("http://billing-api:8080/x/" + id);`, "billing-api"},
		{"UploadString async", `await wc.UploadStringTaskAsync("https://pay.example.com/charge", body);`, "pay.example.com"},
		{"GetStringAsync", `var s = await client.GetStringAsync("http://catalog:80/items");`, "catalog"},
		{"GetAsync", `var r = await client.GetAsync("http://host-a/x");`, "host-a"},
		{"WebRequest", `var r = WebRequest.Create("http://legacy-svc/api");`, "legacy-svc"},
		{"HttpRequestMessage", `var m = new HttpRequestMessage(HttpMethod.Get, "http://svc-b/ping");`, "svc-b"},
		{"new Uri", `wc.DownloadData(new Uri("http://svc-c/f"));`, "svc-c"},
		{"commented", "// wc.DownloadString(\"http://gone/x\");\n/* wc.DownloadString(\"http://gone2/x\"); */", ""},
		{"variable url", `wc.DownloadString(url);`, ""},
		{"config url", `wc.DownloadString(ConfigurationManager.AppSettings["BillingApiUrl"]);`, ""},
		{"localhost", `wc.DownloadString("http://localhost:5000/x");`, ""},
		{"log message", `Log.Info("fetching http://billing-api:8080/ via DownloadString");`, ""},
	}
	for _, c := range cases {
		if got := keysOf(t, map[string]string{"Svc/X.cs": c.src}, truth.ClassHTTP); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
	if got := keysOf(t, map[string]string{"Svc.Tests/XTests.cs": cases[0].src}, truth.ClassHTTP); got != "" {
		t.Errorf("test file: %q", got)
	}
}

func TestWebConfigAppSettingsURL(t *testing.T) {
	cfg := func(body string) string {
		return "<?xml version=\"1.0\"?>\n<configuration>\n  <appSettings>\n" + body + "\n  </appSettings>\n</configuration>"
	}
	cases := []struct{ name, path, src, want string }{
		{"url key", "Web.config", cfg(`<add key="BillingApiUrl" value="http://billing-api:8080/" />`), "billing-api"},
		{"any key, reversed attrs", "web.config", cfg(`<add value="https://crm.example.com/" key="Crm" />`), "crm.example.com"},
		{"transform file", "Web.Release.config", cfg(`<add key="PricingUri" value="http://pricing/" xdt:Transform="SetAttributes" />`), "pricing"},
		{"app.config", "src/App.config", cfg(`<add key="ApiEndpoint" value="http://api-x:81" />`), "api-x"},
		{"sqs queue url is a channel", "Web.config", cfg(`<add key="OrdersQueueUrl" value="https://sqs.eu-central-1.amazonaws.com/123456789012/orders-inbound" />`), ""},
		{"not a url", "Web.config", cfg(`<add key="AWSRegion" value="eu-central-1" /><add key="Name" value="x" />`), ""},
		{"commented", "Web.config", cfg(`<!-- <add key="OldUrl" value="http://old-svc/" /> -->`), ""},
		{"outside appSettings", "Web.config", `<configuration><connectionStrings><add name="db" key="DbUrl" value="http://db-svc/" /></connectionStrings></configuration>`, ""},
		{"localhost", "Web.config", cfg(`<add key="SelfUrl" value="http://localhost:5000/" />`), ""},
		{"schema key", "Web.config", cfg(`<add key="DocsUrl" value="https://docs.example.com/" />`), ""},
		{"other config ignored", "nlog.config", cfg(`<add key="ApiUrl" value="http://x-svc/" />`), ""},
		{"test dir", "Api.Tests/App.config", cfg(`<add key="ApiUrl" value="http://x-svc/" />`), ""},
	}
	for _, c := range cases {
		if got := keysOf(t, map[string]string{c.path: c.src}, truth.ClassHTTP); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}
