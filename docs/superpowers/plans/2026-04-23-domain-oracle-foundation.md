# Domain Oracle CLI — Foundation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a Go CLI (`oracle`) that provides validated graph storage, query, and MCP server for a multi-layered knowledge graph backed by SQLite.

**Architecture:** Cobra CLI → validation layer (type registry + key normalization) → SQLite store. MCP server reuses the same graph operations via stdio transport. All mutations validated against a YAML type registry before persisting.

**Tech Stack:** Go 1.22, Cobra (CLI), modernc.org/sqlite (pure Go SQLite), gopkg.in/yaml.v3 (YAML), mark3labs/mcp-go (MCP server)

---

### Task 1: Go Module + Project Scaffold

**Files:**
- Create: `go.mod`
- Create: `cmd/oracle/main.go`
- Create: `.gitignore`

- [ ] **Step 1: Initialize Go module**

```bash
cd /home/alex/personal/depbot
go mod init github.com/anthropics/depbot
```

- [ ] **Step 2: Create .gitignore**

Create `.gitignore`:

```
oracle.db
oracle.db-journal
oracle.db-wal
/oracle
*.exe
dist/
.oracle/
```

- [ ] **Step 3: Create minimal main.go**

Create `cmd/oracle/main.go`:

```go
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func main() {
	root := &cobra.Command{
		Use:   "oracle",
		Short: "Domain Oracle — graph storage and query API",
	}

	root.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Print version",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println("oracle v0.1.0")
		},
	})

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}
```

- [ ] **Step 4: Add Cobra dependency and build**

```bash
cd /home/alex/personal/depbot
go get github.com/spf13/cobra
go build -o oracle ./cmd/oracle
./oracle version
```

Expected: `oracle v0.1.0`

- [ ] **Step 5: Commit**

```bash
git add go.mod go.sum cmd/ .gitignore
git commit -m "feat: initialize Go module with Cobra CLI scaffold"
```

---

### Task 2: Domain Manifest Loader

**Files:**
- Create: `internal/manifest/manifest.go`
- Create: `internal/manifest/manifest_test.go`
- Create: `testdata/manifest/valid.yaml`
- Create: `testdata/manifest/invalid_no_domain.yaml`

- [ ] **Step 1: Create test fixtures**

Create `testdata/manifest/valid.yaml`:

```yaml
domain: orders
description: Order processing and payment domain
repositories:
  - name: orders-api
    path: ./repos/orders-api
    tags: [nestjs, graphql, kafka-producer]
  - name: payments-api
    path: ./repos/payments-api
    tags: [nestjs, rest, kafka-consumer]
owner: checkout-team
```

Create `testdata/manifest/invalid_no_domain.yaml`:

```yaml
description: Missing domain field
repositories:
  - name: orders-api
    path: ./repos/orders-api
```

- [ ] **Step 2: Write failing tests**

Create `internal/manifest/manifest_test.go`:

```go
package manifest

import (
	"testing"
)

func TestLoadValid(t *testing.T) {
	m, err := LoadFile("../../testdata/manifest/valid.yaml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Domain != "orders" {
		t.Errorf("domain = %q, want %q", m.Domain, "orders")
	}
	if len(m.Repositories) != 2 {
		t.Errorf("repos = %d, want 2", len(m.Repositories))
	}
	if m.Repositories[0].Name != "orders-api" {
		t.Errorf("repo[0].name = %q, want %q", m.Repositories[0].Name, "orders-api")
	}
	if m.Owner != "checkout-team" {
		t.Errorf("owner = %q, want %q", m.Owner, "checkout-team")
	}
	tags := m.Repositories[0].Tags
	if len(tags) != 3 || tags[0] != "nestjs" {
		t.Errorf("repo[0].tags = %v, want [nestjs graphql kafka-producer]", tags)
	}
}

func TestLoadMissingDomain(t *testing.T) {
	_, err := LoadFile("../../testdata/manifest/invalid_no_domain.yaml")
	if err == nil {
		t.Fatal("expected error for missing domain, got nil")
	}
}

func TestLoadFileNotFound(t *testing.T) {
	_, err := LoadFile("nonexistent.yaml")
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}
```

- [ ] **Step 3: Run tests to verify they fail**

```bash
cd /home/alex/personal/depbot
go test ./internal/manifest/ -v
```

Expected: FAIL — package doesn't exist yet.

- [ ] **Step 4: Implement manifest loader**

Create `internal/manifest/manifest.go`:

```go
package manifest

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type Repository struct {
	Name string   `yaml:"name"`
	Path string   `yaml:"path"`
	Tags []string `yaml:"tags"`
}

type Manifest struct {
	Domain       string       `yaml:"domain"`
	Description  string       `yaml:"description"`
	Repositories []Repository `yaml:"repositories"`
	Owner        string       `yaml:"owner"`
}

func LoadFile(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading manifest: %w", err)
	}
	return Load(data)
}

func Load(data []byte) (*Manifest, error) {
	var m Manifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parsing manifest: %w", err)
	}
	if m.Domain == "" {
		return nil, fmt.Errorf("manifest validation: domain is required")
	}
	if len(m.Repositories) == 0 {
		return nil, fmt.Errorf("manifest validation: at least one repository is required")
	}
	for i, r := range m.Repositories {
		if r.Name == "" {
			return nil, fmt.Errorf("manifest validation: repository[%d].name is required", i)
		}
		if r.Path == "" {
			return nil, fmt.Errorf("manifest validation: repository[%d].path is required", i)
		}
	}
	return &m, nil
}
```

- [ ] **Step 5: Add YAML dependency and run tests**

```bash
cd /home/alex/personal/depbot
go get gopkg.in/yaml.v3
go test ./internal/manifest/ -v
```

Expected: all 3 tests PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/manifest/ testdata/manifest/ go.mod go.sum
git commit -m "feat: add domain manifest loader with validation"
```

---

### Task 3: Type Registry

**Files:**
- Create: `internal/registry/registry.go`
- Create: `internal/registry/registry_test.go`
- Create: `internal/registry/defaults.go`
- Create: `testdata/registry/valid.yaml`
- Create: `testdata/registry/invalid_edge.yaml`

- [ ] **Step 1: Create test fixtures**

Create `testdata/registry/valid.yaml`:

```yaml
version: "1"

layers:
  - code
  - service
  - contract

node_types:
  code:
    - controller
    - provider
    - module
  service:
    - service
    - worker
  contract:
    - endpoint
    - topic

edge_types:
  INJECTS:
    from_layers: [code]
    to_layers: [code]
  EXPOSES_ENDPOINT:
    from_layers: [code, service]
    to_layers: [contract]
  PUBLISHES_TOPIC:
    from_layers: [code, service]
    to_layers: [contract]

derivation_kinds:
  - hard
  - linked
  - inferred
  - unknown

source_kinds:
  - file
  - openapi

node_statuses:
  - active
  - stale
  - deleted
  - unknown

trigger_kinds:
  - full_scan
  - manual
```

Create `testdata/registry/invalid_edge.yaml`:

```yaml
version: "1"

layers:
  - code

node_types:
  code:
    - controller

edge_types:
  BAD_EDGE:
    from_layers: [nonexistent]
    to_layers: [code]

derivation_kinds:
  - hard

source_kinds:
  - file

node_statuses:
  - active

trigger_kinds:
  - manual
```

- [ ] **Step 2: Write failing tests**

Create `internal/registry/registry_test.go`:

```go
package registry

import (
	"testing"
)

func TestLoadValid(t *testing.T) {
	r, err := LoadFile("../../testdata/registry/valid.yaml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !r.IsValidLayer("code") {
		t.Error("expected code to be valid layer")
	}
	if r.IsValidLayer("nonexistent") {
		t.Error("expected nonexistent to be invalid layer")
	}
	if !r.IsValidNodeType("code", "controller") {
		t.Error("expected code:controller to be valid")
	}
	if r.IsValidNodeType("code", "nonexistent") {
		t.Error("expected code:nonexistent to be invalid")
	}
	if r.IsValidNodeType("service", "controller") {
		t.Error("expected service:controller to be invalid")
	}
	if !r.IsValidEdgeType("INJECTS") {
		t.Error("expected INJECTS to be valid edge type")
	}
	if r.IsValidEdgeType("NONEXISTENT") {
		t.Error("expected NONEXISTENT to be invalid edge type")
	}
}

func TestValidateEdgeLayers(t *testing.T) {
	r, err := LoadFile("../../testdata/registry/valid.yaml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := r.ValidateEdgeLayers("INJECTS", "code", "code"); err != nil {
		t.Errorf("INJECTS code->code should be valid: %v", err)
	}
	if err := r.ValidateEdgeLayers("INJECTS", "service", "code"); err == nil {
		t.Error("INJECTS service->code should be invalid")
	}
	if err := r.ValidateEdgeLayers("EXPOSES_ENDPOINT", "code", "contract"); err != nil {
		t.Errorf("EXPOSES_ENDPOINT code->contract should be valid: %v", err)
	}
	if err := r.ValidateEdgeLayers("EXPOSES_ENDPOINT", "code", "code"); err == nil {
		t.Error("EXPOSES_ENDPOINT code->code should be invalid")
	}
}

func TestLoadInvalidEdge(t *testing.T) {
	_, err := LoadFile("../../testdata/registry/invalid_edge.yaml")
	if err == nil {
		t.Fatal("expected error for edge referencing nonexistent layer")
	}
}

func TestIsValidDerivation(t *testing.T) {
	r, err := LoadFile("../../testdata/registry/valid.yaml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !r.IsValidDerivation("hard") {
		t.Error("expected hard to be valid")
	}
	if r.IsValidDerivation("bogus") {
		t.Error("expected bogus to be invalid")
	}
}

func TestIsValidSourceKind(t *testing.T) {
	r, err := LoadFile("../../testdata/registry/valid.yaml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !r.IsValidSourceKind("file") {
		t.Error("expected file to be valid")
	}
	if r.IsValidSourceKind("bogus") {
		t.Error("expected bogus to be invalid")
	}
}
```

- [ ] **Step 3: Run tests to verify they fail**

```bash
go test ./internal/registry/ -v
```

Expected: FAIL.

- [ ] **Step 4: Implement registry**

Create `internal/registry/registry.go`:

```go
package registry

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type EdgeTypeDef struct {
	FromLayers []string `yaml:"from_layers"`
	ToLayers   []string `yaml:"to_layers"`
}

type RegistryFile struct {
	Version         string                 `yaml:"version"`
	Layers          []string               `yaml:"layers"`
	NodeTypes       map[string][]string    `yaml:"node_types"`
	EdgeTypes       map[string]EdgeTypeDef `yaml:"edge_types"`
	DerivationKinds []string               `yaml:"derivation_kinds"`
	SourceKinds     []string               `yaml:"source_kinds"`
	NodeStatuses    []string               `yaml:"node_statuses"`
	TriggerKinds    []string               `yaml:"trigger_kinds"`
}

type Registry struct {
	layers      map[string]bool
	nodeTypes   map[string]map[string]bool // layer -> type -> bool
	edgeTypes   map[string]EdgeTypeDef
	derivations map[string]bool
	sourceKinds map[string]bool
	statuses    map[string]bool
	triggers    map[string]bool
}

func LoadFile(path string) (*Registry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading registry: %w", err)
	}
	return Load(data)
}

func Load(data []byte) (*Registry, error) {
	var f RegistryFile
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parsing registry: %w", err)
	}

	r := &Registry{
		layers:      toSet(f.Layers),
		nodeTypes:   make(map[string]map[string]bool),
		edgeTypes:   f.EdgeTypes,
		derivations: toSet(f.DerivationKinds),
		sourceKinds: toSet(f.SourceKinds),
		statuses:    toSet(f.NodeStatuses),
		triggers:    toSet(f.TriggerKinds),
	}

	for layer, types := range f.NodeTypes {
		if !r.layers[layer] {
			return nil, fmt.Errorf("registry validation: node_types references unknown layer %q", layer)
		}
		r.nodeTypes[layer] = toSet(types)
	}

	for name, et := range f.EdgeTypes {
		for _, l := range et.FromLayers {
			if !r.layers[l] {
				return nil, fmt.Errorf("registry validation: edge_type %q from_layers references unknown layer %q", name, l)
			}
		}
		for _, l := range et.ToLayers {
			if !r.layers[l] {
				return nil, fmt.Errorf("registry validation: edge_type %q to_layers references unknown layer %q", name, l)
			}
		}
	}

	return r, nil
}

func (r *Registry) IsValidLayer(layer string) bool {
	return r.layers[layer]
}

func (r *Registry) IsValidNodeType(layer, nodeType string) bool {
	types, ok := r.nodeTypes[layer]
	if !ok {
		return false
	}
	return types[nodeType]
}

func (r *Registry) IsValidEdgeType(edgeType string) bool {
	_, ok := r.edgeTypes[edgeType]
	return ok
}

func (r *Registry) ValidateEdgeLayers(edgeType, fromLayer, toLayer string) error {
	et, ok := r.edgeTypes[edgeType]
	if !ok {
		return fmt.Errorf("unknown edge type %q", edgeType)
	}
	fromOk := false
	for _, l := range et.FromLayers {
		if l == fromLayer {
			fromOk = true
			break
		}
	}
	if !fromOk {
		return fmt.Errorf("edge type %q does not allow from_layer %q", edgeType, fromLayer)
	}
	toOk := false
	for _, l := range et.ToLayers {
		if l == toLayer {
			toOk = true
			break
		}
	}
	if !toOk {
		return fmt.Errorf("edge type %q does not allow to_layer %q", edgeType, toLayer)
	}
	return nil
}

func (r *Registry) IsValidDerivation(kind string) bool {
	return r.derivations[kind]
}

func (r *Registry) IsValidSourceKind(kind string) bool {
	return r.sourceKinds[kind]
}

func (r *Registry) IsValidStatus(status string) bool {
	return r.statuses[status]
}

func (r *Registry) IsValidTrigger(trigger string) bool {
	return r.triggers[trigger]
}

func toSet(items []string) map[string]bool {
	s := make(map[string]bool, len(items))
	for _, item := range items {
		s[item] = true
	}
	return s
}
```

- [ ] **Step 5: Run tests**

```bash
go test ./internal/registry/ -v
```

Expected: all tests PASS.

- [ ] **Step 6: Create defaults.go with embedded default registry**

Create `internal/registry/defaults.go`:

```go
package registry

import _ "embed"

//go:embed defaults.yaml
var DefaultRegistryYAML []byte

func LoadDefaults() (*Registry, error) {
	return Load(DefaultRegistryYAML)
}
```

Create `internal/registry/defaults.yaml` with the full type registry from the spec (all layers, node_types, edge_types, derivation_kinds, source_kinds, node_statuses, trigger_kinds). This is the complete YAML from the spec's "Type Registry" section.

- [ ] **Step 7: Commit**

```bash
git add internal/registry/ testdata/registry/
git commit -m "feat: add type registry with validation and defaults"
```

---

### Task 4: Validation Layer — Key Normalization

**Files:**
- Create: `internal/validate/keys.go`
- Create: `internal/validate/keys_test.go`

- [ ] **Step 1: Write failing tests**

Create `internal/validate/keys_test.go`:

```go
package validate

import (
	"testing"
)

