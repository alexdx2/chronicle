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
| Admin dashboard (`internal/admin/`) | Overview, graph explorer, language, settings, diagrams |
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
- **Dashboard: pro is MVP copy.** Pro copies core's `index.html` and adds federation UI. This will drift. Long-term: core should expose plugin hooks.

## Architecture

```
cmd/chronicle/          CLI entry point
graph/                  Graph engine (public — imported by pro)
store/                  SQLite storage (public)
registry/               Type registry + defaults.yaml (public)
validate/               Key normalization + validation (public)
internal/cli/           CLI commands (internal — not importable by pro)
internal/admin/         Dashboard server + embedded static/ (internal)
internal/mcp/           MCP server + tools (internal)
internal/manifest/      Domain manifest parsing (internal)
```

Public packages (`graph/`, `store/`, `registry/`, `validate/`) are importable by chronicle-pro. Internal packages are only used by this binary. This is enforced by Go's `internal/` convention.

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

## Key conventions

- Module path: `github.com/alexdx2/chronicle-core`
- DB file: `.depbot/chronicle.db` (per project)
- Manifest: `.depbot/chronicle.domain.yaml`
- Registry: `.depbot/chronicle.types.yaml` (optional, defaults built in)
- Node key format: `layer:type:domain:qualified_name`
- Edge key format: `from_node_key->to_node_key:EDGE_TYPE`

## Dashboard dev

The dashboard is a single `internal/admin/static/index.html` (~3700 lines). Uses Alpine.js + D3.js.

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
