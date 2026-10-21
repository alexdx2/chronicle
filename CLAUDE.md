# Chronicle MCP — Developer Guide

## Project overview

Chronicle MCP is a persistent knowledge graph for Claude Code. Go binary, SQLite storage, embedded admin dashboard. Published to npm as `@alexdx/chronicle-mcp`.

Two repos:
- **chronicle-core** (this repo) — single-repo graph engine, CLI, MCP server, dashboard
- **chronicle-pro** (`../chronicle-pro`) — multi-repo federation, extends core

## OSS vs Pro — what goes where

This repo is the open-source core. Chronicle Pro is a separate private repo that imports core as a dependency.

**Rule of thumb:** if it works with one `.depbot/` database, it belongs in core. If it needs multiple databases or cross-repo awareness, it belongs in pro.

### Core (this repo) — OSS

| Feature | Why it's here |
|---------|---------------|
| Graph engine (`graph/`) | Single-repo BFS, impact, path queries |
| Store (`store/`) | SQLite schema, node/edge/evidence/alias CRUD |
| Registry (`registry/`) | Type system — layers, node types, edge types, derivation kinds |
| Validation (`validate/`) | Key normalization, input validation |
| CLI (`internal/cli/`) | `chronicle scan`, `import`, `query`, `alias`, etc. |
| MCP server (`internal/mcp/`) | 25+ tools for Claude (import, query, evidence, diagrams) |
| Admin dashboard (`admin/`) | Overview, graph explorer, language, settings, diagrams |
| `external` node status | Boundary marker — node exists but is defined elsewhere |
| `node_aliases` table | Name/DNS/topic aliases for resolution |
| `GraphQuerier` interface | Abstraction that pro implements for federation |
| `GraphDiscoverer` interface | Abstraction for finding `.depbot/` dirs |
| `manual_resolution` source kind | General evidence kind, not enterprise-specific |

### Pro (`../chronicle-pro`) — private

| Feature | Why it's there |
|---------|----------------|
| `FederatedGraph` | Cross-repo BFS — resolves external nodes, continues traversal into other repos |
| `MultiRepoDiscoverer` | Scans a workspace directory for multiple `.depbot/` dirs |
| Federated MCP server | Same tools as core but backed by FederatedGraph |
| Federated admin dashboard | Core dashboard + federation bar, federation tab, repo selector, resolve UI |
| Manual resolution flow | Writes alias on target node (target repo) + evidence on external (source repo) |
| Cross-repo impact analysis | Impact traversal that crosses repo boundaries via external node resolution |

### Boundary decisions

- **Interfaces in core, implementations split.** `GraphQuerier` is in core (both `Graph` and `FederatedGraph` implement it). `GraphDiscoverer` is in core (`SingleRepoDiscoverer` in core, `MultiRepoDiscoverer` in pro).
- **External nodes in core.** The `external` status and `node_aliases` table are core features. A single repo can have external nodes without federation — they just won't resolve until pro connects the dots.
- **Resolution logic in pro.** The `ResolveExternal()` method that matches external nodes against aliases across repos is pro-only. Core stores the data, pro does the matching.
- **Dashboard: one template, slots.** Pro renders core's `index.html` through `admin.RenderDashboard(DashboardSlots)` and injects federation UI at the `SLOT:*` markers (tabs, state, routes, methods, view bar). Add a slot in core rather than forking the file.

## Architecture

```
cmd/chronicle/          CLI entry point
graph/                  Graph engine (public — imported by pro)
store/                  SQLite storage (public)
registry/               Type registry + defaults.yaml (public)
validate/               Key normalization + validation (public)
internal/cli/           CLI commands (internal — not importable by pro)
admin/                  Dashboard server + static/ (public — importable by pro)
live/                   Agent ↔ dashboard channel (live_events, screen state, diagram tail) (public)
snapshot/                Canonical graph.jsonl — encode/decode/export/load/freshness/merge (public)
review/                  Compare two graph versions — diff, classify, impact, findings, renderers (public)
internal/mcp/           MCP server + tools (internal)
internal/manifest/      Domain manifest parsing (internal)
```