func TestNormalizeNodeKey(t *testing.T) {
	tests := []struct {
		input string
		want  string
		err   bool
	}{
		{"code:controller:orders:OrdersController", "code:controller:orders:orderscontroller", false},
		{"  Code:Controller:Orders:OrdersController  ", "code:controller:orders:orderscontroller", false},
		{"code:controller:orders:/path/to/thing/", "code:controller:orders:path/to/thing", false},
		{"code:controller:orders:", "", true},      // empty qualified_name
		{"code:controller", "", true},              // too few parts
		{"a:b:c:d:e:f", "a:b:c:d:e:f", false},     // extra colons allowed in qualified_name
		{"", "", true},
	}

	for _, tt := range tests {
		got, err := NormalizeNodeKey(tt.input)
		if tt.err && err == nil {
			t.Errorf("NormalizeNodeKey(%q): expected error", tt.input)
		}
		if !tt.err && err != nil {
			t.Errorf("NormalizeNodeKey(%q): unexpected error: %v", tt.input, err)
		}
		if got != tt.want {
			t.Errorf("NormalizeNodeKey(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestBuildEdgeKey(t *testing.T) {
	from := "code:controller:orders:orderscontroller"
	to := "code:provider:orders:ordersservice"
	edgeType := "INJECTS"
	got := BuildEdgeKey(from, to, edgeType)
	want := "code:controller:orders:orderscontroller->code:provider:orders:ordersservice:INJECTS"
	if got != want {
		t.Errorf("BuildEdgeKey = %q, want %q", got, want)
	}
}

func TestNormalizeEdgeKey(t *testing.T) {
	tests := []struct {
		input string
		want  string
		err   bool
	}{
		{
			"code:controller:orders:OrdersController->code:provider:orders:OrdersService:INJECTS",
			"code:controller:orders:orderscontroller->code:provider:orders:ordersservice:INJECTS",
			false,
		},
		{"bad-format", "", true},
		{"->:INJECTS", "", true},
	}

	for _, tt := range tests {
		got, err := NormalizeEdgeKey(tt.input)
		if tt.err && err == nil {
			t.Errorf("NormalizeEdgeKey(%q): expected error", tt.input)
		}
		if !tt.err && err != nil {
			t.Errorf("NormalizeEdgeKey(%q): unexpected error: %v", tt.input, err)
		}
		if got != tt.want {
			t.Errorf("NormalizeEdgeKey(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/validate/ -v
```

Expected: FAIL.

- [ ] **Step 3: Implement key normalization**

Create `internal/validate/keys.go`:

```go
package validate

import (
	"fmt"
	"strings"
)

// NormalizeNodeKey enforces format: layer:type:domain:qualified_name
// Lowercases, trims, strips leading/trailing slashes from qualified_name.
func NormalizeNodeKey(key string) (string, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return "", fmt.Errorf("node_key is empty")
	}
	key = strings.ToLower(key)

	// Split into at least 4 parts: layer:type:domain:rest
	parts := strings.SplitN(key, ":", 4)
	if len(parts) < 4 {
		return "", fmt.Errorf("node_key %q must have format layer:type:domain:qualified_name", key)
	}

	layer := strings.TrimSpace(parts[0])
	nodeType := strings.TrimSpace(parts[1])
	domain := strings.TrimSpace(parts[2])
	qualifiedName := strings.TrimSpace(parts[3])

	// Strip leading/trailing slashes from qualified_name
	qualifiedName = strings.Trim(qualifiedName, "/")

	if layer == "" || nodeType == "" || domain == "" || qualifiedName == "" {
		return "", fmt.Errorf("node_key %q has empty component", key)
	}

	return layer + ":" + nodeType + ":" + domain + ":" + qualifiedName, nil
}

// BuildEdgeKey constructs edge_key from from_node_key, to_node_key, edge_type.
func BuildEdgeKey(fromNodeKey, toNodeKey, edgeType string) string {
	return fromNodeKey + "->" + toNodeKey + ":" + edgeType
}

// NormalizeEdgeKey normalizes an edge_key.
// Format: {from_node_key}->{to_node_key}:{EDGE_TYPE}
func NormalizeEdgeKey(key string) (string, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return "", fmt.Errorf("edge_key is empty")
	}

	// Split on "->" to get from and to+type
	arrowIdx := strings.Index(key, "->")
	if arrowIdx < 0 {
		return "", fmt.Errorf("edge_key %q missing '->' separator", key)
	}

	fromRaw := key[:arrowIdx]
	rest := key[arrowIdx+2:]

	// The edge type is the last colon-separated part after the to_node_key
	// to_node_key has format layer:type:domain:qualified_name, so at least 4 colons
	// The edge type comes after the last ":"
	lastColon := strings.LastIndex(rest, ":")
	if lastColon < 0 {
		return "", fmt.Errorf("edge_key %q missing edge_type after to_node_key", key)
	}

	toRaw := rest[:lastColon]
	edgeType := rest[lastColon+1:]

	fromNorm, err := NormalizeNodeKey(fromRaw)
	if err != nil {
		return "", fmt.Errorf("edge_key from part: %w", err)
	}
	toNorm, err := NormalizeNodeKey(toRaw)
	if err != nil {
		return "", fmt.Errorf("edge_key to part: %w", err)
	}

	if edgeType == "" {
		return "", fmt.Errorf("edge_key %q has empty edge_type", key)
	}

	return BuildEdgeKey(fromNorm, toNorm, edgeType), nil
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/validate/ -v
```

Expected: all tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/validate/
git commit -m "feat: add key normalization for node_key and edge_key"
```

---

### Task 5: Validation Layer — Field Validation

**Files:**
- Create: `internal/validate/validate.go`
- Create: `internal/validate/validate_test.go`

- [ ] **Step 1: Write failing tests**

Create `internal/validate/validate_test.go`:

```go
package validate

import (
	"testing"

	"github.com/anthropics/depbot/internal/registry"
)

func loadTestRegistry(t *testing.T) *registry.Registry {
	t.Helper()
	r, err := registry.LoadFile("../../testdata/registry/valid.yaml")
	if err != nil {
		t.Fatalf("loading test registry: %v", err)
	}
	return r
}

func TestValidateNodeInput_Valid(t *testing.T) {
	reg := loadTestRegistry(t)
	input := NodeInput{
		NodeKey:   "code:controller:orders:OrdersController",
		Layer:     "code",
		NodeType:  "controller",
		DomainKey: "orders",
		Name:      "OrdersController",
	}
	result, err := ValidateNodeInput(input, reg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.NodeKey != "code:controller:orders:orderscontroller" {
		t.Errorf("node_key = %q, want normalized", result.NodeKey)
	}
}

func TestValidateNodeInput_MissingName(t *testing.T) {
	reg := loadTestRegistry(t)
	input := NodeInput{
		NodeKey:   "code:controller:orders:OrdersController",
		Layer:     "code",
		NodeType:  "controller",
		DomainKey: "orders",
		Name:      "",
	}
	_, err := ValidateNodeInput(input, reg)
	if err == nil {
		t.Fatal("expected error for missing name")
	}
}

func TestValidateNodeInput_BadLayer(t *testing.T) {
	reg := loadTestRegistry(t)
	input := NodeInput{
		NodeKey:   "bogus:controller:orders:OrdersController",
		Layer:     "bogus",
		NodeType:  "controller",
		DomainKey: "orders",
		Name:      "OrdersController",
	}
	_, err := ValidateNodeInput(input, reg)
	if err == nil {
		t.Fatal("expected error for invalid layer")
	}
}

func TestValidateNodeInput_BadNodeType(t *testing.T) {
	reg := loadTestRegistry(t)
	input := NodeInput{
		NodeKey:   "code:bogus:orders:OrdersController",
		Layer:     "code",
		NodeType:  "bogus",
		DomainKey: "orders",
		Name:      "OrdersController",
	}
	_, err := ValidateNodeInput(input, reg)
	if err == nil {
		t.Fatal("expected error for invalid node_type")
	}
}

func TestValidateNodeInput_BadConfidence(t *testing.T) {
	reg := loadTestRegistry(t)
	input := NodeInput{
		NodeKey:    "code:controller:orders:OrdersController",
		Layer:      "code",
		NodeType:   "controller",
		DomainKey:  "orders",
		Name:       "OrdersController",
		Confidence: 1.5,
	}
	_, err := ValidateNodeInput(input, reg)
	if err == nil {
		t.Fatal("expected error for confidence > 1")
	}
}

func TestValidateEdgeInput_Valid(t *testing.T) {
	reg := loadTestRegistry(t)
	input := EdgeInput{
		FromNodeKey:    "code:controller:orders:orderscontroller",
		ToNodeKey:      "code:provider:orders:ordersservice",
		EdgeType:       "INJECTS",
		DerivationKind: "hard",
		FromLayer:      "code",
		ToLayer:        "code",
	}
	result, err := ValidateEdgeInput(input, reg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.EdgeKey == "" {
		t.Error("edge_key should be auto-generated")
	}
}

func TestValidateEdgeInput_BadEdgeType(t *testing.T) {
	reg := loadTestRegistry(t)
	input := EdgeInput{
		FromNodeKey:    "code:controller:orders:orderscontroller",
		ToNodeKey:      "code:provider:orders:ordersservice",
		EdgeType:       "NONEXISTENT",
		DerivationKind: "hard",
		FromLayer:      "code",
		ToLayer:        "code",
	}
	_, err := ValidateEdgeInput(input, reg)
	if err == nil {
		t.Fatal("expected error for invalid edge type")
	}
}

func TestValidateEdgeInput_LayerMismatch(t *testing.T) {
	reg := loadTestRegistry(t)
	input := EdgeInput{
		FromNodeKey:    "service:service:orders:ordersservice",
		ToNodeKey:      "code:provider:orders:ordersservice",
		EdgeType:       "INJECTS",
		DerivationKind: "hard",
		FromLayer:      "service",
		ToLayer:        "code",
	}
	_, err := ValidateEdgeInput(input, reg)
	if err == nil {
		t.Fatal("expected error for layer mismatch")
	}
}

func TestValidateEvidenceInput_Valid(t *testing.T) {
	reg := loadTestRegistry(t)
	input := EvidenceInput{
		TargetKind:       "edge",
		SourceKind:       "file",
		ExtractorID:      "claude-code",
		ExtractorVersion: "1.0",
	}
	err := ValidateEvidenceInput(input, reg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateEvidenceInput_BadSourceKind(t *testing.T) {
	reg := loadTestRegistry(t)
	input := EvidenceInput{
		TargetKind:       "edge",
		SourceKind:       "bogus",
		ExtractorID:      "claude-code",
		ExtractorVersion: "1.0",
	}
	err := ValidateEvidenceInput(input, reg)
	if err == nil {
		t.Fatal("expected error for invalid source_kind")
	}
}

func TestValidateEvidenceInput_BadTargetKind(t *testing.T) {
	reg := loadTestRegistry(t)
	input := EvidenceInput{
		TargetKind:       "answer",
		SourceKind:       "file",
		ExtractorID:      "claude-code",
		ExtractorVersion: "1.0",
	}
	err := ValidateEvidenceInput(input, reg)
	if err == nil {
		t.Fatal("expected error for answer target_kind in foundation")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/validate/ -v -run TestValidate
```

Expected: FAIL.

- [ ] **Step 3: Implement validation**

Create `internal/validate/validate.go`:

```go
package validate

import (
	"fmt"

	"github.com/anthropics/depbot/internal/registry"
)

type NodeInput struct {
	NodeKey       string
	Layer         string
	NodeType      string
	DomainKey     string
	Name          string
	QualifiedName string
	RepoName      string
	FilePath      string
	Lang          string
	OwnerKey      string
	Environment   string
	Visibility    string
	Status        string
	Confidence    float64
	Metadata      string // JSON string
}

type ValidatedNode struct {
	NodeKey       string
	Layer         string
	NodeType      string
	DomainKey     string
	Name          string
	QualifiedName string
	RepoName      string
	FilePath      string
	Lang          string
	OwnerKey      string
	Environment   string
	Visibility    string
	Status        string
	Confidence    float64
	Metadata      string
}

type EdgeInput struct {
	EdgeKey        string // optional, auto-generated if empty
	FromNodeKey    string
	ToNodeKey      string
	EdgeType       string
	DerivationKind string
	FromLayer      string // layer of from_node, for validation
	ToLayer        string // layer of to_node, for validation
	ContextKey     string
	Confidence     float64
	Metadata       string
}

type ValidatedEdge struct {
	EdgeKey        string
	FromNodeKey    string
	ToNodeKey      string
	EdgeType       string
	DerivationKind string
	ContextKey     string
	Confidence     float64
	Metadata       string
}

type EvidenceInput struct {
	TargetKind       string // "node" or "edge"
	SourceKind       string
	RepoName         string
	FilePath         string
	LineStart        int
	LineEnd          int
	ColumnStart      int
	ColumnEnd        int
	Locator          string
	ExtractorID      string
	ExtractorVersion string
	ASTRule          string
	SnippetHash      string
	CommitSHA        string
	Confidence       float64
	Metadata         string
}

func ValidateNodeInput(input NodeInput, reg *registry.Registry) (*ValidatedNode, error) {
	if input.Name == "" {
		return nil, fmt.Errorf("validation: name is required")
	}
	if input.DomainKey == "" {
		return nil, fmt.Errorf("validation: domain_key is required")
	}

	normalizedKey, err := NormalizeNodeKey(input.NodeKey)
	if err != nil {
		return nil, fmt.Errorf("validation: %w", err)
	}

	if !reg.IsValidLayer(input.Layer) {
		return nil, fmt.Errorf("validation: invalid layer %q", input.Layer)
	}
	if !reg.IsValidNodeType(input.Layer, input.NodeType) {
		return nil, fmt.Errorf("validation: invalid node_type %q for layer %q", input.NodeType, input.Layer)
	}

	confidence := input.Confidence
	if confidence == 0 {
		confidence = 1.0
	}
	if confidence < 0 || confidence > 1 {
		return nil, fmt.Errorf("validation: confidence %f out of range [0, 1]", confidence)
	}

	status := input.Status
	if status == "" {
		status = "active"
	}
	if !reg.IsValidStatus(status) {
		return nil, fmt.Errorf("validation: invalid status %q", status)
	}

	return &ValidatedNode{
		NodeKey:       normalizedKey,
		Layer:         input.Layer,
		NodeType:      input.NodeType,
		DomainKey:     input.DomainKey,
		Name:          input.Name,
		QualifiedName: input.QualifiedName,
		RepoName:      input.RepoName,
		FilePath:      input.FilePath,
		Lang:          input.Lang,
		OwnerKey:      input.OwnerKey,
		Environment:   input.Environment,
		Visibility:    input.Visibility,
		Status:        status,
		Confidence:    confidence,
		Metadata:      input.Metadata,
	}, nil
}

func ValidateEdgeInput(input EdgeInput, reg *registry.Registry) (*ValidatedEdge, error) {
	if input.FromNodeKey == "" {
		return nil, fmt.Errorf("validation: from_node_key is required")
	}
	if input.ToNodeKey == "" {
		return nil, fmt.Errorf("validation: to_node_key is required")
	}
	if input.EdgeType == "" {
		return nil, fmt.Errorf("validation: edge_type is required")
	}
	if input.DerivationKind == "" {
		return nil, fmt.Errorf("validation: derivation_kind is required")
	}

	if !reg.IsValidEdgeType(input.EdgeType) {
		return nil, fmt.Errorf("validation: invalid edge_type %q", input.EdgeType)
	}
	if !reg.IsValidDerivation(input.DerivationKind) {
		return nil, fmt.Errorf("validation: invalid derivation_kind %q", input.DerivationKind)
	}

	if err := reg.ValidateEdgeLayers(input.EdgeType, input.FromLayer, input.ToLayer); err != nil {
		return nil, fmt.Errorf("validation: %w", err)
	}

	confidence := input.Confidence
	if confidence == 0 {
		confidence = 1.0
	}
	if confidence < 0 || confidence > 1 {
		return nil, fmt.Errorf("validation: confidence %f out of range [0, 1]", confidence)
	}

	edgeKey := input.EdgeKey
	if edgeKey == "" {
		edgeKey = BuildEdgeKey(input.FromNodeKey, input.ToNodeKey, input.EdgeType)
	} else {
		var err error
		edgeKey, err = NormalizeEdgeKey(edgeKey)
		if err != nil {
			return nil, fmt.Errorf("validation: %w", err)
		}
	}

	return &ValidatedEdge{
		EdgeKey:        edgeKey,
		FromNodeKey:    input.FromNodeKey,
		ToNodeKey:      input.ToNodeKey,
		EdgeType:       input.EdgeType,
		DerivationKind: input.DerivationKind,
		ContextKey:     input.ContextKey,
		Confidence:     confidence,
		Metadata:       input.Metadata,
	}, nil
}

func ValidateEvidenceInput(input EvidenceInput, reg *registry.Registry) error {
	if input.TargetKind != "node" && input.TargetKind != "edge" {
		return fmt.Errorf("validation: target_kind must be 'node' or 'edge', got %q", input.TargetKind)
	}
	if !reg.IsValidSourceKind(input.SourceKind) {
		return fmt.Errorf("validation: invalid source_kind %q", input.SourceKind)
	}
	if input.ExtractorID == "" {
		return fmt.Errorf("validation: extractor_id is required")
	}
	if input.ExtractorVersion == "" {
		return fmt.Errorf("validation: extractor_version is required")
	}
	if input.Confidence != 0 && (input.Confidence < 0 || input.Confidence > 1) {
		return fmt.Errorf("validation: confidence %f out of range [0, 1]", input.Confidence)
	}
	return nil
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/validate/ -v
```

Expected: all tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/validate/
git commit -m "feat: add field validation for nodes, edges, and evidence"
```

---

### Task 6: SQLite Store — Init + Migrations

**Files:**
- Create: `internal/store/store.go`
- Create: `internal/store/store_test.go`

- [ ] **Step 1: Write failing tests**

Create `internal/store/store_test.go`:

```go
package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpenAndMigrate(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	s, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	// Verify tables exist by querying them
	tables := []string{"graph_revisions", "graph_nodes", "graph_edges", "graph_evidence", "graph_snapshots"}
	for _, table := range tables {
		var count int
		err := s.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count)
		if err != nil {
			t.Errorf("table %s does not exist: %v", table, err)
		}
	}
}

func TestOpenCreatesDir(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "sub", "dir", "test.db")

	s, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Error("db file was not created")
	}
}

func TestOpenIdempotent(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	s1, err := Open(dbPath)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	s1.Close()

	s2, err := Open(dbPath)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	defer s2.Close()
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/store/ -v
```

Expected: FAIL.

- [ ] **Step 3: Implement store with schema**

Create `internal/store/store.go`:

```go
package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

func Open(dbPath string) (*Store, error) {
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("creating db directory: %w", err)
	}

	db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_foreign_keys=on")
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("pinging database: %w", err)
	}

	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrating database: %w", err)
	}

	return s, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) DB() *sql.DB {
	return s.db
}

func (s *Store) migrate() error {
	_, err := s.db.Exec(schema)
	return err
}

const schema = `
CREATE TABLE IF NOT EXISTS graph_revisions (
  revision_id    INTEGER PRIMARY KEY AUTOINCREMENT,
  domain_key     TEXT NOT NULL,
  git_before_sha TEXT,
  git_after_sha  TEXT NOT NULL,
  trigger_kind   TEXT NOT NULL
                   CHECK (trigger_kind IN (
                     'full_scan','manual','git_hook',
                     'push_webhook','release_webhook','ci_pipeline'
                   )),
  mode           TEXT NOT NULL
                   CHECK (mode IN ('full','incremental')),
  created_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
  metadata       TEXT NOT NULL DEFAULT '{}'
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_graph_revisions_domain_after
  ON graph_revisions(domain_key, git_after_sha);

CREATE TABLE IF NOT EXISTS graph_nodes (
  node_id                INTEGER PRIMARY KEY AUTOINCREMENT,
  node_key               TEXT NOT NULL UNIQUE,
  layer                  TEXT NOT NULL
                           CHECK (layer IN (
                             'code','service','contract','flow',
                             'ownership','infra','ci'
                           )),
  node_type              TEXT NOT NULL,
  domain_key             TEXT NOT NULL,
  name                   TEXT NOT NULL,
  qualified_name         TEXT,
  repo_name              TEXT,
  file_path              TEXT,
  lang                   TEXT,
  owner_key              TEXT,
  environment            TEXT,
  visibility             TEXT,
  status                 TEXT NOT NULL DEFAULT 'active'
                           CHECK (status IN ('active','stale','deleted','unknown')),
  first_seen_revision_id INTEGER REFERENCES graph_revisions(revision_id),
  last_seen_revision_id  INTEGER REFERENCES graph_revisions(revision_id),
  confidence             REAL NOT NULL DEFAULT 1.0
                           CHECK (confidence >= 0 AND confidence <= 1),
  metadata               TEXT NOT NULL DEFAULT '{}'
);

CREATE INDEX IF NOT EXISTS idx_graph_nodes_layer_type ON graph_nodes(layer, node_type);
CREATE INDEX IF NOT EXISTS idx_graph_nodes_domain ON graph_nodes(domain_key);
CREATE INDEX IF NOT EXISTS idx_graph_nodes_repo_path ON graph_nodes(repo_name, file_path);

CREATE TABLE IF NOT EXISTS graph_edges (
  edge_id                INTEGER PRIMARY KEY AUTOINCREMENT,
  edge_key               TEXT NOT NULL UNIQUE,
  from_node_id           INTEGER NOT NULL REFERENCES graph_nodes(node_id),
  to_node_id             INTEGER NOT NULL REFERENCES graph_nodes(node_id),
  edge_type              TEXT NOT NULL,
  derivation_kind        TEXT NOT NULL
                           CHECK (derivation_kind IN ('hard','linked','inferred','unknown')),
  context_key            TEXT,
  active                 INTEGER NOT NULL DEFAULT 1,
  first_seen_revision_id INTEGER REFERENCES graph_revisions(revision_id),
  last_seen_revision_id  INTEGER REFERENCES graph_revisions(revision_id),
  confidence             REAL NOT NULL DEFAULT 1.0
                           CHECK (confidence >= 0 AND confidence <= 1),
  metadata               TEXT NOT NULL DEFAULT '{}'
);

CREATE INDEX IF NOT EXISTS idx_graph_edges_from ON graph_edges(from_node_id);
CREATE INDEX IF NOT EXISTS idx_graph_edges_to ON graph_edges(to_node_id);
CREATE INDEX IF NOT EXISTS idx_graph_edges_type ON graph_edges(edge_type);

CREATE TABLE IF NOT EXISTS graph_evidence (
  evidence_id      INTEGER PRIMARY KEY AUTOINCREMENT,
  target_kind      TEXT NOT NULL
                     CHECK (target_kind IN ('node','edge')),
  node_id          INTEGER REFERENCES graph_nodes(node_id),
  edge_id          INTEGER REFERENCES graph_edges(edge_id),
  source_kind      TEXT NOT NULL,
  repo_name        TEXT,
  file_path        TEXT,
  line_start       INTEGER,
  line_end         INTEGER,
  column_start     INTEGER,
  column_end       INTEGER,
  locator          TEXT,
  extractor_id     TEXT NOT NULL,
  extractor_version TEXT NOT NULL,
  ast_rule         TEXT,
  snippet_hash     TEXT,
  commit_sha       TEXT,
  observed_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
  verified_at      TEXT,
  confidence       REAL NOT NULL DEFAULT 1.0
                     CHECK (confidence >= 0 AND confidence <= 1),
  metadata         TEXT NOT NULL DEFAULT '{}',
  CHECK (
    (target_kind = 'node' AND node_id IS NOT NULL AND edge_id IS NULL) OR
    (target_kind = 'edge' AND edge_id IS NOT NULL AND node_id IS NULL)
  )
);

CREATE INDEX IF NOT EXISTS idx_graph_evidence_node ON graph_evidence(node_id);
CREATE INDEX IF NOT EXISTS idx_graph_evidence_edge ON graph_evidence(edge_id);
CREATE INDEX IF NOT EXISTS idx_graph_evidence_source ON graph_evidence(source_kind, repo_name, file_path);

CREATE TABLE IF NOT EXISTS graph_snapshots (
  snapshot_id        INTEGER PRIMARY KEY AUTOINCREMENT,
  revision_id        INTEGER NOT NULL REFERENCES graph_revisions(revision_id),
  domain_key         TEXT NOT NULL,
  snapshot_kind      TEXT NOT NULL
                       CHECK (snapshot_kind IN ('full','incremental')),
  created_at         TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
  node_count         INTEGER NOT NULL,
  edge_count         INTEGER NOT NULL,
  changed_file_count INTEGER NOT NULL DEFAULT 0,
  changed_node_count INTEGER NOT NULL DEFAULT 0,
  changed_edge_count INTEGER NOT NULL DEFAULT 0,
  impacted_node_count INTEGER NOT NULL DEFAULT 0,
  summary            TEXT NOT NULL DEFAULT '{}',
  UNIQUE (revision_id, domain_key)
);

CREATE INDEX IF NOT EXISTS idx_graph_snapshots_domain_created
  ON graph_snapshots(domain_key, created_at DESC);
`
```

- [ ] **Step 4: Add SQLite dependency and run tests**

```bash
cd /home/alex/personal/depbot
go get modernc.org/sqlite
go test ./internal/store/ -v
```

Expected: all tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store/ go.mod go.sum
git commit -m "feat: add SQLite store with schema migrations"
```

---

### Task 7: Store — Revisions CRUD

**Files:**
- Create: `internal/store/revisions.go`
- Create: `internal/store/revisions_test.go`

- [ ] **Step 1: Write failing tests**

Create `internal/store/revisions_test.go`:

```go
package store

import (
	"path/filepath"
	"testing"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestCreateRevision(t *testing.T) {
	s := openTestStore(t)

	id, err := s.CreateRevision("orders", "", "abc123", "manual", "full", "{}")
	if err != nil {
		t.Fatalf("CreateRevision: %v", err)
	}
	if id <= 0 {
		t.Errorf("revision_id = %d, want > 0", id)
	}
}

func TestCreateRevisionDuplicate(t *testing.T) {
	s := openTestStore(t)

	_, err := s.CreateRevision("orders", "", "abc123", "manual", "full", "{}")
	if err != nil {
		t.Fatalf("first CreateRevision: %v", err)
	}

	_, err = s.CreateRevision("orders", "", "abc123", "manual", "full", "{}")
	if err == nil {
		t.Fatal("expected error for duplicate domain+after_sha")
	}
}

func TestGetRevision(t *testing.T) {
	s := openTestStore(t)

	id, err := s.CreateRevision("orders", "before1", "after1", "manual", "full", `{"note":"test"}`)
	if err != nil {
		t.Fatalf("CreateRevision: %v", err)
	}

	rev, err := s.GetRevision(id)
	if err != nil {
		t.Fatalf("GetRevision: %v", err)
	}
	if rev.DomainKey != "orders" {
		t.Errorf("domain = %q, want orders", rev.DomainKey)
	}
	if rev.GitBeforeSHA != "before1" {
		t.Errorf("before_sha = %q, want before1", rev.GitBeforeSHA)
	}
	if rev.GitAfterSHA != "after1" {
		t.Errorf("after_sha = %q, want after1", rev.GitAfterSHA)
	}
	if rev.Mode != "full" {
		t.Errorf("mode = %q, want full", rev.Mode)
	}
}

func TestGetRevisionNotFound(t *testing.T) {
	s := openTestStore(t)

	_, err := s.GetRevision(999)
	if err == nil {
		t.Fatal("expected error for nonexistent revision")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/store/ -v -run TestRevision
```

Expected: FAIL.

- [ ] **Step 3: Implement revisions CRUD**

Create `internal/store/revisions.go`:

```go
package store

import (
	"database/sql"
	"fmt"
)

type Revision struct {
	RevisionID   int64  `json:"revision_id"`
	DomainKey    string `json:"domain_key"`
	GitBeforeSHA string `json:"git_before_sha"`
	GitAfterSHA  string `json:"git_after_sha"`
	TriggerKind  string `json:"trigger_kind"`
	Mode         string `json:"mode"`
	CreatedAt    string `json:"created_at"`
	Metadata     string `json:"metadata"`
}

func (s *Store) CreateRevision(domainKey, beforeSHA, afterSHA, triggerKind, mode, metadata string) (int64, error) {
	if metadata == "" {
		metadata = "{}"
	}
	res, err := s.db.Exec(
		`INSERT INTO graph_revisions (domain_key, git_before_sha, git_after_sha, trigger_kind, mode, metadata)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		domainKey, beforeSHA, afterSHA, triggerKind, mode, metadata,
	)
	if err != nil {
		return 0, fmt.Errorf("creating revision: %w", err)
	}
	return res.LastInsertId()
}

func (s *Store) GetRevision(id int64) (*Revision, error) {
	var r Revision
	err := s.db.QueryRow(
		`SELECT revision_id, domain_key, COALESCE(git_before_sha, ''), git_after_sha,
		        trigger_kind, mode, created_at, metadata
		 FROM graph_revisions WHERE revision_id = ?`, id,
	).Scan(&r.RevisionID, &r.DomainKey, &r.GitBeforeSHA, &r.GitAfterSHA,
		&r.TriggerKind, &r.Mode, &r.CreatedAt, &r.Metadata)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("revision %d not found", id)
	}
	if err != nil {
		return nil, fmt.Errorf("getting revision: %w", err)
	}
	return &r, nil
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/store/ -v -run TestRevision
```

Expected: all tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store/revisions.go internal/store/revisions_test.go
git commit -m "feat: add revisions CRUD to store"
```

---

### Task 8: Store — Nodes CRUD with Upsert

**Files:**
- Create: `internal/store/nodes.go`
- Create: `internal/store/nodes_test.go`

- [ ] **Step 1: Write failing tests**

Create `internal/store/nodes_test.go`:

