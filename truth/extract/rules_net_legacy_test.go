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