Public packages (`graph/`, `store/`, `registry/`, `validate/`) are importable by chronicle-pro. Internal packages are only used by this binary. This is enforced by Go's `internal/` convention.

**CRITICAL: MCP tool registration has TWO places.**
When adding a new MCP tool, you MUST register it in BOTH:
1. `internal/mcp/server.go` → `NewServer()` — used by tests
2. `internal/mcp/middleware.go` → `NewServerWithLogging()` — used by CLI (`chronicle mcp serve`)

If you only add to `server.go`, tests pass but the tool won't appear in production.
If you only add to `middleware.go`, tests won't cover it.

## Event journal

Every graph mutation is recorded as a semantic event in `.depbot/events/<domain>.jsonl` (append-only, git-tracked, `merge=union`). The journal is the source of truth; `chronicle.db` is a derived, reproducible cache.

CLI (`internal/cli/journal.go`):
- `chronicle journal init` — bootstrap from an existing db (genesis events)
- `chronicle journal flush` — flush pending outbox events to the jsonl files (`journal status` shows totals)
- `chronicle journal rebuild` — replay journal → fresh db (carry-over of local state + atomic swap)
- `chronicle journal verify` — shadow validator: replay into a temp db, diff against the live db

Sync on open: every command applies journal events the db hasn't seen yet, so events merged in via `git pull` materialize automatically.

**Contributor rules:**
- NEVER mutate `graph_*` tables with raw SQL. Every mutation goes through the instrumented store methods so it journals. Scan-lab runs `journal verify` as a shadow validator — raw-SQL mutations make replay diverge and fail it.
- Trust/confidence are derived from evidence (asserter-tier caps, verification promotion) and recomputed on read — never journaled. If a choke point forces a status, it must adjust the evidence in the same write so the derived state stays coherent.

## Graph snapshot + review

`.depbot/graph.jsonl` is the committed, canonical state of the graph — one current record per
node/edge/term/coverage entry, evidence with file/blob/line-range. `chronicle graph update` writes
it from the local database; `chronicle graph status`/`show` report freshness and the nearest graph
version in a revision's git history; `chronicle graph migrate` backfills blobs from an
already-committed event journal. Conflicts on the file regenerate the disputed records instead of
a text merge (`.gitattributes` registers a `chronicle-graph` merge driver via `chronicle graph merge`).

`chronicle review <base>...<head>` compares two committed graph versions (no LLM): what changed
(added/removed/changed/touched facts), what it means (`ChangeClassifier`: integration/producer/
consumer/contract changes, direction-aware), who it may affect (`ImpactAnalyzer`, collapsed to
services/domains), and what the graph does not know (files changed after their last scan). See
`docs/superpowers/specs/2026-09-25-graph-snapshot-contract-design.md` and
`2026-09-25-review-engine-design.md` (outer repo).