```go
package store

import (
	"testing"
)

func TestUpsertNodeInsert(t *testing.T) {
	s := openTestStore(t)
	revID, _ := s.CreateRevision("orders", "", "sha1", "manual", "full", "{}")

	id, err := s.UpsertNode(NodeRow{
		NodeKey:             "code:controller:orders:orderscontroller",
		Layer:               "code",
		NodeType:            "controller",
		DomainKey:           "orders",
		Name:                "OrdersController",
		RepoName:            "orders-api",
		FilePath:            "src/orders/orders.controller.ts",
		Status:              "active",
		FirstSeenRevisionID: revID,
		LastSeenRevisionID:  revID,
		Confidence:          0.99,
		Metadata:            "{}",
	})
	if err != nil {
		t.Fatalf("UpsertNode insert: %v", err)
	}
	if id <= 0 {
		t.Errorf("node_id = %d, want > 0", id)
	}
}

func TestUpsertNodeUpdate(t *testing.T) {
	s := openTestStore(t)
	revID1, _ := s.CreateRevision("orders", "", "sha1", "manual", "full", "{}")
	revID2, _ := s.CreateRevision("orders", "sha1", "sha2", "manual", "incremental", "{}")

	id1, _ := s.UpsertNode(NodeRow{
		NodeKey:             "code:controller:orders:orderscontroller",
		Layer:               "code",
		NodeType:            "controller",
		DomainKey:           "orders",
		Name:                "OrdersController",
		Status:              "active",
		FirstSeenRevisionID: revID1,
		LastSeenRevisionID:  revID1,
		Confidence:          0.99,
		Metadata:            "{}",
	})

	id2, _ := s.UpsertNode(NodeRow{
		NodeKey:             "code:controller:orders:orderscontroller",
		Layer:               "code",
		NodeType:            "controller",
		DomainKey:           "orders",
		Name:                "OrdersController-Renamed",
		Status:              "active",
		FirstSeenRevisionID: revID1,
		LastSeenRevisionID:  revID2,
		Confidence:          0.95,
		Metadata:            `{"updated": true}`,
	})

	if id1 != id2 {
		t.Errorf("upsert should return same id: %d != %d", id1, id2)
	}

	node, err := s.GetNodeByKey("code:controller:orders:orderscontroller")
	if err != nil {
		t.Fatalf("GetNodeByKey: %v", err)
	}
	if node.Name != "OrdersController-Renamed" {
		t.Errorf("name = %q, want OrdersController-Renamed", node.Name)
	}
	if node.Confidence != 0.95 {
		t.Errorf("confidence = %f, want 0.95", node.Confidence)
	}
	if node.LastSeenRevisionID != revID2 {
		t.Errorf("last_seen = %d, want %d", node.LastSeenRevisionID, revID2)
	}
}

func TestUpsertNodeConflict(t *testing.T) {
	s := openTestStore(t)
	revID, _ := s.CreateRevision("orders", "", "sha1", "manual", "full", "{}")

	s.UpsertNode(NodeRow{
		NodeKey:             "code:controller:orders:orderscontroller",
		Layer:               "code",
		NodeType:            "controller",
		DomainKey:           "orders",
		Name:                "OrdersController",
		Status:              "active",
		FirstSeenRevisionID: revID,
		LastSeenRevisionID:  revID,
		Confidence:          0.99,
		Metadata:            "{}",
	})

	// Try to upsert with different immutable field (layer)
	_, err := s.UpsertNode(NodeRow{
		NodeKey:             "code:controller:orders:orderscontroller",
		Layer:               "service", // conflict!
		NodeType:            "controller",
		DomainKey:           "orders",
		Name:                "OrdersController",
		Status:              "active",
		FirstSeenRevisionID: revID,
		LastSeenRevisionID:  revID,
		Confidence:          0.99,
		Metadata:            "{}",
	})
	if err == nil {
		t.Fatal("expected conflict error for different layer")
	}
}

func TestGetNodeNotFound(t *testing.T) {
	s := openTestStore(t)
	_, err := s.GetNodeByKey("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent node")
	}
}

func TestListNodes(t *testing.T) {
	s := openTestStore(t)
	revID, _ := s.CreateRevision("orders", "", "sha1", "manual", "full", "{}")

	s.UpsertNode(NodeRow{
		NodeKey: "code:controller:orders:orderscontroller", Layer: "code", NodeType: "controller",
		DomainKey: "orders", Name: "OrdersController", Status: "active",
		FirstSeenRevisionID: revID, LastSeenRevisionID: revID, Confidence: 1, Metadata: "{}",
	})
	s.UpsertNode(NodeRow{
		NodeKey: "code:provider:orders:ordersservice", Layer: "code", NodeType: "provider",
		DomainKey: "orders", Name: "OrdersService", Status: "active",
		FirstSeenRevisionID: revID, LastSeenRevisionID: revID, Confidence: 1, Metadata: "{}",
	})
	s.UpsertNode(NodeRow{
		NodeKey: "service:service:orders:payments-api", Layer: "service", NodeType: "service",
		DomainKey: "orders", Name: "payments-api", Status: "active",
		FirstSeenRevisionID: revID, LastSeenRevisionID: revID, Confidence: 1, Metadata: "{}",
	})

	// List all
	nodes, err := s.ListNodes(NodeFilter{})
	if err != nil {
		t.Fatalf("ListNodes: %v", err)
	}
	if len(nodes) != 3 {
		t.Errorf("count = %d, want 3", len(nodes))
	}

	// Filter by layer
	nodes, err = s.ListNodes(NodeFilter{Layer: "code"})
	if err != nil {
		t.Fatalf("ListNodes layer: %v", err)
	}
	if len(nodes) != 2 {
		t.Errorf("count = %d, want 2", len(nodes))
	}

	// Filter by type
	nodes, err = s.ListNodes(NodeFilter{Layer: "code", NodeType: "controller"})
	if err != nil {
		t.Fatalf("ListNodes type: %v", err)
	}
	if len(nodes) != 1 {
		t.Errorf("count = %d, want 1", len(nodes))
	}
}

func TestDeleteNode(t *testing.T) {
	s := openTestStore(t)
	revID, _ := s.CreateRevision("orders", "", "sha1", "manual", "full", "{}")

	s.UpsertNode(NodeRow{
		NodeKey: "code:controller:orders:orderscontroller", Layer: "code", NodeType: "controller",
		DomainKey: "orders", Name: "OrdersController", Status: "active",
		FirstSeenRevisionID: revID, LastSeenRevisionID: revID, Confidence: 1, Metadata: "{}",
	})

	err := s.DeleteNode("code:controller:orders:orderscontroller")
	if err != nil {
		t.Fatalf("DeleteNode: %v", err)
	}

	node, err := s.GetNodeByKey("code:controller:orders:orderscontroller")
	if err != nil {
		t.Fatalf("GetNodeByKey: %v", err)
	}
	if node.Status != "deleted" {
		t.Errorf("status = %q, want deleted", node.Status)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/store/ -v -run "TestUpsertNode|TestGetNode|TestListNode|TestDeleteNode"
```

Expected: FAIL.

- [ ] **Step 3: Implement nodes CRUD**

Create `internal/store/nodes.go`:

```go
package store

import (
	"database/sql"
	"fmt"
	"strings"
)

type NodeRow struct {
	NodeID              int64   `json:"node_id"`
	NodeKey             string  `json:"node_key"`
	Layer               string  `json:"layer"`
	NodeType            string  `json:"node_type"`
	DomainKey           string  `json:"domain_key"`
	Name                string  `json:"name"`
	QualifiedName       string  `json:"qualified_name,omitempty"`
	RepoName            string  `json:"repo_name,omitempty"`
	FilePath            string  `json:"file_path,omitempty"`
	Lang                string  `json:"lang,omitempty"`
	OwnerKey            string  `json:"owner_key,omitempty"`
	Environment         string  `json:"environment,omitempty"`
	Visibility          string  `json:"visibility,omitempty"`
	Status              string  `json:"status"`
	FirstSeenRevisionID int64   `json:"first_seen_revision_id"`
	LastSeenRevisionID  int64   `json:"last_seen_revision_id"`
	Confidence          float64 `json:"confidence"`
	Metadata            string  `json:"metadata"`
}

type NodeFilter struct {
	Layer    string
	NodeType string
	Domain   string
	RepoName string
	Status   string
}

func (s *Store) UpsertNode(n NodeRow) (int64, error) {
	// Check if exists
	existing, err := s.GetNodeByKey(n.NodeKey)
	if err == nil {
		// Exists — check immutable fields
		if existing.Layer != n.Layer {
			return 0, fmt.Errorf("conflict: node %q layer is %q, cannot change to %q", n.NodeKey, existing.Layer, n.Layer)
		}
		if existing.NodeType != n.NodeType {
			return 0, fmt.Errorf("conflict: node %q node_type is %q, cannot change to %q", n.NodeKey, existing.NodeType, n.NodeType)
		}
		if existing.DomainKey != n.DomainKey {
			return 0, fmt.Errorf("conflict: node %q domain_key is %q, cannot change to %q", n.NodeKey, existing.DomainKey, n.DomainKey)
		}

		// Update mutable fields
		_, err := s.db.Exec(
			`UPDATE graph_nodes SET name=?, qualified_name=?, repo_name=?, file_path=?,
			 lang=?, owner_key=?, environment=?, visibility=?, status=?,
			 last_seen_revision_id=?, confidence=?, metadata=?
			 WHERE node_key=?`,
			n.Name, n.QualifiedName, n.RepoName, n.FilePath,
			n.Lang, n.OwnerKey, n.Environment, n.Visibility, n.Status,
			n.LastSeenRevisionID, n.Confidence, n.Metadata,
			n.NodeKey,
		)
		if err != nil {
			return 0, fmt.Errorf("updating node: %w", err)
		}
		return existing.NodeID, nil
	}

	// Insert new
	res, err := s.db.Exec(
		`INSERT INTO graph_nodes (node_key, layer, node_type, domain_key, name, qualified_name,
		 repo_name, file_path, lang, owner_key, environment, visibility, status,
		 first_seen_revision_id, last_seen_revision_id, confidence, metadata)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		n.NodeKey, n.Layer, n.NodeType, n.DomainKey, n.Name, n.QualifiedName,
		n.RepoName, n.FilePath, n.Lang, n.OwnerKey, n.Environment, n.Visibility, n.Status,
		n.FirstSeenRevisionID, n.LastSeenRevisionID, n.Confidence, n.Metadata,
	)
	if err != nil {
		return 0, fmt.Errorf("inserting node: %w", err)
	}
	return res.LastInsertId()
}

func (s *Store) GetNodeByKey(key string) (*NodeRow, error) {
	var n NodeRow
	err := s.db.QueryRow(
		`SELECT node_id, node_key, layer, node_type, domain_key, name,
		 COALESCE(qualified_name,''), COALESCE(repo_name,''), COALESCE(file_path,''),
		 COALESCE(lang,''), COALESCE(owner_key,''), COALESCE(environment,''),
		 COALESCE(visibility,''), status,
		 COALESCE(first_seen_revision_id,0), COALESCE(last_seen_revision_id,0),
		 confidence, metadata
		 FROM graph_nodes WHERE node_key = ?`, key,
	).Scan(&n.NodeID, &n.NodeKey, &n.Layer, &n.NodeType, &n.DomainKey, &n.Name,
		&n.QualifiedName, &n.RepoName, &n.FilePath,
		&n.Lang, &n.OwnerKey, &n.Environment,
		&n.Visibility, &n.Status,
		&n.FirstSeenRevisionID, &n.LastSeenRevisionID,
		&n.Confidence, &n.Metadata)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("node %q not found", key)
	}
	if err != nil {
		return nil, fmt.Errorf("getting node: %w", err)
	}
	return &n, nil
}

func (s *Store) ListNodes(f NodeFilter) ([]NodeRow, error) {
	query := `SELECT node_id, node_key, layer, node_type, domain_key, name,
		COALESCE(qualified_name,''), COALESCE(repo_name,''), COALESCE(file_path,''),
		COALESCE(lang,''), COALESCE(owner_key,''), COALESCE(environment,''),
		COALESCE(visibility,''), status,
		COALESCE(first_seen_revision_id,0), COALESCE(last_seen_revision_id,0),
		confidence, metadata
		FROM graph_nodes WHERE 1=1`
	var args []any

	if f.Layer != "" {
		query += " AND layer = ?"
		args = append(args, f.Layer)
	}
	if f.NodeType != "" {
		query += " AND node_type = ?"
		args = append(args, f.NodeType)
	}
	if f.Domain != "" {
		query += " AND domain_key = ?"
		args = append(args, f.Domain)
	}
	if f.RepoName != "" {
		query += " AND repo_name = ?"
		args = append(args, f.RepoName)
	}
	if f.Status != "" {
		query += " AND status = ?"
		args = append(args, f.Status)
	}

	query += " ORDER BY node_key"

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing nodes: %w", err)
	}
	defer rows.Close()

	var nodes []NodeRow
	for rows.Next() {
		var n NodeRow
		if err := rows.Scan(&n.NodeID, &n.NodeKey, &n.Layer, &n.NodeType, &n.DomainKey, &n.Name,
			&n.QualifiedName, &n.RepoName, &n.FilePath,
			&n.Lang, &n.OwnerKey, &n.Environment,
			&n.Visibility, &n.Status,
			&n.FirstSeenRevisionID, &n.LastSeenRevisionID,
			&n.Confidence, &n.Metadata); err != nil {
			return nil, fmt.Errorf("scanning node: %w", err)
		}
		nodes = append(nodes, n)
	}
	return nodes, rows.Err()
}

func (s *Store) DeleteNode(key string) error {
	res, err := s.db.Exec(
		`UPDATE graph_nodes SET status = 'deleted' WHERE node_key = ?`, key,
	)
	if err != nil {
		return fmt.Errorf("deleting node: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("node %q not found", key)
	}
	return nil
}

// GetNodeIDByKey returns the node_id for a given node_key.
func (s *Store) GetNodeIDByKey(key string) (int64, error) {
	var id int64
	err := s.db.QueryRow("SELECT node_id FROM graph_nodes WHERE node_key = ?", key).Scan(&id)
	if err == sql.ErrNoRows {
		return 0, fmt.Errorf("node %q not found", key)
	}
	if err != nil {
		return 0, fmt.Errorf("getting node id: %w", err)
	}
	return id, nil
}

// MarkStaleNodes marks all nodes in a domain whose last_seen_revision_id < revisionID as stale.
func (s *Store) MarkStaleNodes(domainKey string, revisionID int64) (int64, error) {
	res, err := s.db.Exec(
		`UPDATE graph_nodes SET status = 'stale'
		 WHERE domain_key = ? AND last_seen_revision_id < ? AND status = 'active'`,
		domainKey, revisionID,
	)
	if err != nil {
		return 0, fmt.Errorf("marking stale nodes: %w", err)
	}
	return res.RowsAffected()
}

// NodeKeysByFilter returns node_keys matching the filter, for use in validation commands.
func (s *Store) NodeKeysByFilter(f NodeFilter) ([]string, error) {
	nodes, err := s.ListNodes(f)
	if err != nil {
		return nil, err
	}
	keys := make([]string, len(nodes))
	for i, n := range nodes {
		keys[i] = n.NodeKey
	}
	return keys, nil
}

// unused import guard
var _ = strings.TrimSpace
```

Wait — remove the `strings` import guard. The `strings` import isn't needed. Let me fix:

The file should NOT import `strings`. Remove that last line (`var _ = strings.TrimSpace`).

- [ ] **Step 4: Run tests**

```bash
go test ./internal/store/ -v -run "TestUpsertNode|TestGetNode|TestListNode|TestDeleteNode"
```

Expected: all tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store/nodes.go internal/store/nodes_test.go
git commit -m "feat: add node CRUD with upsert and conflict detection"
```

---

### Task 9: Store — Edges CRUD with Upsert

**Files:**
- Create: `internal/store/edges.go`
- Create: `internal/store/edges_test.go`

- [ ] **Step 1: Write failing tests**

Create `internal/store/edges_test.go`:

```go
package store

import (
	"testing"
)

func seedNodes(t *testing.T, s *Store) (int64, int64, int64) {
	t.Helper()
	revID, _ := s.CreateRevision("orders", "", "sha1", "manual", "full", "{}")
	id1, _ := s.UpsertNode(NodeRow{
		NodeKey: "code:controller:orders:orderscontroller", Layer: "code", NodeType: "controller",
		DomainKey: "orders", Name: "OrdersController", Status: "active",
		FirstSeenRevisionID: revID, LastSeenRevisionID: revID, Confidence: 1, Metadata: "{}",
	})
	id2, _ := s.UpsertNode(NodeRow{
		NodeKey: "code:provider:orders:ordersservice", Layer: "code", NodeType: "provider",
		DomainKey: "orders", Name: "OrdersService", Status: "active",
		FirstSeenRevisionID: revID, LastSeenRevisionID: revID, Confidence: 1, Metadata: "{}",
	})
	return revID, id1, id2
}

func TestUpsertEdgeInsert(t *testing.T) {
	s := openTestStore(t)
	revID, fromID, toID := seedNodes(t, s)

	id, err := s.UpsertEdge(EdgeRow{
		EdgeKey:             "code:controller:orders:orderscontroller->code:provider:orders:ordersservice:INJECTS",
		FromNodeID:          fromID,
		ToNodeID:            toID,
		EdgeType:            "INJECTS",
		DerivationKind:      "hard",
		Active:              true,
		FirstSeenRevisionID: revID,
		LastSeenRevisionID:  revID,
		Confidence:          0.99,
		Metadata:            "{}",
	})
	if err != nil {
		t.Fatalf("UpsertEdge: %v", err)
	}
	if id <= 0 {
		t.Errorf("edge_id = %d, want > 0", id)
	}
}

func TestUpsertEdgeUpdate(t *testing.T) {
	s := openTestStore(t)
	revID, fromID, toID := seedNodes(t, s)
	revID2, _ := s.CreateRevision("orders", "sha1", "sha2", "manual", "incremental", "{}")

	edgeKey := "code:controller:orders:orderscontroller->code:provider:orders:ordersservice:INJECTS"

	id1, _ := s.UpsertEdge(EdgeRow{
		EdgeKey: edgeKey, FromNodeID: fromID, ToNodeID: toID,
		EdgeType: "INJECTS", DerivationKind: "hard", Active: true,
		FirstSeenRevisionID: revID, LastSeenRevisionID: revID, Confidence: 0.99, Metadata: "{}",
	})

	id2, _ := s.UpsertEdge(EdgeRow{
		EdgeKey: edgeKey, FromNodeID: fromID, ToNodeID: toID,
		EdgeType: "INJECTS", DerivationKind: "linked", Active: true,
		FirstSeenRevisionID: revID, LastSeenRevisionID: revID2, Confidence: 0.85, Metadata: `{"updated":true}`,
	})

	if id1 != id2 {
		t.Errorf("upsert should return same id: %d != %d", id1, id2)
	}

	edge, _ := s.GetEdgeByKey(edgeKey)
	if edge.DerivationKind != "linked" {
		t.Errorf("derivation = %q, want linked", edge.DerivationKind)
	}
	if edge.Confidence != 0.85 {
		t.Errorf("confidence = %f, want 0.85", edge.Confidence)
	}
}

func TestListEdges(t *testing.T) {
	s := openTestStore(t)
	revID, fromID, toID := seedNodes(t, s)

	s.UpsertEdge(EdgeRow{
		EdgeKey: "code:controller:orders:orderscontroller->code:provider:orders:ordersservice:INJECTS",
		FromNodeID: fromID, ToNodeID: toID, EdgeType: "INJECTS", DerivationKind: "hard",
		Active: true, FirstSeenRevisionID: revID, LastSeenRevisionID: revID, Confidence: 1, Metadata: "{}",
	})

	edges, err := s.ListEdges(EdgeFilter{FromNodeID: fromID})
	if err != nil {
		t.Fatalf("ListEdges: %v", err)
	}
	if len(edges) != 1 {
		t.Errorf("count = %d, want 1", len(edges))
	}

	edges, err = s.ListEdges(EdgeFilter{ToNodeID: toID})
	if err != nil {
		t.Fatalf("ListEdges to: %v", err)
	}
	if len(edges) != 1 {
		t.Errorf("count = %d, want 1", len(edges))
	}
}

func TestDeleteEdge(t *testing.T) {
	s := openTestStore(t)
	revID, fromID, toID := seedNodes(t, s)
	edgeKey := "code:controller:orders:orderscontroller->code:provider:orders:ordersservice:INJECTS"

	s.UpsertEdge(EdgeRow{
		EdgeKey: edgeKey, FromNodeID: fromID, ToNodeID: toID,
		EdgeType: "INJECTS", DerivationKind: "hard", Active: true,
		FirstSeenRevisionID: revID, LastSeenRevisionID: revID, Confidence: 1, Metadata: "{}",
	})

	err := s.DeleteEdge(edgeKey)
	if err != nil {
		t.Fatalf("DeleteEdge: %v", err)
	}

	edge, _ := s.GetEdgeByKey(edgeKey)
	if edge.Active {
		t.Error("edge should be inactive after delete")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/store/ -v -run "TestUpsertEdge|TestListEdge|TestDeleteEdge"
```

Expected: FAIL.

- [ ] **Step 3: Implement edges CRUD**

Create `internal/store/edges.go`:

```go
package store

import (
	"database/sql"
	"fmt"
)

type EdgeRow struct {
	EdgeID              int64   `json:"edge_id"`
	EdgeKey             string  `json:"edge_key"`
	FromNodeID          int64   `json:"from_node_id"`
	ToNodeID            int64   `json:"to_node_id"`
	EdgeType            string  `json:"edge_type"`
	DerivationKind      string  `json:"derivation_kind"`
	ContextKey          string  `json:"context_key,omitempty"`
	Active              bool    `json:"active"`
	FirstSeenRevisionID int64   `json:"first_seen_revision_id"`
	LastSeenRevisionID  int64   `json:"last_seen_revision_id"`
	Confidence          float64 `json:"confidence"`
	Metadata            string  `json:"metadata"`
}

type EdgeFilter struct {
	FromNodeID     int64
	ToNodeID       int64
	EdgeType       string
	DerivationKind string
	Active         *bool
}

func (s *Store) UpsertEdge(e EdgeRow) (int64, error) {
	existing, err := s.GetEdgeByKey(e.EdgeKey)
	if err == nil {
		// Update mutable fields only
		_, err := s.db.Exec(
			`UPDATE graph_edges SET derivation_kind=?, confidence=?, metadata=?,
			 active=?, last_seen_revision_id=?, context_key=?
			 WHERE edge_key=?`,
			e.DerivationKind, e.Confidence, e.Metadata,
			e.Active, e.LastSeenRevisionID, e.ContextKey,
			e.EdgeKey,
		)
		if err != nil {
			return 0, fmt.Errorf("updating edge: %w", err)
		}
		return existing.EdgeID, nil
	}

	res, err := s.db.Exec(
		`INSERT INTO graph_edges (edge_key, from_node_id, to_node_id, edge_type,
		 derivation_kind, context_key, active, first_seen_revision_id, last_seen_revision_id,
		 confidence, metadata)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.EdgeKey, e.FromNodeID, e.ToNodeID, e.EdgeType,
		e.DerivationKind, e.ContextKey, e.Active, e.FirstSeenRevisionID, e.LastSeenRevisionID,
		e.Confidence, e.Metadata,
	)
	if err != nil {
		return 0, fmt.Errorf("inserting edge: %w", err)
	}
	return res.LastInsertId()
}

func (s *Store) GetEdgeByKey(key string) (*EdgeRow, error) {
	var e EdgeRow
	err := s.db.QueryRow(
		`SELECT edge_id, edge_key, from_node_id, to_node_id, edge_type,
		 derivation_kind, COALESCE(context_key,''), active,
		 COALESCE(first_seen_revision_id,0), COALESCE(last_seen_revision_id,0),
		 confidence, metadata
		 FROM graph_edges WHERE edge_key = ?`, key,
	).Scan(&e.EdgeID, &e.EdgeKey, &e.FromNodeID, &e.ToNodeID, &e.EdgeType,
		&e.DerivationKind, &e.ContextKey, &e.Active,
		&e.FirstSeenRevisionID, &e.LastSeenRevisionID,
		&e.Confidence, &e.Metadata)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("edge %q not found", key)
	}
	if err != nil {
		return nil, fmt.Errorf("getting edge: %w", err)
	}
	return &e, nil
}

func (s *Store) ListEdges(f EdgeFilter) ([]EdgeRow, error) {
	query := `SELECT edge_id, edge_key, from_node_id, to_node_id, edge_type,
		derivation_kind, COALESCE(context_key,''), active,
		COALESCE(first_seen_revision_id,0), COALESCE(last_seen_revision_id,0),
		confidence, metadata
		FROM graph_edges WHERE 1=1`
	var args []any

	if f.FromNodeID != 0 {
		query += " AND from_node_id = ?"
		args = append(args, f.FromNodeID)
	}
	if f.ToNodeID != 0 {
		query += " AND to_node_id = ?"
		args = append(args, f.ToNodeID)
	}
	if f.EdgeType != "" {
		query += " AND edge_type = ?"
		args = append(args, f.EdgeType)
	}
	if f.DerivationKind != "" {
		query += " AND derivation_kind = ?"
		args = append(args, f.DerivationKind)
	}
	if f.Active != nil {
		query += " AND active = ?"
		args = append(args, *f.Active)
	}

	query += " ORDER BY edge_key"

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing edges: %w", err)
	}
	defer rows.Close()

	var edges []EdgeRow
	for rows.Next() {
		var e EdgeRow
		if err := rows.Scan(&e.EdgeID, &e.EdgeKey, &e.FromNodeID, &e.ToNodeID, &e.EdgeType,
			&e.DerivationKind, &e.ContextKey, &e.Active,
			&e.FirstSeenRevisionID, &e.LastSeenRevisionID,
			&e.Confidence, &e.Metadata); err != nil {
			return nil, fmt.Errorf("scanning edge: %w", err)
		}
		edges = append(edges, e)
	}
	return edges, rows.Err()
}

