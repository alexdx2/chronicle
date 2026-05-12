# Scan Pipeline

Chronicle extracts architecture from code using a hybrid approach: tree-sitter AST parsing handles deterministic patterns (imports, decorators, DI), a rules engine maps framework syntax to semantic meaning, and LLM agents classify ambiguous patterns that require context. The result is a multi-phase pipeline that balances speed, accuracy, and cost.

## Three layers: AST → Rules → LLM

### 1. AST extraction

Tree-sitter parses source files and emits `RawFact` structs — pure syntax, no framework interpretation:

```go
type RawFact struct {
    Kind       string   // import, decorator, constructor_param, call, member_call, produces_candidate
    Name       string   // decorator name, class name
    To         string   // import path, type name
    From       string   // object in member_call chain
    Symbols    []string // imported symbols
    Method     string   // called method, decorated method
    Args       string   // raw decorator arguments
    Target     string   // first string arg from decorator
    TargetKind string   // what the decorator is on: class, method
}
```

This layer finds every decorator, import, constructor parameter, and function call. It does not decide what they mean.

### 2. Rules engine

Framework-specific rulesets transform raw facts into `SemanticFact` structs:

```go
type SemanticFact struct {
    FromType string // controller, module, provider
    ToType   string
    EdgeType string // INJECTS, EXPOSES, CONTAINS, IMPORTS
    Endpoint string // GET /users/:id
}
```

A `DecoratorRule` for NestJS maps `@Controller('users')` to a controller node, `@Get(':id')` to an endpoint edge, `@Module({ imports: [...] })` to IMPORTS edges. Rules are pluggable — custom packs extend this for other frameworks.

### 3. LLM classification (candidates)

Some patterns are ambiguous at the AST level:

```go
type Candidate struct {
    ID             string // stable: filepath:kind:code_hash
    Kind           string // call, member_call, fetch_call, emit_call, env_access
    Code           string // source text of the expression
    CodeHash       string // sha1 of normalized code
    Line           int
    Receiver       string // object being called
    Method         string // method name
    Context        string // surrounding class/function name
    ResolvedType   string // PascalCase type from constructor params
    ReceiverOrigin string // "constructor_param", "import", "local"
}
```

Examples: `this.httpService.get(url)` — is that an internal or external call? What service does it reach? `this.client.emit('order.created', payload)` — what topic, what schema? AST finds these, LLM classifies them into cross-service edges.

## Scan pipeline stages

### Discovery

`chronicle_scan_status` detects file groups (by directory + tech stack), counts files, identifies frameworks. This determines what instruction packs to load.

### Scope selection

User picks scan scope — which directories/services to include. Variants A through D offer different granularity levels.

### Instruction packs

Per-framework extraction guides tell agents exactly what patterns to look for. Loaded from defaults (NestJS, Prisma built-in) or custom-created by a sonnet agent analyzing unfamiliar code.

Example pack content: "In NestJS, `@Injectable()` classes are providers. Constructor parameters with type annotations are DI injections. Look for `@EventPattern()` for Kafka consumers."

### Phase 1 — breadth (haiku x3-5)

Parallel haiku agents each receive a file group and an instruction pack. They read files and emit structured extractions:

- Nodes: modules, controllers, providers, models
- Edges: INJECTS, EXPOSES, CONTAINS, IMPORTS
- Candidates: ambiguous patterns for phase 2

Haiku is fast and cheap — good for deterministic patterns across many files.

### Phase 2 — depth (sonnet)

Sonnet agents trace complex flows on trigger files identified in phase 1. They receive enriched context: the current graph neighborhood, related files to read, reachable nodes.

Output: flow nodes, TRIGGERS_FLOW edges, REQUIRES edges. These capture multi-step business logic that spans services.

### Resolve

Deduplicate nodes (same service discovered by multiple agents), resolve cross-references (string names to node keys), normalize keys, and import everything into the graph with evidence.

## Evidence and provenance

Every fact stored in the graph carries evidence:

- **file** — source file path
- **line** — line number
- **confidence** — 0.0 to 1.0
- **derivation_kind** — `ast_extraction`, `rule_mapping`, `llm_classification`, `flow_tracing`

This enables trust scoring and incremental invalidation.

## Incremental updates

When files change:

1. `chronicle_invalidate_changed` marks evidence from those files as stale
2. Stale evidence reduces trust scores on affected nodes/edges
3. Re-scanning only the changed files produces fresh evidence
4. Trust recalculates — confirmed facts restore confidence, removed patterns drop nodes

A 6000-file project with 3 changed files re-scans in seconds. The graph stays current without full re-extraction.

## Cost model

| Phase | Model | Files | Purpose |
|-------|-------|-------|---------|
| Phase 1 | haiku x3-5 | all files | breadth extraction |
| Phase 2 | sonnet x1-3 | trigger files only | flow tracing |
| Custom packs | sonnet x1 | sample files | framework learning |

Typical full scan of a 40-file project: ~3-5 haiku calls + 1-3 sonnet calls. Incremental: 1 haiku call for changed files.