`chronicle graph restore` materialises a committed snapshot into a fresh local database, the first
step of a CI job that has no scan history. It reads `.depbot/graph.jsonl` from the working tree, or
from a revision with `--at <rev>`; `--merge-base-with <rev>` resolves `--at` as the merge base of the
two (CI: `--at origin/main --merge-base-with HEAD`). It refuses to replace an existing database
unless `--force` is given. It also registers one structural "restore" scan revision per domain
(trigger `ci_pipeline`, metadata kind `restore`) pinned to the snapshot's commit, so `chronicle
refresh` diffs from that commit, and it marks the on-disk event journal as consumed.

`chronicle extract` re-extracts with an LLM the files a range touched. It diffs from the merge base
of `--base` and `--head` (like `review A...B`), adds the graph neighbourhood of the changed files
within `--radius` (default 1), and caps the read set by `--max-files` (80) and `--max-bytes` (1 MiB).
`--plan` prints that plan as JSON and calls no model; `--files` and `--plan-file` override it.
`--model` takes `anthropic:<model>`, `gemini:<model>`, `openai:<model>` (any OpenAI-compatible
server via `OPENAI_BASE_URL`), `openrouter:<model>`, `ollama:<tag>` or `fake:<dir>` (default
`$CHRONICLE_MODEL`, else the built-in Anthropic model); keys come from `ANTHROPIC_API_KEY`,
`GEMINI_API_KEY` (`GOOGLE_API_KEY` also works), `OPENAI_API_KEY`, `OPENROUTER_API_KEY`; Ollama needs
none (`OLLAMA_HOST`). OpenRouter reports the cost of every call, which `usage.json` carries as is. Without a key it prints `{"error":...,"skipped":true}` and exits 3,
so a pipeline can tell "skipped" from "failed". `--usage-out <file>` writes the full run result
JSON (token usage included).

`chronicle review --head-snapshot <file>` reads the head graph from a snapshot file instead of the
head revision's committed one (freshness is still judged against the head revision's tree), which is
how CI reviews a graph it just rebuilt. `--bundle <zip>` also writes a review bundle: both graphs,
the JSON and Markdown reports, a manifest, and the `--usage` JSON if given. `chronicle review import
<zip>` unpacks a bundle into `<chronicle dir>/reviews/<base>..<head>/` and publishes its full
diagram, with the changed and affected marks, to the local dashboard. A review with no changed
relationships has nothing to diagram: the bundle is still unpacked, nothing is published, exit 0.

`chronicle review narrate --review <review.json> [--changed <file>] [--model ...] [--max-words 120] [--out <file>] [--usage-out <file>]`
asks a model to read the deterministic review and write one short paragraph: what the change means, what may be
affected, what to test. It is the only model-written part of the MR comment, and the output says so (heading
"What this means (model's reading)" plus a footer naming the model); the tables stay the facts. Cost is about
1-5 cents per call. Same `--model` resolution and exit codes as `extract` (3 = no key, prints `skipped`); CI treats
a failure as optional. The `fake:` backend keys fixtures by `Review: <head commit>` (`Review__<commit>.json`).

CI shape on a merge request: `graph restore --at origin/main --merge-base-with HEAD` → `refresh` →
`extract` → `graph update` → `review --head-snapshot <fresh graph> --bundle`. On the default branch
the same update runs, followed by a bot commit of `.depbot/graph.jsonl`. Two properties to keep in
mind. A restored database carries no journal events, so `journal verify` on it diverges; that is
expected. And the contents of a merge request's files reach the model, so an MR can influence its
own review; the facts it can produce are bounded by the fact schema and by resolution against the
graph, and nothing from them is ever executed.

**Graph publications (team mode, spec 2026-10-10-team-chronicle-server-design.md).** Besides the
in-tree `.depbot/graph.jsonl`, a graph version can live as a *publication commit* on a branch
`chronicle/graphs/<code-branch>` (one commit per published snapshot, trailers `Chronicle-Code-Sha`,
`Chronicle-Snapshot-Id`, `Chronicle-Code-Ref`, `Chronicle-Base`, `Chronicle-Schema`, `Chronicle-Force`)
or `chronicle/heads/<code-sha>` (one MR head). Records with duplicated or multi-line trailers are
skipped with a warning.

`snapshot.ResolveVersion` / `ResolveVersionWith(repoRoot, rev, ResolveOptions{IncludeHeads, Verifier})`
is candidate-first. With no `chronicle/graphs/*` refs and no requested head refs it goes straight to
the in-tree history and never loads the trust list. Otherwise it collects candidates from the chain
refs (a local chain ref is ignored when its remote-tracking twin exists) without signature checks, orders them by position in
`git rev-list --topo-order <rev>`, then ref order, then distance from the tip (a requested head ranks
first at its own sha), and verifies signatures only for chain candidates, one at a time, until one is
trusted. Heads named in `IncludeHeads` are accepted without signature checks (CLI: `graph show` /
`graph status --include-head <sha>`). If no candidate is accepted, the in-tree history is the
fallback. `graph show` JSON carries `source`, `code_sha`, `snapshot_id`, `ref`, `signature`;
`chronicle doctor` prints a "graph publications" section.

Trust: `snapshot.LoadVerifier` is fail-closed. It reads `publishers` from the manifest at the origin
default branch (origin/HEAD, then refs/remotes/origin/main; if an `origin` remote exists but neither
resolves, it errors (fail-closed); only without an `origin` remote does it fall back to local `main`,
then `HEAD`), never from the working tree. Only when the manifest is absent or has no publishers does it return TrustAll (with
a warning). With publishers, only chain commits with a good SSH signature from `allowed_signers`
count. Heads are never signed. `Verifier.GitArgs()` supplies `gpg.ssh.allowedSignersFile` /
`revocationFile`, and signatures are checked through `git log` with `%G?`.

`snapshot.ContentID` (`ContentIDFiles(Files{Graph, Domain, Types, Profile})`) is the content identity (sha256 over the re-encoded graph
with `Seq=0`, manifest, types and, for a version 2 snapshot, `profile.json`); it errors on input without a snapshot header or with an unrecognized format, and must be computed from
exported files, not from a store.

Profile, dependencies, packs, coverage version (layered headless scan; `chronicle scan --full --out <dir>`
builds it, `--profile-model <spec>` picks the profiling model, `--no-profile` takes the seed): a snapshot
build runs inventory -> deps (deterministic manifest parsing) -> profile (one model request; on any failure the
deterministic seed, with `seed_reason`) -> packs catalog (project > fleet > builtin, one pack per id) ->
extraction by layers (contracts, structure, integrations, tests) -> layer checks. The coverage version of an
extracted file is `fact-schema@N+packs:<hash>` (hash over the applied packs' `id@version`), so changing a pack
makes only the files it covers stale. `.depbot/profile.json` is the fourth snapshot file (technologies, deps
publishes/depends, packs, checks, scope source, `seeded`, limitations); the snapshot format version is 2 (v1 is still read).
An incremental build inherits the previous profile (and its manifest) when the profile model fails or is off. With a model
window (Ollama and other local backends) the profile prompt shrinks to fit: inventory samples first, then repeated
dependencies, then dependency lines, and what was dropped is stated in `limitations`.

Snapshot format version 2 is written by every exporter from this change on, so a repository with a committed v1
`graph.jsonl` sees one spurious `chronicle graph update --check` mismatch: after upgrading core run
`chronicle graph update` once and commit the result; `graph update --check` fails until then.

## Signal pipeline (scan finalize)

Every `FinalizeIncrementalScan` runs a best-effort post-pass chain
(`graph/invalidation.go`), each signal stored as verifiable evidence:

| Pass | Signal | Evidence row | Confidence |
|------|--------|--------------|------------|
| `ComputeASTComplexity` | cyclomatic / loop_count / loop_depth (TS AST, exact) + cognitive/smells (heuristic) | `complexity-ast`, `complexity-smells` | 1.0 exact / 0.6 heuristic, verified-at-creation by recompute |
| `ComputeGraphComplexity` | recursive, transitive_loop_depth over CALLS_SYMBOL ∪ INJECTS (Tarjan SCC) | `complexity-graph` | binding-path |
| `ComputeGitSignals` | per-node churn + `CHANGES_WITH` co-change edges (score = co/min(a,b), ≥3 co-changes) | `churn-git`, `coupling-git` | 1.0 exact / score |
| `ComputeTestSignals` | has_test_file by naming convention (.spec/.test/__tests__/_test.go) | `test-link` | 0.7 |
| `ComputeSimilarity` | `SIMILAR_TO` near-clones (MinHash 64-perm, jaccard ≥ 0.8; fp cached in Metadata) | `similarity-minhash` | jaccard |

Rules when touching this chain:
- `CHANGES_WITH`/`SIMILAR_TO` stay in `structural_edge_types` (registry) — analytical edges must never inflate impact blast radius.
- Incremental finalizes scope file passes via `revisionFileScope` (file obligations); no obligations = full scan = full pass.
- Evidence re-observation refreshes assertion + verification fields AND journals them — `journal verify` diverges otherwise.
- Insights consume the signals: hot_score = normComplexity × degree × staleness × churn(≤2×) × untested(1.5×); dashboard reads `/api/insights`.

## Scan configuration (`.depbot/scan.yaml`)

A declarative, per-project layer over the built-in scan: which files, where addresses and channel names live, how channels look in code and documents. Package `truth/scancfg` (types, loader, validation, layers, template engine, document rules); `scan/scancfg.go` applies it in the config pass (results land in `graph.ConfigHosts`: `RuleChannels`, `Aliases`, `Hints`, key entries); the resolver turns `RuleChannels` into produces/consumes/declares facts (`injectRuleChannels`) and rewrites aliased hosts. Layers, weakest first: `scan/builtin/*.yaml` (embedded), the fleet dir (`CHRONICLE_SCAN_CONFIG_DIR`, Pro uses `<data>/scan-config`), the project file. A rule with the same id replaces the weaker one.

- Call templates: `client.send({name}` (string literal or identifier resolved through same-file constants and config keys), `{any}` skips one argument, `{rest}` skips everything up to the next anchor inside one statement/block (never across `;` or a brace: ksql `WITH ({rest} KAFKA_TOPIC={name} {rest}) AS SELECT`, Terraform `resource "kafka_topic" {any} { {rest} name = {name}`), `ignore_case: true` on a rule, `side: define` with `{id} = app.topic({name}` binds an identifier repo-wide and `{ref}` looks it up (`@app.agent({ref})`). `when` gates: ext, glob, contains, imports (import-like lines only). A match inside a string literal is not a match.
- Document rules: `glob` takes `**`, `*`, `?`, `{a,b}`; `side_by_key` is a substring test on the full dotted key path; `{doc:path}` is absolute. A model-written layer cannot mark rules `reviewed` (Parse drops it).
- Provenance: `provenance: llm:<model>` marks a model-written layer; its rules are unreviewed. The independent reference (`truth/extract/scancfg.go`) executes only `reviewed: true` project rules, so the oracle never grades a rule with its own output. Builtin and fleet rules count as reviewed.
- Coverage version gets `+scan-config@<hash>`: changing the configuration re-extracts the files it touches.
- Tools: `chronicle scan-config get|validate|dry-run|explain|save|author` and the MCP tool `chronicle_scan_config` (action enum; engine injected by `internal/cli/scan_config.go` because `scan` imports `headless` imports `mcpserver`). `save` refuses a candidate that removes facts the current configuration finds unless `--accept-removed`; `dry-run` compares with `truth-expected.json` when the repo has one.
- Not in v1: `routes` only reach the model as hints; SQL documents are not decoded (code rules with `ext: [.sql, .ksql]` work).
- Spec: `docs/superpowers/specs/2026-10-20-project-scan-config-design.md` (outer repo). Builtin rules have Go twins in `truth/extract`; `scan/builtin_test.go` keeps them in agreement.

## Build and test

```bash
go build ./...              # build
go test ./...               # all tests (~30s)
go test ./e2e/ -v           # e2e tests only (tom-and-jerry fixture)
go test ./graph/ -v         # graph engine tests
air                         # hot-reload dev server
```

## Release / publish to npm

CI handles everything — just tag and push:

```bash
# 1. Update version in internal/cli/root.go
#    fmt.Println("chronicle v0.X.0")

# 2. Commit, tag, push
git add internal/cli/root.go
git commit -m "release: v0.X.0 — description"
git tag v0.X.0
git push origin main v0.X.0
```

CI pipeline (`.github/workflows/ci.yml`):
1. **build** — cross-compile linux/mac/windows x64/arm64
2. **release** — create GitHub Release with binaries
3. **publish-npm** — syncs version from tag, publishes `@alexdx/chronicle-mcp`

npm package: `@alexdx/chronicle-mcp` (user: `alexdx`)
The npm wrapper (`npm/`) auto-downloads the Go binary on `postinstall`.

If CI npm publish fails (2FA), manually:
```bash
cd npm && npm version 0.X.0 --no-git-tag-version && npm publish --access public
```

## Fixtures / showcase

`fixtures/tom-and-jerry/` is the showcase project — 4 NestJS microservices with Prisma, HTTP, Kafka. Used by:
- e2e tests (`e2e/tomandjerry_e2e_test.go`) — 12 tests
- README demo instructions
- Dashboard demo (project switcher)

`fixtures/orders-domain/` is a simpler 2-service fixture.

Both have `expected-graph.json` — the golden payload for import. Keep these up to date when changing the graph schema.

**Important:** `fixtures/tom-and-jerry/.depbot/chronicle.db` is tracked in git (the pre-built DB for instant demo). If the schema changes, rebuild it:
```bash
cd fixtures/tom-and-jerry && rm -rf .depbot/chronicle.db
# then scan via Claude or import via CLI
```

## Testing levels

### L1: Unit tests (Go)
Standard `go test ./...`. Tests store CRUD, graph engine, resolve logic, workflow state machine.
No LLM involved.

### L2.1: Agent extraction — targeted files (haiku)
Give Claude specific hard files and verify correct facts + types.
Fixture: `test-l21-expected.json` — 7 files (modules, services, controllers, schemas, consumers, event handlers).
Tests: from_type detection, import/injects/endpoint/http_call/model/enum/produces/consumes extraction.
Pass criteria: 35/35 checks. Model: haiku.

### L2.1.2: Flow tracing — targeted triggers (sonnet)
Give Claude trigger files with enriched graph context (flow_context with reachable nodes + files_to_read).
Fixture: `test-l212-expected.json` — 3 trigger files (arena controller, tom controller, consumer).
Tests: flow node creation, TRIGGERS_FLOW edges, REQUIRES edges.
Pass criteria: 8/8 flows. Model: sonnet (haiku produces shallow/missing flows).

### L2.2.1: Full project scan — phase 1 (haiku x3)
3 parallel haiku agents scan all 41 tom-and-jerry files (phase 1 only — stop at extract_files).
Tests: 0 duplicates, 0 DEPENDS_ON, 4 modules, 4 controllers, 12+ endpoints, correct CONTAINS/INJECTS.

### L2.2.2: Full project scan — phase 2 (sonnet)
Sonnet agents trace flows on trigger files identified by phase 1.
Tests: flow nodes created with trigger-based keys (no duplicates), TRIGGERS_FLOW + REQUIRES edges.
Uses enriched flow_context (files_to_read, reachable graph neighborhood).

### L2.2: Full project scan — combined
L2.2.1 + L2.2.2 end-to-end. Haiku for breadth, sonnet for flows.
Target: 80+ nodes, 100+ edges, 12+ flows, 0 duplicates.

### L3: Graph queries on a built graph
Given a pre-built graph, test impact analysis, dependency queries, path finding.
Tests the graph engine, not extraction.

### L4: Incremental graph updates
New facts arrive that contradict or extend the current graph.
Tests: stale marking, evidence invalidation, re-resolution, contradiction detection.

### L5+: (future)
- Cross-repo federation (chronicle-pro)
- Flow tracing accuracy (phase 2)
- Evidence verification against source code

## Key conventions

- Module path: `github.com/alexdx2/chronicle-core`
- DB file: `.depbot/chronicle.db` (per project)
- Manifest: `.depbot/chronicle.domain.yaml`
- Registry: `.depbot/chronicle.types.yaml` (optional, defaults built in)
- Node key format: `layer:type:domain:qualified_name`
- Edge key format: `from_node_key->to_node_key:EDGE_TYPE`
- live_events is ephemeral: not journaled, not carried by journal rebuild, cleared by ResetDB
- .depbot/graph.jsonl is the committed canonical snapshot; chronicle.db/events/wiki.db are local and gitignored
- Evidence carries a git blob SHA per file; freshness is computed from it, never guessed

## Dashboard dev

The dashboard is a single `admin/static/index.html` (~3900 lines). This is the ONLY source of truth — `internal/admin/static/` was removed. Uses Alpine.js + D3.js.

```bash
# Dev mode serves from disk (no rebuild needed for HTML/JS changes)
chronicle admin --dev

# Or via air which rebuilds on Go changes
air
```

## Chronicle Pro relationship

Pro imports this repo's public packages. When changing `graph/`, `store/`, `registry/`, `validate/`:
- Don't break the public API (method signatures, types)
- Run pro tests too: `cd ../chronicle-pro && go test ./...`
- Pro uses a `replace` directive in go.mod pointing to `../depbot`

## What NOT to commit

See `.gitignore`. Specifically:
- `*.db` files (except fixture DBs)
- `oracle` / `oracle.*` (legacy binary name)
- `CLAUDE.md` (auto-generated per project, not the repo's dev guide)
- `.superpowers/` brainstorm artifacts
- `e2e/results/` test output
- `tmp/` build artifacts