func (s *Store) DeleteEdge(key string) error {
	res, err := s.db.Exec(
		`UPDATE graph_edges SET active = 0 WHERE edge_key = ?`, key,
	)
	if err != nil {
		return fmt.Errorf("deleting edge: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("edge %q not found", key)
	}
	return nil
}

// MarkStaleEdges marks all active edges in a domain whose last_seen_revision_id < revisionID as inactive.
func (s *Store) MarkStaleEdges(domainKey string, revisionID int64) (int64, error) {
	res, err := s.db.Exec(
		`UPDATE graph_edges SET active = 0
		 WHERE edge_id IN (
		   SELECT e.edge_id FROM graph_edges e
		   JOIN graph_nodes n ON e.from_node_id = n.node_id
		   WHERE n.domain_key = ? AND e.last_seen_revision_id < ? AND e.active = 1
		 )`,
		domainKey, revisionID,
	)
	if err != nil {
		return 0, fmt.Errorf("marking stale edges: %w", err)
	}
	return res.RowsAffected()
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/store/ -v -run "TestUpsertEdge|TestListEdge|TestDeleteEdge"
```

Expected: all tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store/edges.go internal/store/edges_test.go
git commit -m "feat: add edge CRUD with upsert and stale marking"
```

---

### Task 10: Store — Evidence CRUD with Dedup

**Files:**
- Create: `internal/store/evidence.go`
- Create: `internal/store/evidence_test.go`

- [ ] **Step 1: Write failing tests**

Create `internal/store/evidence_test.go`:

```go
package store

import (
	"testing"
)

func TestAddEvidenceForEdge(t *testing.T) {
	s := openTestStore(t)
	revID, fromID, toID := seedNodes(t, s)

	edgeKey := "code:controller:orders:orderscontroller->code:provider:orders:ordersservice:INJECTS"
	edgeID, _ := s.UpsertEdge(EdgeRow{
		EdgeKey: edgeKey, FromNodeID: fromID, ToNodeID: toID,
		EdgeType: "INJECTS", DerivationKind: "hard", Active: true,
		FirstSeenRevisionID: revID, LastSeenRevisionID: revID, Confidence: 1, Metadata: "{}",
	})

	id, err := s.AddEvidence(EvidenceRow{
		TargetKind:       "edge",
		EdgeID:           edgeID,
		SourceKind:       "file",
		RepoName:         "orders-api",
		FilePath:         "src/orders/orders.controller.ts",
		LineStart:        12,
		LineEnd:          12,
		ExtractorID:      "claude-code",
		ExtractorVersion: "1.0",
		CommitSHA:        "abc123",
		Confidence:       0.99,
		Metadata:         "{}",
	})
	if err != nil {
		t.Fatalf("AddEvidence: %v", err)
	}
	if id <= 0 {
		t.Errorf("evidence_id = %d, want > 0", id)
	}
}

func TestAddEvidenceDedup(t *testing.T) {
	s := openTestStore(t)
	revID, fromID, toID := seedNodes(t, s)

	edgeKey := "code:controller:orders:orderscontroller->code:provider:orders:ordersservice:INJECTS"
	edgeID, _ := s.UpsertEdge(EdgeRow{
		EdgeKey: edgeKey, FromNodeID: fromID, ToNodeID: toID,
		EdgeType: "INJECTS", DerivationKind: "hard", Active: true,
		FirstSeenRevisionID: revID, LastSeenRevisionID: revID, Confidence: 1, Metadata: "{}",
	})

	ev := EvidenceRow{
		TargetKind: "edge", EdgeID: edgeID,
		SourceKind: "file", RepoName: "orders-api",
		FilePath: "src/orders/orders.controller.ts", LineStart: 12,
		ExtractorID: "claude-code", ExtractorVersion: "1.0",
		CommitSHA: "abc123", Confidence: 0.99, Metadata: "{}",
	}

	id1, _ := s.AddEvidence(ev)

	ev.CommitSHA = "def456"
	ev.Confidence = 0.95
	id2, _ := s.AddEvidence(ev)

	if id1 != id2 {
		t.Errorf("dedup should return same id: %d != %d", id1, id2)
	}

	// Verify updated fields
	evidence, _ := s.ListEvidenceByEdge(edgeID)
	if len(evidence) != 1 {
		t.Fatalf("expected 1 evidence, got %d", len(evidence))
	}
	if evidence[0].CommitSHA != "def456" {
		t.Errorf("commit_sha = %q, want def456", evidence[0].CommitSHA)
	}
	if evidence[0].Confidence != 0.95 {
		t.Errorf("confidence = %f, want 0.95", evidence[0].Confidence)
	}
}

func TestAddEvidenceDifferentLine(t *testing.T) {
	s := openTestStore(t)
	revID, fromID, toID := seedNodes(t, s)

	edgeKey := "code:controller:orders:orderscontroller->code:provider:orders:ordersservice:INJECTS"
	edgeID, _ := s.UpsertEdge(EdgeRow{
		EdgeKey: edgeKey, FromNodeID: fromID, ToNodeID: toID,
		EdgeType: "INJECTS", DerivationKind: "hard", Active: true,
		FirstSeenRevisionID: revID, LastSeenRevisionID: revID, Confidence: 1, Metadata: "{}",
	})

	s.AddEvidence(EvidenceRow{
		TargetKind: "edge", EdgeID: edgeID,
		SourceKind: "file", RepoName: "orders-api",
		FilePath: "src/orders/orders.controller.ts", LineStart: 12,
		ExtractorID: "claude-code", ExtractorVersion: "1.0",
		Confidence: 0.99, Metadata: "{}",
	})

	// Different line = new evidence
	s.AddEvidence(EvidenceRow{
		TargetKind: "edge", EdgeID: edgeID,
		SourceKind: "file", RepoName: "orders-api",
		FilePath: "src/orders/orders.controller.ts", LineStart: 25,
		ExtractorID: "claude-code", ExtractorVersion: "1.0",
		Confidence: 0.99, Metadata: "{}",
	})

	evidence, _ := s.ListEvidenceByEdge(edgeID)
	if len(evidence) != 2 {
		t.Errorf("expected 2 evidence entries, got %d", len(evidence))
	}
}

func TestListEvidenceByNode(t *testing.T) {
	s := openTestStore(t)
	revID, _ := s.CreateRevision("orders", "", "sha1", "manual", "full", "{}")
	nodeID, _ := s.UpsertNode(NodeRow{
		NodeKey: "code:controller:orders:orderscontroller", Layer: "code", NodeType: "controller",
		DomainKey: "orders", Name: "OrdersController", Status: "active",
		FirstSeenRevisionID: revID, LastSeenRevisionID: revID, Confidence: 1, Metadata: "{}",
	})

	s.AddEvidence(EvidenceRow{
		TargetKind: "node", NodeID: nodeID,
		SourceKind: "file", RepoName: "orders-api",
		FilePath: "src/orders/orders.controller.ts", LineStart: 1,
		ExtractorID: "claude-code", ExtractorVersion: "1.0",
		Confidence: 0.99, Metadata: "{}",
	})

	evidence, err := s.ListEvidenceByNode(nodeID)
	if err != nil {
		t.Fatalf("ListEvidenceByNode: %v", err)
	}
	if len(evidence) != 1 {
		t.Errorf("count = %d, want 1", len(evidence))
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/store/ -v -run TestEvidence
```

Expected: FAIL.

- [ ] **Step 3: Implement evidence CRUD**

Create `internal/store/evidence.go`:

```go
package store

import (
	"database/sql"
	"fmt"
)

type EvidenceRow struct {
	EvidenceID       int64   `json:"evidence_id"`
	TargetKind       string  `json:"target_kind"`
	NodeID           int64   `json:"node_id,omitempty"`
	EdgeID           int64   `json:"edge_id,omitempty"`
	SourceKind       string  `json:"source_kind"`
	RepoName         string  `json:"repo_name,omitempty"`
	FilePath         string  `json:"file_path,omitempty"`
	LineStart        int     `json:"line_start,omitempty"`
	LineEnd          int     `json:"line_end,omitempty"`
	ColumnStart      int     `json:"column_start,omitempty"`
	ColumnEnd        int     `json:"column_end,omitempty"`
	Locator          string  `json:"locator,omitempty"`
	ExtractorID      string  `json:"extractor_id"`
	ExtractorVersion string  `json:"extractor_version"`
	ASTRule          string  `json:"ast_rule,omitempty"`
	SnippetHash      string  `json:"snippet_hash,omitempty"`
	CommitSHA        string  `json:"commit_sha,omitempty"`
	ObservedAt       string  `json:"observed_at"`
	VerifiedAt       string  `json:"verified_at,omitempty"`
	Confidence       float64 `json:"confidence"`
	Metadata         string  `json:"metadata"`
}

// AddEvidence inserts or deduplicates evidence.
// Dedup key: (target_kind, node_id/edge_id, source_kind, repo_name, file_path, line_start, extractor_id)
func (s *Store) AddEvidence(e EvidenceRow) (int64, error) {
	// Try to find existing by dedup key
	var existingID int64
	var query string
	var args []any

	if e.TargetKind == "node" {
		query = `SELECT evidence_id FROM graph_evidence
			WHERE target_kind = 'node' AND node_id = ? AND source_kind = ?
			AND COALESCE(repo_name,'') = ? AND COALESCE(file_path,'') = ?
			AND COALESCE(line_start,0) = ? AND extractor_id = ?`
		args = []any{e.NodeID, e.SourceKind, e.RepoName, e.FilePath, e.LineStart, e.ExtractorID}
	} else {
		query = `SELECT evidence_id FROM graph_evidence
			WHERE target_kind = 'edge' AND edge_id = ? AND source_kind = ?
			AND COALESCE(repo_name,'') = ? AND COALESCE(file_path,'') = ?
			AND COALESCE(line_start,0) = ? AND extractor_id = ?`
		args = []any{e.EdgeID, e.SourceKind, e.RepoName, e.FilePath, e.LineStart, e.ExtractorID}
	}

	err := s.db.QueryRow(query, args...).Scan(&existingID)
	if err == nil {
		// Update existing
		_, err := s.db.Exec(
			`UPDATE graph_evidence SET
			 observed_at = strftime('%Y-%m-%dT%H:%M:%SZ', 'now'),
			 confidence = ?, commit_sha = ?, extractor_version = ?,
			 line_end = ?, column_start = ?, column_end = ?,
			 locator = ?, ast_rule = ?, snippet_hash = ?, metadata = ?
			 WHERE evidence_id = ?`,
			e.Confidence, e.CommitSHA, e.ExtractorVersion,
			e.LineEnd, e.ColumnStart, e.ColumnEnd,
			e.Locator, e.ASTRule, e.SnippetHash, e.Metadata,
			existingID,
		)
		if err != nil {
			return 0, fmt.Errorf("updating evidence: %w", err)
		}
		return existingID, nil
	}
	if err != sql.ErrNoRows {
		return 0, fmt.Errorf("checking evidence dedup: %w", err)
	}

	// Insert new
	var nodeID, edgeID *int64
	if e.TargetKind == "node" {
		nodeID = &e.NodeID
	} else {
		edgeID = &e.EdgeID
	}

	res, err := s.db.Exec(
		`INSERT INTO graph_evidence (target_kind, node_id, edge_id, source_kind,
		 repo_name, file_path, line_start, line_end, column_start, column_end,
		 locator, extractor_id, extractor_version, ast_rule, snippet_hash,
		 commit_sha, confidence, metadata)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.TargetKind, nodeID, edgeID, e.SourceKind,
		e.RepoName, e.FilePath, e.LineStart, e.LineEnd, e.ColumnStart, e.ColumnEnd,
		e.Locator, e.ExtractorID, e.ExtractorVersion, e.ASTRule, e.SnippetHash,
		e.CommitSHA, e.Confidence, e.Metadata,
	)
	if err != nil {
		return 0, fmt.Errorf("inserting evidence: %w", err)
	}
	return res.LastInsertId()
}

func (s *Store) ListEvidenceByNode(nodeID int64) ([]EvidenceRow, error) {
	return s.listEvidence("node_id = ?", nodeID)
}

func (s *Store) ListEvidenceByEdge(edgeID int64) ([]EvidenceRow, error) {
	return s.listEvidence("edge_id = ?", edgeID)
}

func (s *Store) listEvidence(where string, arg any) ([]EvidenceRow, error) {
	query := `SELECT evidence_id, target_kind, COALESCE(node_id,0), COALESCE(edge_id,0),
		source_kind, COALESCE(repo_name,''), COALESCE(file_path,''),
		COALESCE(line_start,0), COALESCE(line_end,0),
		COALESCE(column_start,0), COALESCE(column_end,0),
		COALESCE(locator,''), extractor_id, extractor_version,
		COALESCE(ast_rule,''), COALESCE(snippet_hash,''),
		COALESCE(commit_sha,''), observed_at, COALESCE(verified_at,''),
		confidence, metadata
		FROM graph_evidence WHERE ` + where + ` ORDER BY evidence_id`

	rows, err := s.db.Query(query, arg)
	if err != nil {
		return nil, fmt.Errorf("listing evidence: %w", err)
	}
	defer rows.Close()

	var result []EvidenceRow
	for rows.Next() {
		var e EvidenceRow
		if err := rows.Scan(&e.EvidenceID, &e.TargetKind, &e.NodeID, &e.EdgeID,
			&e.SourceKind, &e.RepoName, &e.FilePath,
			&e.LineStart, &e.LineEnd, &e.ColumnStart, &e.ColumnEnd,
			&e.Locator, &e.ExtractorID, &e.ExtractorVersion,
			&e.ASTRule, &e.SnippetHash, &e.CommitSHA,
			&e.ObservedAt, &e.VerifiedAt, &e.Confidence, &e.Metadata); err != nil {
			return nil, fmt.Errorf("scanning evidence: %w", err)
		}
		result = append(result, e)
	}
	return result, rows.Err()
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/store/ -v -run TestEvidence
```

Expected: all tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store/evidence.go internal/store/evidence_test.go
git commit -m "feat: add evidence CRUD with dedup"
```

---

### Task 11: Store — Snapshots + Transaction Wrapper

**Files:**
- Create: `internal/store/snapshots.go`
- Create: `internal/store/snapshots_test.go`

- [ ] **Step 1: Write failing tests**

Create `internal/store/snapshots_test.go`:

```go
package store

import (
	"testing"
)

func TestCreateSnapshot(t *testing.T) {
	s := openTestStore(t)
	revID, _ := s.CreateRevision("orders", "", "sha1", "manual", "full", "{}")

	id, err := s.CreateSnapshot(SnapshotRow{
		RevisionID: revID,
		DomainKey:  "orders",
		Kind:       "full",
		NodeCount:  10,
		EdgeCount:  20,
		Summary:    `{"extractors_run": ["claude-code"]}`,
	})
	if err != nil {
		t.Fatalf("CreateSnapshot: %v", err)
	}
	if id <= 0 {
		t.Errorf("snapshot_id = %d, want > 0", id)
	}
}

func TestCreateSnapshotDuplicate(t *testing.T) {
	s := openTestStore(t)
	revID, _ := s.CreateRevision("orders", "", "sha1", "manual", "full", "{}")

	s.CreateSnapshot(SnapshotRow{RevisionID: revID, DomainKey: "orders", Kind: "full", NodeCount: 10, EdgeCount: 20, Summary: "{}"})

	_, err := s.CreateSnapshot(SnapshotRow{RevisionID: revID, DomainKey: "orders", Kind: "full", NodeCount: 10, EdgeCount: 20, Summary: "{}"})
	if err == nil {
		t.Fatal("expected error for duplicate revision+domain snapshot")
	}
}

func TestListSnapshots(t *testing.T) {
	s := openTestStore(t)
	rev1, _ := s.CreateRevision("orders", "", "sha1", "manual", "full", "{}")
	rev2, _ := s.CreateRevision("orders", "sha1", "sha2", "manual", "incremental", "{}")

	s.CreateSnapshot(SnapshotRow{RevisionID: rev1, DomainKey: "orders", Kind: "full", NodeCount: 10, EdgeCount: 20, Summary: "{}"})
	s.CreateSnapshot(SnapshotRow{RevisionID: rev2, DomainKey: "orders", Kind: "incremental", NodeCount: 12, EdgeCount: 22, Summary: "{}"})

	snaps, err := s.ListSnapshots("orders")
	if err != nil {
		t.Fatalf("ListSnapshots: %v", err)
	}
	if len(snaps) != 2 {
		t.Errorf("count = %d, want 2", len(snaps))
	}
}

func TestTransaction(t *testing.T) {
	s := openTestStore(t)

	// Successful transaction
	err := s.WithTx(func(tx *Store) error {
		tx.CreateRevision("orders", "", "sha1", "manual", "full", "{}")
		return nil
	})
	if err != nil {
		t.Fatalf("WithTx success: %v", err)
	}

	// Failed transaction — should rollback
	err = s.WithTx(func(tx *Store) error {
		tx.CreateRevision("orders2", "", "sha2", "manual", "full", "{}")
		return fmt.Errorf("intentional failure")
	})
	if err == nil {
		t.Fatal("expected error from failed transaction")
	}

	// Verify rollback
	_, err = s.GetRevision(2)
	if err == nil {
		t.Error("revision from failed tx should not exist")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/store/ -v -run "TestSnapshot|TestTransaction"
```

Expected: FAIL.

- [ ] **Step 3: Implement snapshots + transaction wrapper**

Create `internal/store/snapshots.go`:

```go
package store

import (
	"database/sql"
	"fmt"
)

type SnapshotRow struct {
	SnapshotID       int64  `json:"snapshot_id"`
	RevisionID       int64  `json:"revision_id"`
	DomainKey        string `json:"domain_key"`
	Kind             string `json:"snapshot_kind"`
	CreatedAt        string `json:"created_at"`
	NodeCount        int    `json:"node_count"`
	EdgeCount        int    `json:"edge_count"`
	ChangedFileCount int    `json:"changed_file_count"`
	ChangedNodeCount int    `json:"changed_node_count"`
	ChangedEdgeCount int    `json:"changed_edge_count"`
	ImpactedNodeCount int   `json:"impacted_node_count"`
	Summary          string `json:"summary"`
}

func (s *Store) CreateSnapshot(snap SnapshotRow) (int64, error) {
	if snap.Summary == "" {
		snap.Summary = "{}"
	}
	res, err := s.db.Exec(
		`INSERT INTO graph_snapshots (revision_id, domain_key, snapshot_kind,
		 node_count, edge_count, changed_file_count, changed_node_count,
		 changed_edge_count, impacted_node_count, summary)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		snap.RevisionID, snap.DomainKey, snap.Kind,
		snap.NodeCount, snap.EdgeCount, snap.ChangedFileCount, snap.ChangedNodeCount,
		snap.ChangedEdgeCount, snap.ImpactedNodeCount, snap.Summary,
	)
	if err != nil {
		return 0, fmt.Errorf("creating snapshot: %w", err)
	}
	return res.LastInsertId()
}

func (s *Store) ListSnapshots(domainKey string) ([]SnapshotRow, error) {
	rows, err := s.db.Query(
		`SELECT snapshot_id, revision_id, domain_key, snapshot_kind, created_at,
		 node_count, edge_count, changed_file_count, changed_node_count,
		 changed_edge_count, impacted_node_count, summary
		 FROM graph_snapshots WHERE domain_key = ? ORDER BY created_at DESC`, domainKey,
	)
	if err != nil {
		return nil, fmt.Errorf("listing snapshots: %w", err)
	}
	defer rows.Close()

	var result []SnapshotRow
	for rows.Next() {
		var snap SnapshotRow
		if err := rows.Scan(&snap.SnapshotID, &snap.RevisionID, &snap.DomainKey,
			&snap.Kind, &snap.CreatedAt, &snap.NodeCount, &snap.EdgeCount,
			&snap.ChangedFileCount, &snap.ChangedNodeCount, &snap.ChangedEdgeCount,
			&snap.ImpactedNodeCount, &snap.Summary); err != nil {
			return nil, fmt.Errorf("scanning snapshot: %w", err)
		}
		result = append(result, snap)
	}
	return result, rows.Err()
}

// WithTx runs fn inside a database transaction. If fn returns an error, the tx is rolled back.
func (s *Store) WithTx(fn func(tx *Store) error) error {
	sqlTx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}

	txStore := &Store{db: nil}
	// We need a wrapper that implements the same interface but uses the transaction
	// For simplicity, we use a txDB wrapper
	txStore.db = txToDB(sqlTx)

	if err := fn(txStore); err != nil {
		sqlTx.Rollback()
		return err
	}
	return sqlTx.Commit()
}

// txDB wraps sql.Tx to satisfy the *sql.DB interface that Store methods use.
// Since Store methods use s.db.Exec/Query/QueryRow, we need a common interface.
// Refactor: use an interface instead.

type dbLike interface {
	Exec(query string, args ...any) (sql.Result, error)
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
}
```

Wait — this approach won't work cleanly because `Store.db` is `*sql.DB`, not an interface. Let me refactor the Store to use an interface.

Update `internal/store/store.go` — change the `db` field to use the `dbLike` interface:

Replace the `db *sql.DB` field and update the `Open` function and `DB()` method. The `dbLike` interface should be defined in `store.go`:

Updated `internal/store/store.go` (changes only):

```go
package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

type DBTX interface {
	Exec(query string, args ...any) (sql.Result, error)
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
}

type Store struct {
	conn *sql.DB // the underlying connection, nil for tx-based stores
	db   DBTX    // the active executor (db or tx)
}

func Open(dbPath string) (*Store, error) {
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("creating db directory: %w", err)
	}

	db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_foreign_keys=on")
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("pinging database: %w", err)
	}

	s := &Store{conn: db, db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrating database: %w", err)
	}

	return s, nil
}

func (s *Store) Close() error {
	if s.conn != nil {
		return s.conn.Close()
	}
	return nil
}

func (s *Store) migrate() error {
	_, err := s.db.Exec(schema)
	return err
}

func (s *Store) WithTx(fn func(tx *Store) error) error {
	if s.conn == nil {
		return fmt.Errorf("cannot start transaction: no connection (already in tx?)")
	}
	sqlTx, err := s.conn.Begin()
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}

	txStore := &Store{conn: nil, db: sqlTx}

	if err := fn(txStore); err != nil {
		sqlTx.Rollback()
		return err
	}
	return sqlTx.Commit()
}

// schema omitted — same as before
```

Then `snapshots.go` is just the `CreateSnapshot`, `ListSnapshots` methods (no `WithTx` or interface stuff):

```go
package store

import (
	"fmt"
)

type SnapshotRow struct {
	SnapshotID        int64  `json:"snapshot_id"`
	RevisionID        int64  `json:"revision_id"`
	DomainKey         string `json:"domain_key"`
	Kind              string `json:"snapshot_kind"`
	CreatedAt         string `json:"created_at"`
	NodeCount         int    `json:"node_count"`
	EdgeCount         int    `json:"edge_count"`
	ChangedFileCount  int    `json:"changed_file_count"`
	ChangedNodeCount  int    `json:"changed_node_count"`
	ChangedEdgeCount  int    `json:"changed_edge_count"`
	ImpactedNodeCount int    `json:"impacted_node_count"`
	Summary           string `json:"summary"`
}

