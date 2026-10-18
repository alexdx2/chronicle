package extract

import (
	"reflect"
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/truth"
)

func TestMergeUnion(t *testing.T) {
	a := truth.Fact{Class: truth.ClassEndpoints, Key: "GET /x", Primary: truth.Evidence{File: "spec.yaml", Line: 3}, Sources: []string{"openapi.paths"}}
	b := truth.Fact{Class: truth.ClassEndpoints, Key: "GET /x", Primary: truth.Evidence{File: "C.cs", Line: 9}, Sources: []string{"csharp.route_attr"}}
	out, warns := Merge([]truth.Fact{a, b})
	if len(out) != 1 || len(warns) != 0 {
		t.Fatalf("out=%v warns=%v", out, warns)
	}
	f := out[0]
	if !reflect.DeepEqual(f.Sources, []string{"csharp.route_attr", "openapi.paths"}) {
		t.Fatalf("sources %v", f.Sources)
	}
	if f.Primary.File != "C.cs" {
		t.Fatalf("primary %v", f.Primary)
	}
	if len(f.Derived) != 1 || f.Derived[0].File != "spec.yaml" {
		t.Fatalf("derived %v", f.Derived)
	}
}

func TestMergeConflictKeepsPriority(t *testing.T) {
	lo := truth.Fact{Class: truth.ClassDeps, Key: "k", Attrs: map[string]string{"method": "1"}, Sources: []string{"dotenv.x"}}
	hi := truth.Fact{Class: truth.ClassDeps, Key: "k", Attrs: map[string]string{"method": "2", "w": "z"}, Sources: []string{"lock.npm"}}
	out, warns := Merge([]truth.Fact{lo, hi})
	if out[0].Attrs["method"] != "2" || out[0].Attrs["w"] != "z" {
		t.Fatalf("attrs %v", out[0].Attrs)
	}
	if len(warns) != 1 || warns[0] != "conflict deps k method: 2 vs 1 (kept 2)" {
		t.Fatalf("warns %v", warns)
	}
}

func TestMergeOrderAndPriority(t *testing.T) {
	in := []truth.Fact{
		{Class: truth.ClassServices, Key: "a"},
		{Class: truth.ClassEndpoints, Key: "z"},
		{Class: truth.ClassEndpoints, Key: "b"},
	}
	out, _ := Merge(in)
	var ks []string
	for _, f := range out {
		ks = append(ks, string(f.Class)+":"+f.Key)
	}
	if strings.Join(ks, ",") != "endpoints:b,endpoints:z,services:a" {
		t.Fatalf("%v", ks)
	}
	if Priority("kafkajs") != 3 || Priority("csharp.x") != 3 || Priority("openapi.p") != 2 ||
		Priority("compose.s") != 1 || Priority("dotenv.e") != 0 || Priority("weird.x") != 0 {
		t.Fatal("priority table")
	}
}

func TestPriorityTable(t *testing.T) {
	for s, want := range map[string]int{"csharp.route_attr": 3, "openapi.paths": 2, "compose.service": 1,
		"appsettings.url": 0, "kafkajs": 3, "kafkajs.consumer": 3, "unknown.rule": 0} {
		if got := Priority(s); got != want {
			t.Errorf("%s: got %d want %d", s, got, want)
		}
	}
}

func TestMergeZeroPrimaryNotDerived(t *testing.T) {
	a := truth.Fact{Class: truth.ClassDeps, Key: "k", Primary: truth.Evidence{File: "a", Line: 1}, Sources: []string{"lock.x"}}
	b := truth.Fact{Class: truth.ClassDeps, Key: "k", Sources: []string{"dotenv.x"}}
	out, _ := Merge([]truth.Fact{a, b})
	if len(out[0].Derived) != 0 {
		t.Fatalf("derived %v", out[0].Derived)
	}
}

func TestMergePerEvidenceAttrNoWarning(t *testing.T) {
	a := truth.Fact{Class: truth.ClassDeps, Key: "k", Attrs: map[string]string{"path": "a/x.csproj", "version": "1"}, Sources: []string{"lock.npm"}}
	b := truth.Fact{Class: truth.ClassDeps, Key: "k", Attrs: map[string]string{"path": "b/x.csproj", "version": "2"}, Sources: []string{"manifest.npm"}}
	if _, warns := Merge([]truth.Fact{a, b}); len(warns) != 0 {
		t.Fatalf("warns %v", warns)
	}
}

func TestMergeWarningsDeduped(t *testing.T) {
	mk := func(src, m string) truth.Fact {
		return truth.Fact{Class: truth.ClassEndpoints, Key: "k", Attrs: map[string]string{"method": m}, Sources: []string{src}}
	}
	_, warns := Merge([]truth.Fact{mk("csharp.a", "GET"), mk("openapi.b", "POST"), mk("openapi.c", "POST")})
	if len(warns) != 1 {
		t.Fatalf("warns %v", warns)
	}
}
