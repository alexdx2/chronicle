package verify

import (
	"encoding/json"
	"testing"
)

func TestManifestVerifier_Found(t *testing.T) {
	content := []byte(`{
  "name": "my-app",
  "dependencies": {
    "@otopoint/pricing-engine": "workspace:*",
    "express": "^4.18.0"
  },
  "devDependencies": {
    "typescript": "^5.0.0"
  }
}`)

	v := &ManifestVerifier{}
	assertion := json.RawMessage(`{"package": "@otopoint/pricing-engine", "sections": ["dependencies", "devDependencies"]}`)

	result, err := v.Verify(content, assertion, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "valid" {
		t.Errorf("expected valid, got %s: %s", result.Status, result.Reason)
	}
	if result.Confidence < 0.90 {
		t.Errorf("expected confidence >= 0.90, got %f", result.Confidence)
	}
	if result.NewLocator == nil {
		t.Error("expected locator to be set")
	} else if result.NewLocator.LineStart != 4 {
		t.Errorf("expected line 4, got %d", result.NewLocator.LineStart)
	}
}

func TestManifestVerifier_Missing(t *testing.T) {
	content := []byte(`{
  "name": "my-app",
  "dependencies": {
    "express": "^4.18.0"
  }
}`)

	v := &ManifestVerifier{}
	assertion := json.RawMessage(`{"package": "@otopoint/pricing-engine", "sections": ["dependencies", "devDependencies"]}`)

	result, err := v.Verify(content, assertion, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "missing" {
		t.Errorf("expected missing, got %s", result.Status)
	}
}

func TestManifestVerifier_ScopeChanged(t *testing.T) {
	content := []byte(`{
  "name": "my-app",
  "dependencies": {},
  "devDependencies": {
    "@otopoint/pricing-engine": "workspace:*"
  }
}`)

	v := &ManifestVerifier{}
	assertion := json.RawMessage(`{"package": "@otopoint/pricing-engine", "sections": ["dependencies", "devDependencies"]}`)
	oldLocator := &Locator{LineStart: 4, JSONPath: "/dependencies/@otopoint~1pricing-engine"}

	result, err := v.Verify(content, assertion, oldLocator)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "valid" {
		t.Errorf("expected valid, got %s: %s", result.Status, result.Reason)
	}
	if result.ChangeType != "scope_changed" {
		t.Errorf("expected scope_changed, got %q", result.ChangeType)
	}
}

func TestManifestVerifier_VersionChanged(t *testing.T) {
	content := []byte(`{
  "dependencies": {
    "@otopoint/pricing-engine": "^2.0.0"
  }
}`)

	v := &ManifestVerifier{}
	assertion := json.RawMessage(`{"package": "@otopoint/pricing-engine", "sections": ["dependencies"], "expected_version": "workspace:*"}`)

	result, err := v.Verify(content, assertion, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "valid" {
		t.Errorf("expected valid, got %s", result.Status)
	}
	if result.ChangeType != "value_changed" {
		t.Errorf("expected value_changed, got %q", result.ChangeType)
	}
}

func TestGoModVerifier_Found(t *testing.T) {
	content := []byte(`module github.com/myorg/myapp

go 1.21

require (
	github.com/foo/bar v1.2.3
	github.com/baz/qux v0.5.0 // indirect
)
`)

	v := &GoModVerifier{}
	assertion := json.RawMessage(`{"module": "github.com/foo/bar", "version": "v1.2.3"}`)

	result, err := v.Verify(content, assertion, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "valid" {
		t.Errorf("expected valid, got %s: %s", result.Status, result.Reason)
	}
	if result.NewLocator == nil || result.NewLocator.LineStart != 6 {
		t.Errorf("expected line 6, got %v", result.NewLocator)
	}
}

func TestGoModVerifier_VersionChanged(t *testing.T) {
	content := []byte(`module github.com/myorg/myapp

go 1.21

require github.com/foo/bar v2.0.0
`)

	v := &GoModVerifier{}
	assertion := json.RawMessage(`{"module": "github.com/foo/bar", "version": "v1.2.3"}`)

	result, err := v.Verify(content, assertion, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "valid" {
		t.Errorf("expected valid, got %s", result.Status)
	}
	if result.ChangeType != "value_changed" {
		t.Errorf("expected value_changed, got %q", result.ChangeType)
	}
}

func TestGoModVerifier_Missing(t *testing.T) {
	content := []byte(`module github.com/myorg/myapp

go 1.21

require (
	github.com/other/pkg v1.0.0
)
`)

	v := &GoModVerifier{}
	assertion := json.RawMessage(`{"module": "github.com/foo/bar"}`)

	result, err := v.Verify(content, assertion, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "missing" {
		t.Errorf("expected missing, got %s", result.Status)
	}
}

func TestTSImportVerifier_ModuleFound(t *testing.T) {
	content := []byte(`import { calculatePrice, PriceResult } from "@otopoint/pricing-engine";
import express from "express";

const app = express();
`)

	v := &TSImportVerifier{}
	assertion := json.RawMessage(`{"module": "@otopoint/pricing-engine", "symbols": ["calculatePrice"]}`)

	result, err := v.Verify(content, assertion, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "valid" {
		t.Errorf("expected valid, got %s: %s", result.Status, result.Reason)
	}
	if result.Confidence < 0.90 {
		t.Errorf("expected confidence >= 0.90, got %f", result.Confidence)
	}
	if result.NewLocator == nil || result.NewLocator.LineStart != 1 {
		t.Errorf("expected line 1, got %v", result.NewLocator)
	}
}

func TestTSImportVerifier_ModuleMissing(t *testing.T) {
	content := []byte(`import express from "express";

const app = express();
`)

	v := &TSImportVerifier{}
	assertion := json.RawMessage(`{"module": "@otopoint/pricing-engine"}`)

	result, err := v.Verify(content, assertion, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "missing" {
		t.Errorf("expected missing, got %s: %s", result.Status, result.Reason)
	}
}

func TestTSImportVerifier_SymbolMissing(t *testing.T) {
	content := []byte(`import { PriceResult } from "@otopoint/pricing-engine";
`)

	v := &TSImportVerifier{}
	assertion := json.RawMessage(`{"module": "@otopoint/pricing-engine", "symbols": ["calculatePrice", "PriceResult"]}`)

	result, err := v.Verify(content, assertion, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "valid" {
		t.Errorf("expected valid (module exists), got %s: %s", result.Status, result.Reason)
	}
	if result.ChangeType != "value_changed" {
		t.Errorf("expected value_changed, got %q", result.ChangeType)
	}
}

func TestTSImportVerifier_DefaultImport(t *testing.T) {
	content := []byte(`import PricingEngine from "@otopoint/pricing-engine";
`)

	v := &TSImportVerifier{}
	assertion := json.RawMessage(`{"module": "@otopoint/pricing-engine"}`)

	result, err := v.Verify(content, assertion, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "valid" {
		t.Errorf("expected valid, got %s: %s", result.Status, result.Reason)
	}
}

func TestTSImportVerifier_NamespaceImport(t *testing.T) {
	content := []byte(`import * as pricing from "@otopoint/pricing-engine";
`)

	v := &TSImportVerifier{}
	assertion := json.RawMessage(`{"module": "@otopoint/pricing-engine"}`)

	result, err := v.Verify(content, assertion, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "valid" {
		t.Errorf("expected valid, got %s: %s", result.Status, result.Reason)
	}
}

func TestTextVerifier_Found(t *testing.T) {
	content := []byte(`import { calculatePrice } from "./pricing";

export function checkout() {
  const price = calculatePrice(items);
  return price;
}
`)

	v := &TextVerifier{}
	assertion := json.RawMessage(`{"substring": "calculatePrice", "context": "import"}`)

	result, err := v.Verify(content, assertion, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "valid" {
		t.Errorf("expected valid, got %s: %s", result.Status, result.Reason)
	}
	if result.Confidence > 0.60 {
		t.Errorf("TextVerifier confidence should be capped at 0.60, got %f", result.Confidence)
	}
}

func TestTextVerifier_Ambiguous(t *testing.T) {
	content := []byte(`import { calculatePrice } from "./pricing";
import { calculatePrice } from "./pricing-v2";
`)

	v := &TextVerifier{}
	assertion := json.RawMessage(`{"substring": "calculatePrice", "context": "import"}`)

	result, err := v.Verify(content, assertion, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "ambiguous" {
		t.Errorf("expected ambiguous, got %s", result.Status)
	}
}

func TestTextVerifier_Missing(t *testing.T) {
	content := []byte(`import express from "express";
`)

	v := &TextVerifier{}
	assertion := json.RawMessage(`{"substring": "calculatePrice"}`)

	result, err := v.Verify(content, assertion, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "missing" {
		t.Errorf("expected missing, got %s", result.Status)
	}
}

func TestRegistry_Dispatch(t *testing.T) {
	reg := DefaultRegistry()

	content := []byte(`{"dependencies": {"foo": "1.0.0"}}`)
	assertion := json.RawMessage(`{"package": "foo"}`)

	result, err := reg.Verify("manifest_dependency", content, assertion, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "valid" {
		t.Errorf("expected valid, got %s: %s", result.Status, result.Reason)
	}
}

func TestRegistry_Unsupported(t *testing.T) {
	reg := DefaultRegistry()

	result, err := reg.Verify("unknown_kind", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "unsupported" {
		t.Errorf("expected unsupported, got %s", result.Status)
	}
}