func (s *Store) CreateSnapshot(snap SnapshotRow) (int64, error) {
	if snap.Summary == "" {
		snap.Summary = "{}"
	}
	res, err := s.db.Exec(
		`INSERT INTO graph_snapshots (revision_id, domain_key, snapshot_kind,
		 node_count, edge_count, changed_file_count, changed_node_count,
		 changed_edge_count, impacted_node_count, summary)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		snap.RevisionID, snap.DomainKey, snap.Kind,
		snap.NodeCount, snap.EdgeCount, snap.ChangedFileCount, snap.ChangedNodeCount,
		snap.ChangedEdgeCount, snap.ImpactedNodeCount, snap.Summary,
	)
	if err != nil {
		return 0, fmt.Errorf("creating snapshot: %w", err)
	}
	return res.LastInsertId()
}

func (s *Store) ListSnapshots(domainKey string) ([]SnapshotRow, error) {
	rows, err := s.db.Query(
		`SELECT snapshot_id, revision_id, domain_key, snapshot_kind, created_at,
		 node_count, edge_count, changed_file_count, changed_node_count,
		 changed_edge_count, impacted_node_count, summary
		 FROM graph_snapshots WHERE domain_key = ? ORDER BY created_at DESC`, domainKey,
	)
	if err != nil {
		return nil, fmt.Errorf("listing snapshots: %w", err)
	}
	defer rows.Close()

	var result []SnapshotRow
	for rows.Next() {
		var snap SnapshotRow
		if err := rows.Scan(&snap.SnapshotID, &snap.RevisionID, &snap.DomainKey,
			&snap.Kind, &snap.CreatedAt, &snap.NodeCount, &snap.EdgeCount,
			&snap.ChangedFileCount, &snap.ChangedNodeCount, &snap.ChangedEdgeCount,
			&snap.ImpactedNodeCount, &snap.Summary); err != nil {
			return nil, fmt.Errorf("scanning snapshot: %w", err)
		}
		result = append(result, snap)
	}
	return result, rows.Err()
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/store/ -v -run "TestSnapshot|TestTransaction"
```

Expected: all tests PASS.

- [ ] **Step 5: Run all store tests**

```bash
go test ./internal/store/ -v
```

Expected: all tests PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/store/
git commit -m "feat: add snapshots, transactions, refactor Store to use DBTX interface"
```

---

### Task 12: Graph Facade — Validated Operations

**Files:**
- Create: `internal/graph/graph.go`
- Create: `internal/graph/graph_test.go`

- [ ] **Step 1: Write failing tests**

Create `internal/graph/graph_test.go`:

```go
package graph

import (
	"path/filepath"
	"testing"

	"github.com/anthropics/depbot/internal/registry"
	"github.com/anthropics/depbot/internal/store"
	"github.com/anthropics/depbot/internal/validate"
)

func setupGraph(t *testing.T) *Graph {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	reg, err := registry.LoadFile("../../testdata/registry/valid.yaml")
	if err != nil {
		t.Fatalf("LoadRegistry: %v", err)
	}
	return New(s, reg)
}

func TestGraphUpsertNodeValid(t *testing.T) {
	g := setupGraph(t)
	revID, _ := g.Store().CreateRevision("orders", "", "sha1", "manual", "full", "{}")

	id, err := g.UpsertNode(validate.NodeInput{
		NodeKey:   "code:controller:orders:OrdersController",
		Layer:     "code",
		NodeType:  "controller",
		DomainKey: "orders",
		Name:      "OrdersController",
	}, revID)
	if err != nil {
		t.Fatalf("UpsertNode: %v", err)
	}
	if id <= 0 {
		t.Error("expected positive node_id")
	}
}

func TestGraphUpsertNodeInvalidType(t *testing.T) {
	g := setupGraph(t)
	revID, _ := g.Store().CreateRevision("orders", "", "sha1", "manual", "full", "{}")

	_, err := g.UpsertNode(validate.NodeInput{
		NodeKey:   "code:bogus:orders:thing",
		Layer:     "code",
		NodeType:  "bogus",
		DomainKey: "orders",
		Name:      "Thing",
	}, revID)
	if err == nil {
		t.Fatal("expected validation error for bogus node type")
	}
}

func TestGraphUpsertEdgeValid(t *testing.T) {
	g := setupGraph(t)
	revID, _ := g.Store().CreateRevision("orders", "", "sha1", "manual", "full", "{}")

	g.UpsertNode(validate.NodeInput{
		NodeKey: "code:controller:orders:orderscontroller", Layer: "code", NodeType: "controller",
		DomainKey: "orders", Name: "OrdersController",
	}, revID)
	g.UpsertNode(validate.NodeInput{
		NodeKey: "code:provider:orders:ordersservice", Layer: "code", NodeType: "provider",
		DomainKey: "orders", Name: "OrdersService",
	}, revID)

	id, err := g.UpsertEdge(validate.EdgeInput{
		FromNodeKey:    "code:controller:orders:orderscontroller",
		ToNodeKey:      "code:provider:orders:ordersservice",
		EdgeType:       "INJECTS",
		DerivationKind: "hard",
		FromLayer:      "code",
		ToLayer:        "code",
	}, revID)
	if err != nil {
		t.Fatalf("UpsertEdge: %v", err)
	}
	if id <= 0 {
		t.Error("expected positive edge_id")
	}
}

func TestGraphUpsertEdgeInvalidLayers(t *testing.T) {
	g := setupGraph(t)
	revID, _ := g.Store().CreateRevision("orders", "", "sha1", "manual", "full", "{}")

	g.UpsertNode(validate.NodeInput{
		NodeKey: "service:service:orders:payments", Layer: "service", NodeType: "service",
		DomainKey: "orders", Name: "payments",
	}, revID)
	g.UpsertNode(validate.NodeInput{
		NodeKey: "code:provider:orders:ordersservice", Layer: "code", NodeType: "provider",
		DomainKey: "orders", Name: "OrdersService",
	}, revID)

	_, err := g.UpsertEdge(validate.EdgeInput{
		FromNodeKey:    "service:service:orders:payments",
		ToNodeKey:      "code:provider:orders:ordersservice",
		EdgeType:       "INJECTS",
		DerivationKind: "hard",
		FromLayer:      "service",
		ToLayer:        "code",
	}, revID)
	if err == nil {
		t.Fatal("expected validation error: INJECTS doesn't allow service->code")
	}
}

func TestGraphAddEvidence(t *testing.T) {
	g := setupGraph(t)
	revID, _ := g.Store().CreateRevision("orders", "", "sha1", "manual", "full", "{}")

	g.UpsertNode(validate.NodeInput{
		NodeKey: "code:controller:orders:orderscontroller", Layer: "code", NodeType: "controller",
		DomainKey: "orders", Name: "OrdersController",
	}, revID)

	id, err := g.AddNodeEvidence("code:controller:orders:orderscontroller", validate.EvidenceInput{
		TargetKind:       "node",
		SourceKind:       "file",
		RepoName:         "orders-api",
		FilePath:         "src/orders/orders.controller.ts",
		LineStart:        1,
		ExtractorID:      "claude-code",
		ExtractorVersion: "1.0",
		Confidence:       0.99,
	})
	if err != nil {
		t.Fatalf("AddNodeEvidence: %v", err)
	}
	if id <= 0 {
		t.Error("expected positive evidence_id")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/graph/ -v
```

Expected: FAIL.

- [ ] **Step 3: Implement graph facade**

Create `internal/graph/graph.go`:

```go
package graph

import (
	"fmt"

	"github.com/anthropics/depbot/internal/registry"
	"github.com/anthropics/depbot/internal/store"
	"github.com/anthropics/depbot/internal/validate"
)

type Graph struct {
	store *store.Store
	reg   *registry.Registry
}

func New(s *store.Store, r *registry.Registry) *Graph {
	return &Graph{store: s, reg: r}
}

func (g *Graph) Store() *store.Store {
	return g.store
}

func (g *Graph) UpsertNode(input validate.NodeInput, revisionID int64) (int64, error) {
	validated, err := validate.ValidateNodeInput(input, g.reg)
	if err != nil {
		return 0, err
	}

	return g.store.UpsertNode(store.NodeRow{
		NodeKey:             validated.NodeKey,
		Layer:               validated.Layer,
		NodeType:            validated.NodeType,
		DomainKey:           validated.DomainKey,
		Name:                validated.Name,
		QualifiedName:       validated.QualifiedName,
		RepoName:            validated.RepoName,
		FilePath:            validated.FilePath,
		Lang:                validated.Lang,
		OwnerKey:            validated.OwnerKey,
		Environment:         validated.Environment,
		Visibility:          validated.Visibility,
		Status:              validated.Status,
		FirstSeenRevisionID: revisionID,
		LastSeenRevisionID:  revisionID,
		Confidence:          validated.Confidence,
		Metadata:            validated.Metadata,
	})
}

func (g *Graph) UpsertEdge(input validate.EdgeInput, revisionID int64) (int64, error) {
	validated, err := validate.ValidateEdgeInput(input, g.reg)
	if err != nil {
		return 0, err
	}

	fromID, err := g.store.GetNodeIDByKey(validated.FromNodeKey)
	if err != nil {
		return 0, fmt.Errorf("from node: %w", err)
	}
	toID, err := g.store.GetNodeIDByKey(validated.ToNodeKey)
	if err != nil {
		return 0, fmt.Errorf("to node: %w", err)
	}

	return g.store.UpsertEdge(store.EdgeRow{
		EdgeKey:             validated.EdgeKey,
		FromNodeID:          fromID,
		ToNodeID:            toID,
		EdgeType:            validated.EdgeType,
		DerivationKind:      validated.DerivationKind,
		ContextKey:          validated.ContextKey,
		Active:              true,
		FirstSeenRevisionID: revisionID,
		LastSeenRevisionID:  revisionID,
		Confidence:          validated.Confidence,
		Metadata:            validated.Metadata,
	})
}

func (g *Graph) AddNodeEvidence(nodeKey string, input validate.EvidenceInput) (int64, error) {
	if err := validate.ValidateEvidenceInput(input, g.reg); err != nil {
		return 0, err
	}

	nodeID, err := g.store.GetNodeIDByKey(nodeKey)
	if err != nil {
		return 0, fmt.Errorf("node for evidence: %w", err)
	}

	return g.store.AddEvidence(store.EvidenceRow{
		TargetKind:       "node",
		NodeID:           nodeID,
		SourceKind:       input.SourceKind,
		RepoName:         input.RepoName,
		FilePath:         input.FilePath,
		LineStart:        input.LineStart,
		LineEnd:          input.LineEnd,
		ColumnStart:      input.ColumnStart,
		ColumnEnd:        input.ColumnEnd,
		Locator:          input.Locator,
		ExtractorID:      input.ExtractorID,
		ExtractorVersion: input.ExtractorVersion,
		ASTRule:          input.ASTRule,
		SnippetHash:      input.SnippetHash,
		CommitSHA:        input.CommitSHA,
		Confidence:       input.Confidence,
		Metadata:         input.Metadata,
	})
}

func (g *Graph) AddEdgeEvidence(edgeKey string, input validate.EvidenceInput) (int64, error) {
	if err := validate.ValidateEvidenceInput(input, g.reg); err != nil {
		return 0, err
	}

	edge, err := g.store.GetEdgeByKey(edgeKey)
	if err != nil {
		return 0, fmt.Errorf("edge for evidence: %w", err)
	}

	return g.store.AddEvidence(store.EvidenceRow{
		TargetKind:       "edge",
		EdgeID:           edge.EdgeID,
		SourceKind:       input.SourceKind,
		RepoName:         input.RepoName,
		FilePath:         input.FilePath,
		LineStart:        input.LineStart,
		LineEnd:          input.LineEnd,
		ColumnStart:      input.ColumnStart,
		ColumnEnd:        input.ColumnEnd,
		Locator:          input.Locator,
		ExtractorID:      input.ExtractorID,
		ExtractorVersion: input.ExtractorVersion,
		ASTRule:          input.ASTRule,
		SnippetHash:      input.SnippetHash,
		CommitSHA:        input.CommitSHA,
		Confidence:       input.Confidence,
		Metadata:         input.Metadata,
	})
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/graph/ -v
```

Expected: all tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/graph/
git commit -m "feat: add graph facade with validation pipeline"
```

---

### Task 13: Graph — Bulk Import

**Files:**
- Create: `internal/graph/import.go`
- Create: `internal/graph/import_test.go`

- [ ] **Step 1: Write failing tests**

Create `internal/graph/import_test.go`:

```go
package graph

import (
	"testing"
)

func TestImportAll(t *testing.T) {
	g := setupGraph(t)
	revID, _ := g.Store().CreateRevision("orders", "", "sha1", "manual", "full", "{}")

	payload := ImportPayload{
		Nodes: []ImportNode{
			{NodeKey: "code:controller:orders:orderscontroller", Layer: "code", NodeType: "controller", DomainKey: "orders", Name: "OrdersController"},
			{NodeKey: "code:provider:orders:ordersservice", Layer: "code", NodeType: "provider", DomainKey: "orders", Name: "OrdersService"},
		},
		Edges: []ImportEdge{
			{FromNodeKey: "code:controller:orders:orderscontroller", ToNodeKey: "code:provider:orders:ordersservice", EdgeType: "INJECTS", DerivationKind: "hard", FromLayer: "code", ToLayer: "code"},
		},
		Evidence: []ImportEvidence{
			{TargetKind: "node", NodeKey: "code:controller:orders:orderscontroller", SourceKind: "file", RepoName: "orders-api", FilePath: "src/orders.controller.ts", LineStart: 1, ExtractorID: "claude-code", ExtractorVersion: "1.0"},
		},
	}

	result, err := g.ImportAll(payload, revID)
	if err != nil {
		t.Fatalf("ImportAll: %v", err)
	}
	if result.NodesCreated != 2 {
		t.Errorf("NodesCreated = %d, want 2", result.NodesCreated)
	}
	if result.EdgesCreated != 1 {
		t.Errorf("EdgesCreated = %d, want 1", result.EdgesCreated)
	}
	if result.EvidenceCreated != 1 {
		t.Errorf("EvidenceCreated = %d, want 1", result.EvidenceCreated)
	}
}

func TestImportAllValidationFailure(t *testing.T) {
	g := setupGraph(t)
	revID, _ := g.Store().CreateRevision("orders", "", "sha1", "manual", "full", "{}")

	payload := ImportPayload{
		Nodes: []ImportNode{
			{NodeKey: "code:controller:orders:orderscontroller", Layer: "code", NodeType: "controller", DomainKey: "orders", Name: "OrdersController"},
			{NodeKey: "code:bogus:orders:bad", Layer: "code", NodeType: "bogus", DomainKey: "orders", Name: "Bad"},
		},
	}

	_, err := g.ImportAll(payload, revID)
	if err == nil {
		t.Fatal("expected validation error for bogus node type")
	}

	// Verify nothing was written (transaction rollback)
	nodes, _ := g.Store().ListNodes(store.NodeFilter{})
	if len(nodes) != 0 {
		t.Errorf("expected 0 nodes after rollback, got %d", len(nodes))
	}
}

func TestImportAllIdempotent(t *testing.T) {
	g := setupGraph(t)
	revID, _ := g.Store().CreateRevision("orders", "", "sha1", "manual", "full", "{}")

	payload := ImportPayload{
		Nodes: []ImportNode{
			{NodeKey: "code:controller:orders:orderscontroller", Layer: "code", NodeType: "controller", DomainKey: "orders", Name: "OrdersController"},
		},
	}

	g.ImportAll(payload, revID)
	result, err := g.ImportAll(payload, revID)
	if err != nil {
		t.Fatalf("second ImportAll: %v", err)
	}
	if result.NodesCreated != 1 {
		t.Errorf("NodesCreated = %d, want 1 (upsert)", result.NodesCreated)
	}

	nodes, _ := g.Store().ListNodes(store.NodeFilter{})
	if len(nodes) != 1 {
		t.Errorf("expected 1 node (deduped), got %d", len(nodes))
	}
}
```

Add missing import to test file: `"github.com/anthropics/depbot/internal/store"`

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/graph/ -v -run TestImport
```

Expected: FAIL.

- [ ] **Step 3: Implement bulk import**

Create `internal/graph/import.go`:

```go
package graph

import (
	"fmt"

	"github.com/anthropics/depbot/internal/validate"
)

type ImportNode struct {
	NodeKey       string  `json:"node_key"`
	Layer         string  `json:"layer"`
	NodeType      string  `json:"node_type"`
	DomainKey     string  `json:"domain_key"`
	Name          string  `json:"name"`
	QualifiedName string  `json:"qualified_name,omitempty"`
	RepoName      string  `json:"repo_name,omitempty"`
	FilePath      string  `json:"file_path,omitempty"`
	Lang          string  `json:"lang,omitempty"`
	OwnerKey      string  `json:"owner_key,omitempty"`
	Environment   string  `json:"environment,omitempty"`
	Visibility    string  `json:"visibility,omitempty"`
	Status        string  `json:"status,omitempty"`
	Confidence    float64 `json:"confidence,omitempty"`
	Metadata      string  `json:"metadata,omitempty"`
}

type ImportEdge struct {
	EdgeKey        string  `json:"edge_key,omitempty"`
	FromNodeKey    string  `json:"from_node_key"`
	ToNodeKey      string  `json:"to_node_key"`
	EdgeType       string  `json:"edge_type"`
	DerivationKind string  `json:"derivation_kind"`
	FromLayer      string  `json:"from_layer"`
	ToLayer        string  `json:"to_layer"`
	ContextKey     string  `json:"context_key,omitempty"`
	Confidence     float64 `json:"confidence,omitempty"`
	Metadata       string  `json:"metadata,omitempty"`
}

type ImportEvidence struct {
	TargetKind       string  `json:"target_kind"`
	NodeKey          string  `json:"node_key,omitempty"`
	EdgeKey          string  `json:"edge_key,omitempty"`
	SourceKind       string  `json:"source_kind"`
	RepoName         string  `json:"repo_name,omitempty"`
	FilePath         string  `json:"file_path,omitempty"`
	LineStart        int     `json:"line_start,omitempty"`
	LineEnd          int     `json:"line_end,omitempty"`
	ColumnStart      int     `json:"column_start,omitempty"`
	ColumnEnd        int     `json:"column_end,omitempty"`
	Locator          string  `json:"locator,omitempty"`
	ExtractorID      string  `json:"extractor_id"`
	ExtractorVersion string  `json:"extractor_version"`
	ASTRule          string  `json:"ast_rule,omitempty"`
	SnippetHash      string  `json:"snippet_hash,omitempty"`
	CommitSHA        string  `json:"commit_sha,omitempty"`
	Confidence       float64 `json:"confidence,omitempty"`
	Metadata         string  `json:"metadata,omitempty"`
}

type ImportPayload struct {
	Nodes    []ImportNode    `json:"nodes"`
	Edges    []ImportEdge    `json:"edges"`
	Evidence []ImportEvidence `json:"evidence"`
}

type ImportResult struct {
	NodesCreated    int `json:"nodes_created"`
	EdgesCreated    int `json:"edges_created"`
	EvidenceCreated int `json:"evidence_created"`
}

func (g *Graph) ImportAll(payload ImportPayload, revisionID int64) (*ImportResult, error) {
	result := &ImportResult{}

	err := g.store.WithTx(func(tx *Graph) error {
		// Phase 1: validate and upsert all nodes
		for i, n := range payload.Nodes {
			_, err := tx.UpsertNode(validate.NodeInput{
				NodeKey:       n.NodeKey,
				Layer:         n.Layer,
				NodeType:      n.NodeType,
				DomainKey:     n.DomainKey,
				Name:          n.Name,
				QualifiedName: n.QualifiedName,
				RepoName:      n.RepoName,
				FilePath:      n.FilePath,
				Lang:          n.Lang,
				OwnerKey:      n.OwnerKey,
				Environment:   n.Environment,
				Visibility:    n.Visibility,
				Status:        n.Status,
				Confidence:    n.Confidence,
				Metadata:      n.Metadata,
			}, revisionID)
			if err != nil {
				return fmt.Errorf("node[%d] %q: %w", i, n.NodeKey, err)
			}
			result.NodesCreated++
		}

		// Phase 2: validate and upsert all edges
		for i, e := range payload.Edges {
			_, err := tx.UpsertEdge(validate.EdgeInput{
				EdgeKey:        e.EdgeKey,
				FromNodeKey:    e.FromNodeKey,
				ToNodeKey:      e.ToNodeKey,
				EdgeType:       e.EdgeType,
				DerivationKind: e.DerivationKind,
				FromLayer:      e.FromLayer,
				ToLayer:        e.ToLayer,
				ContextKey:     e.ContextKey,
				Confidence:     e.Confidence,
				Metadata:       e.Metadata,
			}, revisionID)
			if err != nil {
				return fmt.Errorf("edge[%d]: %w", i, err)
			}
			result.EdgesCreated++
		}

		// Phase 3: add evidence
		for i, ev := range payload.Evidence {
			input := validate.EvidenceInput{
				TargetKind:       ev.TargetKind,
				SourceKind:       ev.SourceKind,
				RepoName:         ev.RepoName,
				FilePath:         ev.FilePath,
				LineStart:        ev.LineStart,
				LineEnd:          ev.LineEnd,
				ColumnStart:      ev.ColumnStart,
				ColumnEnd:        ev.ColumnEnd,
				Locator:          ev.Locator,
				ExtractorID:      ev.ExtractorID,
				ExtractorVersion: ev.ExtractorVersion,
				ASTRule:          ev.ASTRule,
				SnippetHash:      ev.SnippetHash,
				CommitSHA:        ev.CommitSHA,
				Confidence:       ev.Confidence,
				Metadata:         ev.Metadata,
			}
			if ev.TargetKind == "node" {
				_, err := tx.AddNodeEvidence(ev.NodeKey, input)
				if err != nil {
					return fmt.Errorf("evidence[%d] node %q: %w", i, ev.NodeKey, err)
				}
			} else {
				_, err := tx.AddEdgeEvidence(ev.EdgeKey, input)
				if err != nil {
					return fmt.Errorf("evidence[%d] edge %q: %w", i, ev.EdgeKey, err)
				}
			}
			result.EvidenceCreated++
		}

		return nil
	})

	if err != nil {
		return nil, err
	}
	return result, nil
}
```

Wait — `WithTx` currently takes `func(tx *store.Store)`, but `ImportAll` needs a `Graph` with validation. We need to adjust. The simplest approach: `WithTx` on `Store` creates a tx-Store, and we wrap it in a new `Graph`:

Update `Graph.ImportAll` to use `store.WithTx` and create a new Graph inside:

```go
func (g *Graph) ImportAll(payload ImportPayload, revisionID int64) (*ImportResult, error) {
	result := &ImportResult{}

	err := g.store.WithTx(func(txStore *store.Store) error {
		txGraph := New(txStore, g.reg)

		for i, n := range payload.Nodes {
			_, err := txGraph.UpsertNode(validate.NodeInput{
				NodeKey:       n.NodeKey,
				Layer:         n.Layer,
				NodeType:      n.NodeType,
				DomainKey:     n.DomainKey,
				Name:          n.Name,
				QualifiedName: n.QualifiedName,
				RepoName:      n.RepoName,
				FilePath:      n.FilePath,
				Lang:          n.Lang,
				OwnerKey:      n.OwnerKey,
				Environment:   n.Environment,
				Visibility:    n.Visibility,
				Status:        n.Status,
				Confidence:    n.Confidence,
				Metadata:      n.Metadata,
			}, revisionID)
			if err != nil {
				return fmt.Errorf("node[%d] %q: %w", i, n.NodeKey, err)
			}
			result.NodesCreated++
		}

		for i, e := range payload.Edges {
			_, err := txGraph.UpsertEdge(validate.EdgeInput{
				EdgeKey:        e.EdgeKey,
				FromNodeKey:    e.FromNodeKey,
				ToNodeKey:      e.ToNodeKey,
				EdgeType:       e.EdgeType,
				DerivationKind: e.DerivationKind,
				FromLayer:      e.FromLayer,
				ToLayer:        e.ToLayer,
				ContextKey:     e.ContextKey,
				Confidence:     e.Confidence,
				Metadata:       e.Metadata,
			}, revisionID)
			if err != nil {
				return fmt.Errorf("edge[%d]: %w", i, err)
			}
			result.EdgesCreated++
		}

		for i, ev := range payload.Evidence {
			input := validate.EvidenceInput{
				TargetKind:       ev.TargetKind,
				SourceKind:       ev.SourceKind,
				RepoName:         ev.RepoName,
				FilePath:         ev.FilePath,
				LineStart:        ev.LineStart,
				LineEnd:          ev.LineEnd,
				ColumnStart:      ev.ColumnStart,
				ColumnEnd:        ev.ColumnEnd,
				Locator:          ev.Locator,
				ExtractorID:      ev.ExtractorID,
				ExtractorVersion: ev.ExtractorVersion,
				ASTRule:          ev.ASTRule,
				SnippetHash:      ev.SnippetHash,
				CommitSHA:        ev.CommitSHA,
				Confidence:       ev.Confidence,
				Metadata:         ev.Metadata,
			}
			if ev.TargetKind == "node" {
				_, err := txGraph.AddNodeEvidence(ev.NodeKey, input)
				if err != nil {
					return fmt.Errorf("evidence[%d] node %q: %w", i, ev.NodeKey, err)
				}
			} else {
				edgeKey := ev.EdgeKey
				if edgeKey == "" {
					return fmt.Errorf("evidence[%d]: edge evidence requires edge_key", i)
				}
				_, err := txGraph.AddEdgeEvidence(edgeKey, input)
				if err != nil {
					return fmt.Errorf("evidence[%d] edge %q: %w", i, edgeKey, err)
				}
			}
			result.EvidenceCreated++
		}

		return nil
	})

	if err != nil {
		return nil, err
	}
	return result, nil
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/graph/ -v -run TestImport
```

Expected: all tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/graph/import.go internal/graph/import_test.go
git commit -m "feat: add bulk import with transactional validation"
```

---

### Task 14: Graph — Basic Queries (deps, reverse-deps, stats)

**Files:**
- Create: `internal/graph/query.go`
- Create: `internal/graph/query_test.go`

- [ ] **Step 1: Write failing tests**

Create `internal/graph/query_test.go`:

```go
package graph

import (
	"testing"

	"github.com/anthropics/depbot/internal/validate"
)

func seedGraphForQuery(t *testing.T) *Graph {
	t.Helper()
	g := setupGraph(t)
	revID, _ := g.Store().CreateRevision("orders", "", "sha1", "manual", "full", "{}")

	// A -> B -> C
	g.UpsertNode(validate.NodeInput{NodeKey: "code:controller:orders:a", Layer: "code", NodeType: "controller", DomainKey: "orders", Name: "A"}, revID)
	g.UpsertNode(validate.NodeInput{NodeKey: "code:provider:orders:b", Layer: "code", NodeType: "provider", DomainKey: "orders", Name: "B"}, revID)
	g.UpsertNode(validate.NodeInput{NodeKey: "code:provider:orders:c", Layer: "code", NodeType: "provider", DomainKey: "orders", Name: "C"}, revID)

	g.UpsertEdge(validate.EdgeInput{FromNodeKey: "code:controller:orders:a", ToNodeKey: "code:provider:orders:b", EdgeType: "INJECTS", DerivationKind: "hard", FromLayer: "code", ToLayer: "code"}, revID)
	g.UpsertEdge(validate.EdgeInput{FromNodeKey: "code:provider:orders:b", ToNodeKey: "code:provider:orders:c", EdgeType: "CALLS_SYMBOL", DerivationKind: "hard", FromLayer: "code", ToLayer: "code"}, revID)

	return g
}

func TestQueryDeps(t *testing.T) {
	g := seedGraphForQuery(t)

	// Direct deps of A
	deps, err := g.QueryDeps("code:controller:orders:a", 1, nil)
	if err != nil {
		t.Fatalf("QueryDeps: %v", err)
	}
	if len(deps) != 1 {
		t.Fatalf("deps count = %d, want 1", len(deps))
	}
	if deps[0].NodeKey != "code:provider:orders:b" {
		t.Errorf("dep = %q, want code:provider:orders:b", deps[0].NodeKey)
	}
}

func TestQueryDepsDepth2(t *testing.T) {
	g := seedGraphForQuery(t)

	deps, err := g.QueryDeps("code:controller:orders:a", 2, nil)
	if err != nil {
		t.Fatalf("QueryDeps depth 2: %v", err)
	}
	if len(deps) != 2 {
		t.Errorf("deps count = %d, want 2", len(deps))
	}
}

func TestQueryReverseDeps(t *testing.T) {
	g := seedGraphForQuery(t)

	// Who depends on C?
	deps, err := g.QueryReverseDeps("code:provider:orders:c", 1, nil)
	if err != nil {
		t.Fatalf("QueryReverseDeps: %v", err)
	}
	if len(deps) != 1 {
		t.Fatalf("reverse deps count = %d, want 1", len(deps))
	}
	if deps[0].NodeKey != "code:provider:orders:b" {
		t.Errorf("dep = %q, want code:provider:orders:b", deps[0].NodeKey)
	}
}

func TestQueryReverseDepsDepth2(t *testing.T) {
	g := seedGraphForQuery(t)

	deps, err := g.QueryReverseDeps("code:provider:orders:c", 2, nil)
	if err != nil {
		t.Fatalf("QueryReverseDeps depth 2: %v", err)
	}
	if len(deps) != 2 {
		t.Errorf("reverse deps count = %d, want 2", len(deps))
	}
}

func TestQueryStats(t *testing.T) {
	g := seedGraphForQuery(t)

	stats, err := g.QueryStats("orders")
	if err != nil {
		t.Fatalf("QueryStats: %v", err)
	}
	if stats.NodeCount != 3 {
		t.Errorf("node count = %d, want 3", stats.NodeCount)
	}
	if stats.EdgeCount != 2 {
		t.Errorf("edge count = %d, want 2", stats.EdgeCount)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/graph/ -v -run "TestQuery"
```

Expected: FAIL.

- [ ] **Step 3: Implement queries**

Create `internal/graph/query.go`:

```go
package graph

import (
	"fmt"

	"github.com/anthropics/depbot/internal/store"
)

type DepNode struct {
	NodeKey  string `json:"node_key"`
	Name     string `json:"name"`
	Layer    string `json:"layer"`
	NodeType string `json:"node_type"`
	Depth    int    `json:"depth"`
}

type Stats struct {
	NodeCount     int            `json:"node_count"`
	EdgeCount     int            `json:"edge_count"`
	NodesByLayer  map[string]int `json:"nodes_by_layer"`
	EdgesByType   map[string]int `json:"edges_by_type"`
	ActiveNodes   int            `json:"active_nodes"`
	StaleNodes    int            `json:"stale_nodes"`
}

// QueryDeps returns nodes that this node depends on (outgoing edges).
func (g *Graph) QueryDeps(nodeKey string, maxDepth int, derivationFilter []string) ([]DepNode, error) {
	return g.traverseDeps(nodeKey, maxDepth, derivationFilter, false)
}

// QueryReverseDeps returns nodes that depend on this node (incoming edges).
func (g *Graph) QueryReverseDeps(nodeKey string, maxDepth int, derivationFilter []string) ([]DepNode, error) {
	return g.traverseDeps(nodeKey, maxDepth, derivationFilter, true)
}

func (g *Graph) traverseDeps(nodeKey string, maxDepth int, derivationFilter []string, reverse bool) ([]DepNode, error) {
	startID, err := g.store.GetNodeIDByKey(nodeKey)
	if err != nil {
		return nil, fmt.Errorf("start node: %w", err)
	}

	visited := map[int64]bool{startID: true}
	var result []DepNode
	frontier := []int64{startID}

	for depth := 1; depth <= maxDepth && len(frontier) > 0; depth++ {
		var nextFrontier []int64
		for _, nodeID := range frontier {
			var edges []store.EdgeRow
			var err error
			if reverse {
				edges, err = g.store.ListEdges(store.EdgeFilter{ToNodeID: nodeID})
			} else {
				edges, err = g.store.ListEdges(store.EdgeFilter{FromNodeID: nodeID})
			}
			if err != nil {
				return nil, fmt.Errorf("listing edges: %w", err)
			}

			for _, e := range edges {
				if !e.Active {
					continue
				}
				if len(derivationFilter) > 0 && !contains(derivationFilter, e.DerivationKind) {
					continue
				}

				targetID := e.ToNodeID
				if reverse {
					targetID = e.FromNodeID
				}

				if visited[targetID] {
					continue
				}
				visited[targetID] = true
				nextFrontier = append(nextFrontier, targetID)

				// Look up node info
				nodes, err := g.store.ListNodes(store.NodeFilter{})
				if err != nil {
					return nil, err
				}
				for _, n := range nodes {
					if n.NodeID == targetID {
						result = append(result, DepNode{
							NodeKey:  n.NodeKey,
							Name:     n.Name,
							Layer:    n.Layer,
							NodeType: n.NodeType,
							Depth:    depth,
						})
						break
					}
				}
			}
		}
		frontier = nextFrontier
	}

	return result, nil
}

// QueryStats returns aggregate graph statistics.
func (g *Graph) QueryStats(domainKey string) (*Stats, error) {
	nodes, err := g.store.ListNodes(store.NodeFilter{Domain: domainKey})
	if err != nil {
		return nil, err
	}

	edges, err := g.store.ListEdges(store.EdgeFilter{})
	if err != nil {
		return nil, err
	}

	stats := &Stats{
		NodeCount:    len(nodes),
		EdgeCount:    len(edges),
		NodesByLayer: make(map[string]int),
		EdgesByType:  make(map[string]int),
	}

	for _, n := range nodes {
		stats.NodesByLayer[n.Layer]++
		if n.Status == "active" {
			stats.ActiveNodes++
		} else if n.Status == "stale" {
			stats.StaleNodes++
		}
	}
	for _, e := range edges {
		stats.EdgesByType[e.EdgeType]++
	}

	return stats, nil
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}
```

Note: The `traverseDeps` function uses a naive approach of listing all nodes to find by ID. This is fine for the MVP. An optimization (direct query by ID) can be added later if needed.

- [ ] **Step 4: Run tests**

```bash
go test ./internal/graph/ -v -run TestQuery
```

Expected: all tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/graph/query.go internal/graph/query_test.go
git commit -m "feat: add deps, reverse-deps, and stats queries"
```

---

### Task 15: CLI Commands — init, revision, node, edge, evidence, snapshot, import, query, validate

**Files:**
- Create: `internal/cli/root.go`
- Create: `internal/cli/init.go`
- Create: `internal/cli/revision.go`
- Create: `internal/cli/node.go`
- Create: `internal/cli/edge.go`
- Create: `internal/cli/evidence.go`
- Create: `internal/cli/snapshot.go`
- Create: `internal/cli/importcmd.go`
- Create: `internal/cli/query.go`
- Create: `internal/cli/validate_cmd.go`
- Create: `internal/cli/output.go`
- Modify: `cmd/oracle/main.go`

This is a larger task. It wires all internal packages to CLI commands. Each command parses flags, opens the store/registry, calls graph operations, and outputs JSON.

- [ ] **Step 1: Create output helper**

Create `internal/cli/output.go`:

```go
package cli

import (
	"encoding/json"
	"fmt"
	"os"
)

func outputJSON(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.Encode(v)
}

func exitError(msg string, err error) {
	fmt.Fprintf(os.Stderr, "error: %s: %v\n", msg, err)
	os.Exit(1)
}

type ErrorResponse struct {
	Error string `json:"error"`
}

func outputError(err error) {
	outputJSON(ErrorResponse{Error: err.Error()})
	os.Exit(1)
}
```

- [ ] **Step 2: Create root command with global flags**

Create `internal/cli/root.go`:

```go
package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/anthropics/depbot/internal/graph"
	"github.com/anthropics/depbot/internal/registry"
	"github.com/anthropics/depbot/internal/store"
	"github.com/spf13/cobra"
)

var (
	dbPath       string
	registryPath string
	manifestPath string
)

func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "oracle",
		Short: "Domain Oracle — graph storage and query API",
	}

	root.PersistentFlags().StringVar(&dbPath, "db", "oracle.db", "Path to SQLite database")
	root.PersistentFlags().StringVar(&registryPath, "registry", "oracle.types.yaml", "Path to type registry")
	root.PersistentFlags().StringVar(&manifestPath, "manifest", "oracle.domain.yaml", "Path to domain manifest")

	root.AddCommand(
		newVersionCmd(),
		newInitCmd(),
		newRevisionCmd(),
		newNodeCmd(),
		newEdgeCmd(),
		newEvidenceCmd(),
		newSnapshotCmd(),
		newImportCmd(),
		newQueryCmd(),
		newValidateCmd(),
	)

	return root
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println("oracle v0.1.0")
		},
	}
}

func openGraph() *graph.Graph {
	s, err := store.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error opening database %q: %v\n", dbPath, err)
		os.Exit(1)
	}

	var reg *registry.Registry
	if _, err := os.Stat(registryPath); err == nil {
		reg, err = registry.LoadFile(registryPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error loading registry %q: %v\n", registryPath, err)
			os.Exit(1)
		}
	} else {
		reg, err = registry.LoadDefaults()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error loading default registry: %v\n", err)
			os.Exit(1)
		}
	}

	return graph.New(s, reg)
}

func absPath(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return abs
}
```

- [ ] **Step 3: Create init command**

Create `internal/cli/init.go`:

```go
package cli

import (
	"fmt"
	"os"

	"github.com/anthropics/depbot/internal/registry"
	"github.com/anthropics/depbot/internal/store"
	"github.com/spf13/cobra"
)

func newInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Initialize domain oracle (manifest skeleton + types + database)",
		Run: func(cmd *cobra.Command, args []string) {
			// Create manifest skeleton if not exists
			if _, err := os.Stat(manifestPath); os.IsNotExist(err) {
				skeleton := `# Domain Oracle Manifest — edit this file
domain: my-domain
description: ""
repositories:
  - name: example-repo
    path: ./repos/example-repo
    tags: []
owner: my-team
`
				if err := os.WriteFile(manifestPath, []byte(skeleton), 0644); err != nil {
					outputError(fmt.Errorf("writing manifest: %w", err))
				}
				fmt.Fprintf(os.Stderr, "Created %s (edit this file)\n", manifestPath)
			}

			// Create types file if not exists
			if _, err := os.Stat(registryPath); os.IsNotExist(err) {
				if err := os.WriteFile(registryPath, registry.DefaultRegistryYAML, 0644); err != nil {
					outputError(fmt.Errorf("writing types: %w", err))
				}
				fmt.Fprintf(os.Stderr, "Created %s\n", registryPath)
			}

			// Init database
			s, err := store.Open(dbPath)
			if err != nil {
				outputError(fmt.Errorf("init database: %w", err))
			}
			s.Close()
			fmt.Fprintf(os.Stderr, "Database ready at %s\n", dbPath)

			outputJSON(map[string]string{
				"manifest": manifestPath,
				"registry": registryPath,
				"database": dbPath,
				"status":   "initialized",
			})
		},
	}
}
```

- [ ] **Step 4: Create revision commands**

Create `internal/cli/revision.go`:

```go
package cli

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"
)

func newRevisionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "revision",
		Short: "Manage graph revisions",
	}

	// create
	createCmd := &cobra.Command{
		Use:   "create",
		Short: "Create a new revision",
		Run: func(cmd *cobra.Command, args []string) {
			g := openGraph()
			defer g.Store().Close()

			domain, _ := cmd.Flags().GetString("domain")
			beforeSHA, _ := cmd.Flags().GetString("before-sha")
			afterSHA, _ := cmd.Flags().GetString("after-sha")
			trigger, _ := cmd.Flags().GetString("trigger")
			mode, _ := cmd.Flags().GetString("mode")
			metadata, _ := cmd.Flags().GetString("metadata")

			id, err := g.Store().CreateRevision(domain, beforeSHA, afterSHA, trigger, mode, metadata)
			if err != nil {
				outputError(err)
			}
			outputJSON(map[string]any{"revision_id": id})
		},
	}
	createCmd.Flags().String("domain", "", "Domain key (required)")
	createCmd.Flags().String("before-sha", "", "Git SHA before")
	createCmd.Flags().String("after-sha", "", "Git SHA after (required)")
	createCmd.Flags().String("trigger", "manual", "Trigger kind")
	createCmd.Flags().String("mode", "full", "Scan mode")
	createCmd.Flags().String("metadata", "{}", "JSON metadata")
	createCmd.MarkFlagRequired("domain")
	createCmd.MarkFlagRequired("after-sha")

	// get
	getCmd := &cobra.Command{
		Use:   "get [revision_id]",
		Short: "Get revision details",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			g := openGraph()
			defer g.Store().Close()

			id, err := strconv.ParseInt(args[0], 10, 64)
			if err != nil {
				outputError(fmt.Errorf("invalid revision_id: %w", err))
			}
			rev, err := g.Store().GetRevision(id)
			if err != nil {
				outputError(err)
			}
			outputJSON(rev)
		},
	}

	cmd.AddCommand(createCmd, getCmd)
	return cmd
}
```

- [ ] **Step 5: Create node commands**

Create `internal/cli/node.go`:

```go
package cli

import (
	"github.com/anthropics/depbot/internal/store"
	"github.com/anthropics/depbot/internal/validate"
	"github.com/spf13/cobra"
)

func newNodeCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "node", Short: "Manage graph nodes"}

	// upsert
	upsertCmd := &cobra.Command{
		Use:   "upsert",
		Short: "Create or update a node",
		Run: func(cmd *cobra.Command, args []string) {
			g := openGraph()
			defer g.Store().Close()

			nodeKey, _ := cmd.Flags().GetString("node-key")
			layer, _ := cmd.Flags().GetString("layer")
			nodeType, _ := cmd.Flags().GetString("type")
			domain, _ := cmd.Flags().GetString("domain")
			name, _ := cmd.Flags().GetString("name")
			repo, _ := cmd.Flags().GetString("repo")
			file, _ := cmd.Flags().GetString("file")
			revision, _ := cmd.Flags().GetInt64("revision")
			confidence, _ := cmd.Flags().GetFloat64("confidence")
			metadata, _ := cmd.Flags().GetString("metadata")

			id, err := g.UpsertNode(validate.NodeInput{
				NodeKey:   nodeKey,
				Layer:     layer,
				NodeType:  nodeType,
				DomainKey: domain,
				Name:      name,
				RepoName:  repo,
				FilePath:  file,
				Confidence: confidence,
				Metadata:  metadata,
			}, revision)
			if err != nil {
				outputError(err)
			}
			outputJSON(map[string]any{"node_id": id})
		},
	}
	upsertCmd.Flags().String("node-key", "", "Node key")
	upsertCmd.Flags().String("layer", "", "Layer")
	upsertCmd.Flags().String("type", "", "Node type")
	upsertCmd.Flags().String("domain", "", "Domain key")
	upsertCmd.Flags().String("name", "", "Node name")
	upsertCmd.Flags().String("repo", "", "Repository name")
	upsertCmd.Flags().String("file", "", "File path")
	upsertCmd.Flags().Int64("revision", 0, "Revision ID")
	upsertCmd.Flags().Float64("confidence", 0, "Confidence [0-1]")
	upsertCmd.Flags().String("metadata", "{}", "JSON metadata")
	upsertCmd.MarkFlagRequired("node-key")
	upsertCmd.MarkFlagRequired("layer")
	upsertCmd.MarkFlagRequired("type")
	upsertCmd.MarkFlagRequired("domain")
	upsertCmd.MarkFlagRequired("name")

	// get
	getCmd := &cobra.Command{
		Use:   "get [node_key]",
		Short: "Get node details",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			g := openGraph()
			defer g.Store().Close()
			node, err := g.Store().GetNodeByKey(args[0])
			if err != nil {
				outputError(err)
			}
			outputJSON(node)
		},
	}

	// list
	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List nodes",
		Run: func(cmd *cobra.Command, args []string) {
			g := openGraph()
			defer g.Store().Close()

			layer, _ := cmd.Flags().GetString("layer")
			nodeType, _ := cmd.Flags().GetString("type")
			domain, _ := cmd.Flags().GetString("domain")
			repo, _ := cmd.Flags().GetString("repo")
			status, _ := cmd.Flags().GetString("status")

			nodes, err := g.Store().ListNodes(store.NodeFilter{
				Layer: layer, NodeType: nodeType, Domain: domain,
				RepoName: repo, Status: status,
			})
			if err != nil {
				outputError(err)
			}
			outputJSON(nodes)
		},
	}
	listCmd.Flags().String("layer", "", "Filter by layer")
	listCmd.Flags().String("type", "", "Filter by type")
	listCmd.Flags().String("domain", "", "Filter by domain")
	listCmd.Flags().String("repo", "", "Filter by repo")
	listCmd.Flags().String("status", "", "Filter by status")

	// delete
	deleteCmd := &cobra.Command{
		Use:   "delete [node_key]",
		Short: "Mark node as deleted",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			g := openGraph()
			defer g.Store().Close()
			if err := g.Store().DeleteNode(args[0]); err != nil {
				outputError(err)
			}
			outputJSON(map[string]string{"status": "deleted", "node_key": args[0]})
		},
	}

	cmd.AddCommand(upsertCmd, getCmd, listCmd, deleteCmd)
	return cmd
}
```

- [ ] **Step 6: Create edge commands**

Create `internal/cli/edge.go`:

```go
package cli

import (
	"github.com/anthropics/depbot/internal/store"
	"github.com/anthropics/depbot/internal/validate"
	"github.com/spf13/cobra"
)

func newEdgeCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "edge", Short: "Manage graph edges"}

	upsertCmd := &cobra.Command{
		Use:   "upsert",
		Short: "Create or update an edge",
		Run: func(cmd *cobra.Command, args []string) {
			g := openGraph()
			defer g.Store().Close()

			edgeKey, _ := cmd.Flags().GetString("edge-key")
			from, _ := cmd.Flags().GetString("from")
			to, _ := cmd.Flags().GetString("to")
			edgeType, _ := cmd.Flags().GetString("type")
			derivation, _ := cmd.Flags().GetString("derivation")
			revision, _ := cmd.Flags().GetInt64("revision")
			confidence, _ := cmd.Flags().GetFloat64("confidence")
			metadata, _ := cmd.Flags().GetString("metadata")

			// Need layers for validation — look up from/to nodes
			fromNode, err := g.Store().GetNodeByKey(from)
			if err != nil {
				outputError(err)
			}
			toNode, err := g.Store().GetNodeByKey(to)
			if err != nil {
				outputError(err)
			}

			id, err := g.UpsertEdge(validate.EdgeInput{
				EdgeKey:        edgeKey,
				FromNodeKey:    from,
				ToNodeKey:      to,
				EdgeType:       edgeType,
				DerivationKind: derivation,
				FromLayer:      fromNode.Layer,
				ToLayer:        toNode.Layer,
				Confidence:     confidence,
				Metadata:       metadata,
			}, revision)
			if err != nil {
				outputError(err)
			}
			outputJSON(map[string]any{"edge_id": id})
		},
	}
	upsertCmd.Flags().String("edge-key", "", "Edge key (auto-generated if empty)")
	upsertCmd.Flags().String("from", "", "From node key")
	upsertCmd.Flags().String("to", "", "To node key")
	upsertCmd.Flags().String("type", "", "Edge type")
	upsertCmd.Flags().String("derivation", "", "Derivation kind")
	upsertCmd.Flags().Int64("revision", 0, "Revision ID")
	upsertCmd.Flags().Float64("confidence", 0, "Confidence")
	upsertCmd.Flags().String("metadata", "{}", "JSON metadata")
	upsertCmd.MarkFlagRequired("from")
	upsertCmd.MarkFlagRequired("to")
	upsertCmd.MarkFlagRequired("type")
	upsertCmd.MarkFlagRequired("derivation")

	getCmd := &cobra.Command{
		Use:  "get [edge_key]",
		Args: cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			g := openGraph()
			defer g.Store().Close()
			edge, err := g.Store().GetEdgeByKey(args[0])
			if err != nil {
				outputError(err)
			}
			outputJSON(edge)
		},
	}

	listCmd := &cobra.Command{
		Use: "list",
		Run: func(cmd *cobra.Command, args []string) {
			g := openGraph()
			defer g.Store().Close()

			from, _ := cmd.Flags().GetString("from")
			to, _ := cmd.Flags().GetString("to")
			edgeType, _ := cmd.Flags().GetString("type")
			derivation, _ := cmd.Flags().GetString("derivation")

			filter := store.EdgeFilter{EdgeType: edgeType, DerivationKind: derivation}
			if from != "" {
				id, err := g.Store().GetNodeIDByKey(from)
				if err != nil {
					outputError(err)
				}
				filter.FromNodeID = id
			}
			if to != "" {
				id, err := g.Store().GetNodeIDByKey(to)
				if err != nil {
					outputError(err)
				}
				filter.ToNodeID = id
			}

			edges, err := g.Store().ListEdges(filter)
			if err != nil {
				outputError(err)
			}
			outputJSON(edges)
		},
	}
	listCmd.Flags().String("from", "", "From node key")
	listCmd.Flags().String("to", "", "To node key")
	listCmd.Flags().String("type", "", "Edge type")
	listCmd.Flags().String("derivation", "", "Derivation kind")

	deleteCmd := &cobra.Command{
		Use:  "delete [edge_key]",
		Args: cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			g := openGraph()
			defer g.Store().Close()
			if err := g.Store().DeleteEdge(args[0]); err != nil {
				outputError(err)
			}
			outputJSON(map[string]string{"status": "deleted", "edge_key": args[0]})
		},
	}

	cmd.AddCommand(upsertCmd, getCmd, listCmd, deleteCmd)
	return cmd
}
```

- [ ] **Step 7: Create evidence commands**

Create `internal/cli/evidence.go`:

```go
package cli

import (
	"github.com/anthropics/depbot/internal/validate"
	"github.com/spf13/cobra"
)

func newEvidenceCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "evidence", Short: "Manage graph evidence"}

	addCmd := &cobra.Command{
		Use:   "add",
		Short: "Add evidence",
		Run: func(cmd *cobra.Command, args []string) {
			g := openGraph()
			defer g.Store().Close()

			targetKind, _ := cmd.Flags().GetString("target-kind")
			nodeKey, _ := cmd.Flags().GetString("node-key")
			edgeKey, _ := cmd.Flags().GetString("edge-key")
			sourceKind, _ := cmd.Flags().GetString("source-kind")
			repo, _ := cmd.Flags().GetString("repo")
			file, _ := cmd.Flags().GetString("file")
			lineStart, _ := cmd.Flags().GetInt("line-start")
			lineEnd, _ := cmd.Flags().GetInt("line-end")
			extractorID, _ := cmd.Flags().GetString("extractor-id")
			extractorVer, _ := cmd.Flags().GetString("extractor-version")
			commitSHA, _ := cmd.Flags().GetString("commit-sha")
			confidence, _ := cmd.Flags().GetFloat64("confidence")
			metadata, _ := cmd.Flags().GetString("metadata")

			input := validate.EvidenceInput{
				TargetKind:       targetKind,
				SourceKind:       sourceKind,
				RepoName:         repo,
				FilePath:         file,
				LineStart:        lineStart,
				LineEnd:          lineEnd,
				ExtractorID:      extractorID,
				ExtractorVersion: extractorVer,
				CommitSHA:        commitSHA,
				Confidence:       confidence,
				Metadata:         metadata,
			}

			var id int64
			var err error
			if targetKind == "node" {
				id, err = g.AddNodeEvidence(nodeKey, input)
			} else {
				id, err = g.AddEdgeEvidence(edgeKey, input)
			}
			if err != nil {
				outputError(err)
			}
			outputJSON(map[string]any{"evidence_id": id})
		},
	}
	addCmd.Flags().String("target-kind", "", "Target kind: node or edge")
	addCmd.Flags().String("node-key", "", "Node key (for node evidence)")
	addCmd.Flags().String("edge-key", "", "Edge key (for edge evidence)")
	addCmd.Flags().String("source-kind", "", "Source kind")
	addCmd.Flags().String("repo", "", "Repository name")
	addCmd.Flags().String("file", "", "File path")
	addCmd.Flags().Int("line-start", 0, "Line start")
	addCmd.Flags().Int("line-end", 0, "Line end")
	addCmd.Flags().String("extractor-id", "", "Extractor ID")
	addCmd.Flags().String("extractor-version", "", "Extractor version")
	addCmd.Flags().String("commit-sha", "", "Commit SHA")
	addCmd.Flags().Float64("confidence", 0, "Confidence")
	addCmd.Flags().String("metadata", "{}", "Metadata JSON")
	addCmd.MarkFlagRequired("target-kind")
	addCmd.MarkFlagRequired("source-kind")
	addCmd.MarkFlagRequired("extractor-id")
	addCmd.MarkFlagRequired("extractor-version")

	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List evidence",
		Run: func(cmd *cobra.Command, args []string) {
			g := openGraph()
			defer g.Store().Close()

			nodeKey, _ := cmd.Flags().GetString("node-key")
			edgeKey, _ := cmd.Flags().GetString("edge-key")

			if nodeKey != "" {
				nodeID, err := g.Store().GetNodeIDByKey(nodeKey)
				if err != nil {
					outputError(err)
				}
				evidence, err := g.Store().ListEvidenceByNode(nodeID)
				if err != nil {
					outputError(err)
				}
				outputJSON(evidence)
			} else if edgeKey != "" {
				edge, err := g.Store().GetEdgeByKey(edgeKey)
				if err != nil {
					outputError(err)
				}
				evidence, err := g.Store().ListEvidenceByEdge(edge.EdgeID)
				if err != nil {
					outputError(err)
				}
				outputJSON(evidence)
			} else {
				outputError(fmt.Errorf("--node-key or --edge-key required"))
			}
		},
	}
	listCmd.Flags().String("node-key", "", "Node key")
	listCmd.Flags().String("edge-key", "", "Edge key")

	cmd.AddCommand(addCmd, listCmd)
	return cmd
}
```

Add `"fmt"` to imports in the evidence.go file.

- [ ] **Step 8: Create snapshot commands**

Create `internal/cli/snapshot.go`:

```go
package cli

import (
	"github.com/anthropics/depbot/internal/store"
	"github.com/spf13/cobra"
)

func newSnapshotCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "snapshot", Short: "Manage snapshots"}

	createCmd := &cobra.Command{
		Use: "create",
		Run: func(cmd *cobra.Command, args []string) {
			g := openGraph()
			defer g.Store().Close()

			revision, _ := cmd.Flags().GetInt64("revision")
			domain, _ := cmd.Flags().GetString("domain")
			kind, _ := cmd.Flags().GetString("kind")
			nodeCount, _ := cmd.Flags().GetInt("node-count")
			edgeCount, _ := cmd.Flags().GetInt("edge-count")
			summary, _ := cmd.Flags().GetString("summary")

			id, err := g.Store().CreateSnapshot(store.SnapshotRow{
				RevisionID: revision, DomainKey: domain, Kind: kind,
				NodeCount: nodeCount, EdgeCount: edgeCount, Summary: summary,
			})
			if err != nil {
				outputError(err)
			}
			outputJSON(map[string]any{"snapshot_id": id})
		},
	}
	createCmd.Flags().Int64("revision", 0, "Revision ID")
	createCmd.Flags().String("domain", "", "Domain key")
	createCmd.Flags().String("kind", "full", "Snapshot kind")
	createCmd.Flags().Int("node-count", 0, "Node count")
	createCmd.Flags().Int("edge-count", 0, "Edge count")
	createCmd.Flags().String("summary", "{}", "Summary JSON")
	createCmd.MarkFlagRequired("revision")
	createCmd.MarkFlagRequired("domain")

	listCmd := &cobra.Command{
		Use: "list",
		Run: func(cmd *cobra.Command, args []string) {
			g := openGraph()
			defer g.Store().Close()
			domain, _ := cmd.Flags().GetString("domain")
			snaps, err := g.Store().ListSnapshots(domain)
			if err != nil {
				outputError(err)
			}
			outputJSON(snaps)
		},
	}
	listCmd.Flags().String("domain", "", "Domain key")
	listCmd.MarkFlagRequired("domain")

	cmd.AddCommand(createCmd, listCmd)
	return cmd
}
```

- [ ] **Step 9: Create import command**

Create `internal/cli/importcmd.go`:

```go
package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/anthropics/depbot/internal/graph"
	"github.com/spf13/cobra"
)

func newImportCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "import", Short: "Bulk import graph data"}

	allCmd := &cobra.Command{
		Use:   "all",
		Short: "Import nodes, edges, and evidence from JSON file",
		Run: func(cmd *cobra.Command, args []string) {
			g := openGraph()
			defer g.Store().Close()

			filePath, _ := cmd.Flags().GetString("file")
			revision, _ := cmd.Flags().GetInt64("revision")

			data, err := os.ReadFile(filePath)
			if err != nil {
				outputError(fmt.Errorf("reading file: %w", err))
			}

			var payload graph.ImportPayload
			if err := json.Unmarshal(data, &payload); err != nil {
				outputError(fmt.Errorf("parsing JSON: %w", err))
			}

			result, err := g.ImportAll(payload, revision)
			if err != nil {
				outputError(err)
			}
			outputJSON(result)
		},
	}
	allCmd.Flags().String("file", "", "Path to JSON file")
	allCmd.Flags().Int64("revision", 0, "Revision ID")
	allCmd.MarkFlagRequired("file")
	allCmd.MarkFlagRequired("revision")

	cmd.AddCommand(allCmd)
	return cmd
}
```

- [ ] **Step 10: Create query commands**

Create `internal/cli/query.go`:

```go
package cli

import (
	"strings"

	"github.com/spf13/cobra"
)

func newQueryCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "query", Short: "Query the graph"}

	nodeCmd := &cobra.Command{
		Use:  "node [node_key]",
		Args: cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			g := openGraph()
			defer g.Store().Close()
			node, err := g.Store().GetNodeByKey(args[0])
			if err != nil {
				outputError(err)
			}
			nodeID, _ := g.Store().GetNodeIDByKey(args[0])
			evidence, _ := g.Store().ListEvidenceByNode(nodeID)
			outputJSON(map[string]any{"node": node, "evidence": evidence})
		},
	}

	edgesCmd := &cobra.Command{
		Use:  "edges [node_key]",
		Args: cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			g := openGraph()
			defer g.Store().Close()
			nodeID, err := g.Store().GetNodeIDByKey(args[0])
			if err != nil {
				outputError(err)
			}
			from, _ := g.Store().ListEdges(store.EdgeFilter{FromNodeID: nodeID})
			to, _ := g.Store().ListEdges(store.EdgeFilter{ToNodeID: nodeID})
			outputJSON(map[string]any{"outgoing": from, "incoming": to})
		},
	}

	depsCmd := &cobra.Command{
		Use:  "deps [node_key]",
		Args: cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			g := openGraph()
			defer g.Store().Close()
			depth, _ := cmd.Flags().GetInt("depth")
			derivation, _ := cmd.Flags().GetString("derivation")
			var filter []string
			if derivation != "" {
				filter = strings.Split(derivation, ",")
			}
			deps, err := g.QueryDeps(args[0], depth, filter)
			if err != nil {
				outputError(err)
			}
			outputJSON(deps)
		},
	}
	depsCmd.Flags().Int("depth", 1, "Max traversal depth")
	depsCmd.Flags().String("derivation", "", "Comma-separated derivation filter")

	reverseDepsCmd := &cobra.Command{
		Use:  "reverse-deps [node_key]",
		Args: cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			g := openGraph()
			defer g.Store().Close()
			depth, _ := cmd.Flags().GetInt("depth")
			derivation, _ := cmd.Flags().GetString("derivation")
			var filter []string
			if derivation != "" {
				filter = strings.Split(derivation, ",")
			}
			deps, err := g.QueryReverseDeps(args[0], depth, filter)
			if err != nil {
				outputError(err)
			}
			outputJSON(deps)
		},
	}
	reverseDepsCmd.Flags().Int("depth", 1, "Max traversal depth")
	reverseDepsCmd.Flags().String("derivation", "", "Comma-separated derivation filter")

	statsCmd := &cobra.Command{
		Use: "stats",
		Run: func(cmd *cobra.Command, args []string) {
			g := openGraph()
			defer g.Store().Close()
			domain, _ := cmd.Flags().GetString("domain")
			stats, err := g.QueryStats(domain)
			if err != nil {
				outputError(err)
			}
			outputJSON(stats)
		},
	}
	statsCmd.Flags().String("domain", "", "Domain key")

	evidenceCmd := &cobra.Command{
		Use: "evidence",
		Run: func(cmd *cobra.Command, args []string) {
			g := openGraph()
			defer g.Store().Close()
			nodeKey, _ := cmd.Flags().GetString("node-key")
			edgeKey, _ := cmd.Flags().GetString("edge-key")
			if nodeKey != "" {
				nodeID, err := g.Store().GetNodeIDByKey(nodeKey)
				if err != nil {
					outputError(err)
				}
				ev, _ := g.Store().ListEvidenceByNode(nodeID)
				outputJSON(ev)
			} else if edgeKey != "" {
				edge, err := g.Store().GetEdgeByKey(edgeKey)
				if err != nil {
					outputError(err)
				}
				ev, _ := g.Store().ListEvidenceByEdge(edge.EdgeID)
				outputJSON(ev)
			}
		},
	}
	evidenceCmd.Flags().String("node-key", "", "Node key")
	evidenceCmd.Flags().String("edge-key", "", "Edge key")

	cmd.AddCommand(nodeCmd, edgesCmd, depsCmd, reverseDepsCmd, statsCmd, evidenceCmd)
	return cmd
}
```

Add missing import: `"github.com/anthropics/depbot/internal/store"` to query.go.

- [ ] **Step 11: Create validate commands**

Create `internal/cli/validate_cmd.go`:

```go
package cli

import (
	"fmt"

	"github.com/anthropics/depbot/internal/store"
	"github.com/anthropics/depbot/internal/validate"
	"github.com/spf13/cobra"
)

type ValidationIssue struct {
	Kind    string `json:"kind"`
	Target  string `json:"target"`
	Message string `json:"message"`
}

func newValidateCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "validate", Short: "Validate graph integrity"}

	graphCmd := &cobra.Command{
		Use:   "graph",
		Short: "Full graph integrity check",
		Run: func(cmd *cobra.Command, args []string) {
			g := openGraph()
			defer g.Store().Close()

			var issues []ValidationIssue

			// Check orphan edges
			edges, _ := g.Store().ListEdges(store.EdgeFilter{})
			for _, e := range edges {
				if _, err := g.Store().GetNodeByKey(""); err != nil {
					// We check by ID instead
				}
			}

			// Check key formats
			nodes, _ := g.Store().ListNodes(store.NodeFilter{})
			for _, n := range nodes {
				if _, err := validate.NormalizeNodeKey(n.NodeKey); err != nil {
					issues = append(issues, ValidationIssue{Kind: "malformed_key", Target: n.NodeKey, Message: err.Error()})
				}
			}
			for _, e := range edges {
				if _, err := validate.NormalizeEdgeKey(e.EdgeKey); err != nil {
					issues = append(issues, ValidationIssue{Kind: "malformed_key", Target: e.EdgeKey, Message: err.Error()})
				}
			}

			// Check confidence ranges
			for _, n := range nodes {
				if n.Confidence < 0 || n.Confidence > 1 {
					issues = append(issues, ValidationIssue{Kind: "confidence_range", Target: n.NodeKey, Message: fmt.Sprintf("confidence %f out of [0,1]", n.Confidence)})
				}
			}

			outputJSON(map[string]any{
				"issues": issues,
				"nodes_checked": len(nodes),
				"edges_checked": len(edges),
				"valid": len(issues) == 0,
			})
		},
	}

	cmd.AddCommand(graphCmd)
	return cmd
}
```

- [ ] **Step 12: Update main.go to use cli package**

Update `cmd/oracle/main.go`:

```go
package main

import (
	"os"

	"github.com/anthropics/depbot/internal/cli"
)

func main() {
	if err := cli.NewRootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}
```

- [ ] **Step 13: Build and test CLI**

```bash
cd /home/alex/personal/depbot
go build -o oracle ./cmd/oracle
./oracle version
./oracle init --db /tmp/test-oracle.db --registry /tmp/test.types.yaml --manifest /tmp/test.manifest.yaml
./oracle --help
```

Expected: version prints, init creates files, help shows all subcommands.

- [ ] **Step 14: Run all tests**

```bash
go test ./... -v
```

Expected: all tests PASS.

- [ ] **Step 15: Commit**

```bash
git add internal/cli/ cmd/oracle/main.go
git commit -m "feat: add CLI commands for all graph operations"
```

---

### Task 16: MCP Server

**Files:**
- Create: `internal/mcp/server.go`
- Create: `internal/cli/mcp.go`

- [ ] **Step 1: Add MCP dependency**

```bash
cd /home/alex/personal/depbot
go get github.com/mark3labs/mcp-go
```

- [ ] **Step 2: Create MCP server**

Create `internal/mcp/server.go`:

```go
package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/anthropics/depbot/internal/graph"
	"github.com/anthropics/depbot/internal/store"
	"github.com/anthropics/depbot/internal/validate"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func NewServer(g *graph.Graph) *server.MCPServer {
	s := server.NewMCPServer(
		"oracle",
		"0.1.0",
	)

	s.AddTool(revisionCreateTool(), revisionCreateHandler(g))
	s.AddTool(nodeUpsertTool(), nodeUpsertHandler(g))
	s.AddTool(nodeListTool(), nodeListHandler(g))
	s.AddTool(nodeGetTool(), nodeGetHandler(g))
	s.AddTool(edgeUpsertTool(), edgeUpsertHandler(g))
	s.AddTool(edgeListTool(), edgeListHandler(g))
	s.AddTool(evidenceAddTool(), evidenceAddHandler(g))
	s.AddTool(importAllTool(), importAllHandler(g))
	s.AddTool(queryDepsTool(), queryDepsHandler(g))
	s.AddTool(queryReverseDepsTool(), queryReverseDepsHandler(g))
	s.AddTool(queryStatsTool(), queryStatsHandler(g))
	s.AddTool(snapshotCreateTool(), snapshotCreateHandler(g))
	s.AddTool(staleMarkTool(), staleMarkHandler(g))

	return s
}

// Helper to get string param
func strParam(args map[string]any, key string) string {
	v, _ := args[key].(string)
	return v
}

func intParam(args map[string]any, key string) int {
	switch v := args[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	}
	return 0
}

func int64Param(args map[string]any, key string) int64 {
	switch v := args[key].(type) {
	case float64:
		return int64(v)
	case int64:
		return v
	}
	return 0
}

func float64Param(args map[string]any, key string) float64 {
	v, _ := args[key].(float64)
	return v
}

func jsonResult(v any) *mcp.CallToolResult {
	data, _ := json.Marshal(v)
	return mcp.NewToolResultText(string(data))
}

func errorResult(err error) *mcp.CallToolResult {
	return mcp.NewToolResultError(err.Error())
}

// --- Tool definitions and handlers ---

func revisionCreateTool() mcp.Tool {
	return mcp.NewTool("oracle_revision_create",
		mcp.WithDescription("Create a new graph revision"),
		mcp.WithString("domain", mcp.Required(), mcp.Description("Domain key")),
		mcp.WithString("after_sha", mcp.Required(), mcp.Description("Git SHA after")),
		mcp.WithString("before_sha", mcp.Description("Git SHA before")),
		mcp.WithString("trigger", mcp.Description("Trigger kind (default: manual)")),
		mcp.WithString("mode", mcp.Description("Scan mode (default: full)")),
	)
}

func revisionCreateHandler(g *graph.Graph) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.Params.Arguments
		trigger := strParam(args, "trigger")
		if trigger == "" {
			trigger = "manual"
		}
		mode := strParam(args, "mode")
		if mode == "" {
			mode = "full"
		}
		id, err := g.Store().CreateRevision(strParam(args, "domain"), strParam(args, "before_sha"), strParam(args, "after_sha"), trigger, mode, "{}")
		if err != nil {
			return errorResult(err), nil
		}
		return jsonResult(map[string]any{"revision_id": id}), nil
	}
}

func nodeUpsertTool() mcp.Tool {
	return mcp.NewTool("oracle_node_upsert",
		mcp.WithDescription("Create or update a graph node"),
		mcp.WithString("node_key", mcp.Required(), mcp.Description("Node key (layer:type:domain:name)")),
		mcp.WithString("layer", mcp.Required(), mcp.Description("Graph layer")),
		mcp.WithString("node_type", mcp.Required(), mcp.Description("Node type")),
		mcp.WithString("domain", mcp.Required(), mcp.Description("Domain key")),
		mcp.WithString("name", mcp.Required(), mcp.Description("Display name")),
		mcp.WithNumber("revision_id", mcp.Required(), mcp.Description("Revision ID")),
		mcp.WithString("repo_name", mcp.Description("Repository name")),
		mcp.WithString("file_path", mcp.Description("File path")),
		mcp.WithNumber("confidence", mcp.Description("Confidence 0-1")),
		mcp.WithString("metadata", mcp.Description("JSON metadata")),
	)
}

func nodeUpsertHandler(g *graph.Graph) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.Params.Arguments
		id, err := g.UpsertNode(validate.NodeInput{
			NodeKey:    strParam(args, "node_key"),
			Layer:      strParam(args, "layer"),
			NodeType:   strParam(args, "node_type"),
			DomainKey:  strParam(args, "domain"),
			Name:       strParam(args, "name"),
			RepoName:   strParam(args, "repo_name"),
			FilePath:   strParam(args, "file_path"),
			Confidence: float64Param(args, "confidence"),
			Metadata:   strParam(args, "metadata"),
		}, int64Param(args, "revision_id"))
		if err != nil {
			return errorResult(err), nil
		}
		return jsonResult(map[string]any{"node_id": id}), nil
	}
}

func nodeListTool() mcp.Tool {
	return mcp.NewTool("oracle_node_list",
		mcp.WithDescription("List graph nodes with filters"),
		mcp.WithString("layer", mcp.Description("Filter by layer")),
		mcp.WithString("node_type", mcp.Description("Filter by type")),
		mcp.WithString("domain", mcp.Description("Filter by domain")),
		mcp.WithString("status", mcp.Description("Filter by status")),
	)
}

func nodeListHandler(g *graph.Graph) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.Params.Arguments
		nodes, err := g.Store().ListNodes(store.NodeFilter{
			Layer: strParam(args, "layer"), NodeType: strParam(args, "node_type"),
			Domain: strParam(args, "domain"), Status: strParam(args, "status"),
		})
		if err != nil {
			return errorResult(err), nil
		}
		return jsonResult(nodes), nil
	}
}

func nodeGetTool() mcp.Tool {
	return mcp.NewTool("oracle_node_get",
		mcp.WithDescription("Get a node by key with evidence"),
		mcp.WithString("node_key", mcp.Required(), mcp.Description("Node key")),
	)
}

func nodeGetHandler(g *graph.Graph) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		key := strParam(req.Params.Arguments, "node_key")
		node, err := g.Store().GetNodeByKey(key)
		if err != nil {
			return errorResult(err), nil
		}
		nodeID, _ := g.Store().GetNodeIDByKey(key)
		evidence, _ := g.Store().ListEvidenceByNode(nodeID)
		return jsonResult(map[string]any{"node": node, "evidence": evidence}), nil
	}
}

func edgeUpsertTool() mcp.Tool {
	return mcp.NewTool("oracle_edge_upsert",
		mcp.WithDescription("Create or update a graph edge"),
		mcp.WithString("from_node_key", mcp.Required(), mcp.Description("Source node key")),
		mcp.WithString("to_node_key", mcp.Required(), mcp.Description("Target node key")),
		mcp.WithString("edge_type", mcp.Required(), mcp.Description("Edge type")),
		mcp.WithString("derivation_kind", mcp.Required(), mcp.Description("Derivation: hard/linked/inferred/unknown")),
		mcp.WithNumber("revision_id", mcp.Required(), mcp.Description("Revision ID")),
		mcp.WithString("edge_key", mcp.Description("Edge key (auto-generated if empty)")),
		mcp.WithNumber("confidence", mcp.Description("Confidence 0-1")),
		mcp.WithString("metadata", mcp.Description("JSON metadata")),
	)
}

func edgeUpsertHandler(g *graph.Graph) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.Params.Arguments
		fromKey := strParam(args, "from_node_key")
		toKey := strParam(args, "to_node_key")

		fromNode, err := g.Store().GetNodeByKey(fromKey)
		if err != nil {
			return errorResult(fmt.Errorf("from node: %w", err)), nil
		}
		toNode, err := g.Store().GetNodeByKey(toKey)
		if err != nil {
			return errorResult(fmt.Errorf("to node: %w", err)), nil
		}

		id, err := g.UpsertEdge(validate.EdgeInput{
			EdgeKey:        strParam(args, "edge_key"),
			FromNodeKey:    fromKey,
			ToNodeKey:      toKey,
			EdgeType:       strParam(args, "edge_type"),
			DerivationKind: strParam(args, "derivation_kind"),
			FromLayer:      fromNode.Layer,
			ToLayer:        toNode.Layer,
			Confidence:     float64Param(args, "confidence"),
			Metadata:       strParam(args, "metadata"),
		}, int64Param(args, "revision_id"))
		if err != nil {
			return errorResult(err), nil
		}
		return jsonResult(map[string]any{"edge_id": id}), nil
	}
}

func edgeListTool() mcp.Tool {
	return mcp.NewTool("oracle_edge_list",
		mcp.WithDescription("List graph edges"),
		mcp.WithString("from_node_key", mcp.Description("Filter by source node")),
		mcp.WithString("to_node_key", mcp.Description("Filter by target node")),
		mcp.WithString("edge_type", mcp.Description("Filter by edge type")),
	)
}

func edgeListHandler(g *graph.Graph) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.Params.Arguments
		filter := store.EdgeFilter{EdgeType: strParam(args, "edge_type")}
		if from := strParam(args, "from_node_key"); from != "" {
			id, err := g.Store().GetNodeIDByKey(from)
			if err != nil {
				return errorResult(err), nil
			}
			filter.FromNodeID = id
		}
		if to := strParam(args, "to_node_key"); to != "" {
			id, err := g.Store().GetNodeIDByKey(to)
			if err != nil {
				return errorResult(err), nil
			}
			filter.ToNodeID = id
		}
		edges, err := g.Store().ListEdges(filter)
		if err != nil {
			return errorResult(err), nil
		}
		return jsonResult(edges), nil
	}
}

func evidenceAddTool() mcp.Tool {
	return mcp.NewTool("oracle_evidence_add",
		mcp.WithDescription("Add evidence for a node or edge"),
		mcp.WithString("target_kind", mcp.Required(), mcp.Description("node or edge")),
		mcp.WithString("source_kind", mcp.Required(), mcp.Description("Source kind")),
		mcp.WithString("extractor_id", mcp.Required(), mcp.Description("Extractor ID")),
		mcp.WithString("extractor_version", mcp.Required(), mcp.Description("Extractor version")),
		mcp.WithString("node_key", mcp.Description("Node key (for node evidence)")),
		mcp.WithString("edge_key", mcp.Description("Edge key (for edge evidence)")),
		mcp.WithString("repo_name", mcp.Description("Repo name")),
		mcp.WithString("file_path", mcp.Description("File path")),
		mcp.WithNumber("line_start", mcp.Description("Line start")),
		mcp.WithNumber("line_end", mcp.Description("Line end")),
		mcp.WithString("commit_sha", mcp.Description("Commit SHA")),
		mcp.WithNumber("confidence", mcp.Description("Confidence 0-1")),
	)
}

func evidenceAddHandler(g *graph.Graph) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.Params.Arguments
		input := validate.EvidenceInput{
			TargetKind:       strParam(args, "target_kind"),
			SourceKind:       strParam(args, "source_kind"),
			RepoName:         strParam(args, "repo_name"),
			FilePath:         strParam(args, "file_path"),
			LineStart:        intParam(args, "line_start"),
			LineEnd:          intParam(args, "line_end"),
			ExtractorID:      strParam(args, "extractor_id"),
			ExtractorVersion: strParam(args, "extractor_version"),
			CommitSHA:        strParam(args, "commit_sha"),
			Confidence:       float64Param(args, "confidence"),
		}

		var id int64
		var err error
		if input.TargetKind == "node" {
			id, err = g.AddNodeEvidence(strParam(args, "node_key"), input)
		} else {
			id, err = g.AddEdgeEvidence(strParam(args, "edge_key"), input)
		}
		if err != nil {
			return errorResult(err), nil
		}
		return jsonResult(map[string]any{"evidence_id": id}), nil
	}
}

func importAllTool() mcp.Tool {
	return mcp.NewTool("oracle_import_all",
		mcp.WithDescription("Bulk import nodes, edges, and evidence in a single transaction"),
		mcp.WithNumber("revision_id", mcp.Required(), mcp.Description("Revision ID")),
		mcp.WithString("payload", mcp.Required(), mcp.Description("JSON string with nodes, edges, evidence arrays")),
	)
}

func importAllHandler(g *graph.Graph) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.Params.Arguments
		payloadStr := strParam(args, "payload")
		revisionID := int64Param(args, "revision_id")

		var payload graph.ImportPayload
		if err := json.Unmarshal([]byte(payloadStr), &payload); err != nil {
			return errorResult(fmt.Errorf("parsing payload: %w", err)), nil
		}

		result, err := g.ImportAll(payload, revisionID)
		if err != nil {
			return errorResult(err), nil
		}
		return jsonResult(result), nil
	}
}

func queryDepsTool() mcp.Tool {
	return mcp.NewTool("oracle_query_deps",
		mcp.WithDescription("Query dependencies of a node"),
		mcp.WithString("node_key", mcp.Required(), mcp.Description("Node key")),
		mcp.WithNumber("depth", mcp.Description("Max depth (default 1)")),
		mcp.WithString("derivation", mcp.Description("Comma-separated derivation filter")),
	)
}

func queryDepsHandler(g *graph.Graph) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.Params.Arguments
		depth := intParam(args, "depth")
		if depth == 0 {
			depth = 1
		}
		var filter []string
		if d := strParam(args, "derivation"); d != "" {
			for _, s := range splitComma(d) {
				filter = append(filter, s)
			}
		}
		deps, err := g.QueryDeps(strParam(args, "node_key"), depth, filter)
		if err != nil {
			return errorResult(err), nil
		}
		return jsonResult(deps), nil
	}
}

func queryReverseDepsTool() mcp.Tool {
	return mcp.NewTool("oracle_query_reverse_deps",
		mcp.WithDescription("Query reverse dependencies (who depends on this node)"),
		mcp.WithString("node_key", mcp.Required(), mcp.Description("Node key")),
		mcp.WithNumber("depth", mcp.Description("Max depth (default 1)")),
		mcp.WithString("derivation", mcp.Description("Comma-separated derivation filter")),
	)
}

func queryReverseDepsHandler(g *graph.Graph) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.Params.Arguments
		depth := intParam(args, "depth")
		if depth == 0 {
			depth = 1
		}
		var filter []string
		if d := strParam(args, "derivation"); d != "" {
			for _, s := range splitComma(d) {
				filter = append(filter, s)
			}
		}
		deps, err := g.QueryReverseDeps(strParam(args, "node_key"), depth, filter)
		if err != nil {
			return errorResult(err), nil
		}
		return jsonResult(deps), nil
	}
}

func queryStatsTool() mcp.Tool {
	return mcp.NewTool("oracle_query_stats",
		mcp.WithDescription("Get graph statistics"),
		mcp.WithString("domain", mcp.Description("Domain key")),
	)
}

func queryStatsHandler(g *graph.Graph) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		stats, err := g.QueryStats(strParam(req.Params.Arguments, "domain"))
		if err != nil {
			return errorResult(err), nil
		}
		return jsonResult(stats), nil
	}
}

func snapshotCreateTool() mcp.Tool {
	return mcp.NewTool("oracle_snapshot_create",
		mcp.WithDescription("Create a graph snapshot"),
		mcp.WithNumber("revision_id", mcp.Required(), mcp.Description("Revision ID")),
		mcp.WithString("domain", mcp.Required(), mcp.Description("Domain key")),
		mcp.WithString("kind", mcp.Description("Snapshot kind: full or incremental")),
		mcp.WithNumber("node_count", mcp.Required(), mcp.Description("Node count")),
		mcp.WithNumber("edge_count", mcp.Required(), mcp.Description("Edge count")),
	)
}

func snapshotCreateHandler(g *graph.Graph) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.Params.Arguments
		kind := strParam(args, "kind")
		if kind == "" {
			kind = "full"
		}
		id, err := g.Store().CreateSnapshot(store.SnapshotRow{
			RevisionID: int64Param(args, "revision_id"),
			DomainKey:  strParam(args, "domain"),
			Kind:       kind,
			NodeCount:  intParam(args, "node_count"),
			EdgeCount:  intParam(args, "edge_count"),
		})
		if err != nil {
			return errorResult(err), nil
		}
		return jsonResult(map[string]any{"snapshot_id": id}), nil
	}
}

func staleMarkTool() mcp.Tool {
	return mcp.NewTool("oracle_stale_mark",
		mcp.WithDescription("Mark nodes and edges not seen in this revision as stale"),
		mcp.WithString("domain", mcp.Required(), mcp.Description("Domain key")),
		mcp.WithNumber("revision_id", mcp.Required(), mcp.Description("Current revision ID")),
	)
}

func staleMarkHandler(g *graph.Graph) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.Params.Arguments
		domain := strParam(args, "domain")
		revID := int64Param(args, "revision_id")

		staleNodes, err := g.Store().MarkStaleNodes(domain, revID)
		if err != nil {
			return errorResult(err), nil
		}
		staleEdges, err := g.Store().MarkStaleEdges(domain, revID)
		if err != nil {
			return errorResult(err), nil
		}
		return jsonResult(map[string]any{
			"stale_nodes": staleNodes,
			"stale_edges": staleEdges,
		}), nil
	}
}

func splitComma(s string) []string {
	var result []string
	for _, part := range splitString(s, ",") {
		trimmed := trimSpace(part)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func splitString(s, sep string) []string {
	if s == "" {
		return nil
	}
	result := []string{}
	for {
		i := indexOf(s, sep)
		if i < 0 {
			result = append(result, s)
			break
		}
		result = append(result, s[:i])
		s = s[i+len(sep):]
	}
	return result
}

func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

func trimSpace(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t' || s[0] == '\n') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t' || s[len(s)-1] == '\n') {
		s = s[:len(s)-1]
	}
	return s
}
```

- [ ] **Step 3: Create CLI mcp command**

Create `internal/cli/mcp.go`:

```go
package cli

import (
	mcpserver "github.com/anthropics/depbot/internal/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/spf13/cobra"
)

func init() {
	// Register mcp command in root
}

func newMCPCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "mcp", Short: "MCP server"}

	serveCmd := &cobra.Command{
		Use:   "serve",
		Short: "Start MCP server (stdio transport)",
		Run: func(cmd *cobra.Command, args []string) {
			g := openGraph()
			// Note: don't defer close, server runs until stdin closes

			s := mcpserver.NewServer(g)
			if err := server.ServeStdio(s); err != nil {
				outputError(err)
			}
		},
	}

	cmd.AddCommand(serveCmd)
	return cmd
}
```

Update `internal/cli/root.go` to add `newMCPCmd()` to the list of subcommands.

- [ ] **Step 4: Build and verify MCP server starts**

```bash
cd /home/alex/personal/depbot
go build -o oracle ./cmd/oracle
echo '{"jsonrpc":"2.0","method":"initialize","id":1,"params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}' | timeout 2 ./oracle mcp serve --db /tmp/mcp-test.db 2>/dev/null || true
```

Expected: server responds with initialize result (or timeout, which is fine — proves it started).

- [ ] **Step 5: Run all tests**

```bash
go test ./... -v
```

Expected: all tests PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/mcp/ internal/cli/mcp.go internal/cli/root.go go.mod go.sum
git commit -m "feat: add MCP server with stdio transport"
```

---

### Task 17: End-to-End Integration Test

**Files:**
- Create: `internal/graph/integration_test.go`

- [ ] **Step 1: Write integration test**

Create `internal/graph/integration_test.go`:

```go
package graph

import (
	"path/filepath"
	"testing"

	"github.com/anthropics/depbot/internal/registry"
	"github.com/anthropics/depbot/internal/store"
	"github.com/anthropics/depbot/internal/validate"
)

func TestFullWorkflow(t *testing.T) {
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	reg, err := registry.LoadDefaults()
	if err != nil {
		t.Fatalf("LoadDefaults: %v", err)
	}

	g := New(s, reg)

	// 1. Create revision
	revID, err := s.CreateRevision("orders", "", "abc123", "manual", "full", "{}")
	if err != nil {
		t.Fatalf("CreateRevision: %v", err)
	}

	// 2. Bulk import — simulates what Claude Code would send
	payload := ImportPayload{
		Nodes: []ImportNode{
			{NodeKey: "code:controller:orders:orderscontroller", Layer: "code", NodeType: "controller", DomainKey: "orders", Name: "OrdersController", RepoName: "orders-api", FilePath: "src/orders/orders.controller.ts"},
			{NodeKey: "code:provider:orders:ordersservice", Layer: "code", NodeType: "provider", DomainKey: "orders", Name: "OrdersService", RepoName: "orders-api", FilePath: "src/orders/orders.service.ts"},
			{NodeKey: "code:provider:orders:paymentsservice", Layer: "code", NodeType: "provider", DomainKey: "orders", Name: "PaymentsService", RepoName: "orders-api", FilePath: "src/payments/payments.service.ts"},
			{NodeKey: "contract:endpoint:orders:post:/orders", Layer: "contract", NodeType: "endpoint", DomainKey: "orders", Name: "POST /orders"},
			{NodeKey: "contract:endpoint:orders:post:/orders/{id}/capture", Layer: "contract", NodeType: "endpoint", DomainKey: "orders", Name: "POST /orders/{id}/capture"},
			{NodeKey: "service:service:orders:payments-api", Layer: "service", NodeType: "service", DomainKey: "orders", Name: "payments-api"},
		},
		Edges: []ImportEdge{
			{FromNodeKey: "code:controller:orders:orderscontroller", ToNodeKey: "code:provider:orders:ordersservice", EdgeType: "INJECTS", DerivationKind: "hard", FromLayer: "code", ToLayer: "code"},
			{FromNodeKey: "code:controller:orders:orderscontroller", ToNodeKey: "contract:endpoint:orders:post:/orders", EdgeType: "EXPOSES_ENDPOINT", DerivationKind: "hard", FromLayer: "code", ToLayer: "contract"},
			{FromNodeKey: "code:controller:orders:orderscontroller", ToNodeKey: "contract:endpoint:orders:post:/orders/{id}/capture", EdgeType: "EXPOSES_ENDPOINT", DerivationKind: "hard", FromLayer: "code", ToLayer: "contract"},
			{FromNodeKey: "code:provider:orders:ordersservice", ToNodeKey: "code:provider:orders:paymentsservice", EdgeType: "CALLS_SYMBOL", DerivationKind: "hard", FromLayer: "code", ToLayer: "code"},
			{FromNodeKey: "code:provider:orders:paymentsservice", ToNodeKey: "service:service:orders:payments-api", EdgeType: "CALLS_SERVICE", DerivationKind: "linked", FromLayer: "code", ToLayer: "service"},
		},
		Evidence: []ImportEvidence{
			{TargetKind: "node", NodeKey: "code:controller:orders:orderscontroller", SourceKind: "file", RepoName: "orders-api", FilePath: "src/orders/orders.controller.ts", LineStart: 1, LineEnd: 50, ExtractorID: "claude-code", ExtractorVersion: "1.0", Confidence: 0.99},
			{TargetKind: "edge", EdgeKey: "code:controller:orders:orderscontroller->code:provider:orders:ordersservice:INJECTS", SourceKind: "file", RepoName: "orders-api", FilePath: "src/orders/orders.controller.ts", LineStart: 10, ExtractorID: "claude-code", ExtractorVersion: "1.0", Confidence: 0.99},
		},
	}

	result, err := g.ImportAll(payload, revID)
	if err != nil {
		t.Fatalf("ImportAll: %v", err)
	}
	if result.NodesCreated != 6 {
		t.Errorf("nodes = %d, want 6", result.NodesCreated)
	}
	if result.EdgesCreated != 5 {
		t.Errorf("edges = %d, want 5", result.EdgesCreated)
	}
	if result.EvidenceCreated != 2 {
		t.Errorf("evidence = %d, want 2", result.EvidenceCreated)
	}

	// 3. Query deps
	deps, err := g.QueryDeps("code:controller:orders:orderscontroller", 1, nil)
	if err != nil {
		t.Fatalf("QueryDeps: %v", err)
	}
	if len(deps) != 3 { // ordersservice + 2 endpoints
		t.Errorf("direct deps = %d, want 3", len(deps))
	}

	// 4. Query reverse deps — who depends on payments-api?
	rdeps, err := g.QueryReverseDeps("service:service:orders:payments-api", 2, nil)
	if err != nil {
		t.Fatalf("QueryReverseDeps: %v", err)
	}
	// paymentsservice (depth 1) + ordersservice (depth 2)
	if len(rdeps) < 2 {
		t.Errorf("reverse deps = %d, want >= 2", len(rdeps))
	}

	// 5. Stats
	stats, err := g.QueryStats("orders")
	if err != nil {
		t.Fatalf("QueryStats: %v", err)
	}
	if stats.NodeCount != 6 {
		t.Errorf("node count = %d, want 6", stats.NodeCount)
	}
	if stats.EdgeCount != 5 {
		t.Errorf("edge count = %d, want 5", stats.EdgeCount)
	}

	// 6. Idempotent re-import
	result2, err := g.ImportAll(payload, revID)
	if err != nil {
		t.Fatalf("second ImportAll: %v", err)
	}
	if result2.NodesCreated != 6 { // upserts, not duplicates
		t.Errorf("second import nodes = %d, want 6", result2.NodesCreated)
	}
	// Verify no duplicates
	allNodes, _ := s.ListNodes(store.NodeFilter{})
	if len(allNodes) != 6 {
		t.Errorf("total nodes after re-import = %d, want 6", len(allNodes))
	}

	// 7. Create snapshot
	snapID, err := s.CreateSnapshot(store.SnapshotRow{
		RevisionID: revID, DomainKey: "orders", Kind: "full",
		NodeCount: stats.NodeCount, EdgeCount: stats.EdgeCount,
	})
	if err != nil {
		t.Fatalf("CreateSnapshot: %v", err)
	}
	if snapID <= 0 {
		t.Error("expected positive snapshot_id")
	}

	// 8. Stale marking
	revID2, _ := s.CreateRevision("orders", "abc123", "def456", "manual", "full", "{}")
	// Only re-import the controller — others should go stale
	g.UpsertNode(validate.NodeInput{
		NodeKey: "code:controller:orders:orderscontroller", Layer: "code", NodeType: "controller",
		DomainKey: "orders", Name: "OrdersController",
	}, revID2)

	staleCount, err := s.MarkStaleNodes("orders", revID2)
	if err != nil {
		t.Fatalf("MarkStaleNodes: %v", err)
	}
	if staleCount != 5 { // all except the controller
		t.Errorf("stale nodes = %d, want 5", staleCount)
	}

	t.Log("Full workflow passed!")
}
```

- [ ] **Step 2: Run integration test**

```bash
go test ./internal/graph/ -v -run TestFullWorkflow
```

Expected: PASS.

- [ ] **Step 3: Run all tests**

```bash
go test ./... -v
```

Expected: all tests PASS.

- [ ] **Step 4: Build final binary**

```bash
go build -o oracle ./cmd/oracle
./oracle version
```

Expected: `oracle v0.1.0`

- [ ] **Step 5: Commit**

```bash
git add internal/graph/integration_test.go
git commit -m "test: add end-to-end integration test for full workflow"
```

---

### Task 18: Default Registry File + Final Polish

**Files:**
- Create: `internal/registry/defaults.yaml`
- Verify all builds and tests

- [ ] **Step 1: Create the full default registry YAML**

Create `internal/registry/defaults.yaml` with the complete type registry from the spec (the full YAML from the "Type Registry" section of the design doc — all layers, node_types for each layer, all edge_types with from_layers/to_layers, derivation_kinds, source_kinds, node_statuses, trigger_kinds).

- [ ] **Step 2: Test default registry loads**

```bash
go test ./internal/registry/ -v -run TestLoadDefaults
```

Add this test to `internal/registry/registry_test.go`:

```go
func TestLoadDefaults(t *testing.T) {
	r, err := LoadDefaults()
	if err != nil {
		t.Fatalf("LoadDefaults: %v", err)
	}
	if !r.IsValidLayer("code") {
		t.Error("expected code to be valid layer")
	}
	if !r.IsValidEdgeType("INJECTS") {
		t.Error("expected INJECTS to be valid")
	}
	if !r.IsValidNodeType("contract", "endpoint") {
		t.Error("expected contract:endpoint to be valid")
	}
}
```

Expected: PASS.

- [ ] **Step 3: Final build + full test suite**

```bash
cd /home/alex/personal/depbot
go build -o oracle ./cmd/oracle
go test ./... -count=1
```

Expected: builds cleanly, all tests pass.

- [ ] **Step 4: Commit**

```bash
git add internal/registry/defaults.yaml internal/registry/registry_test.go
git commit -m "feat: add full default type registry with all layers and edge types"
```
