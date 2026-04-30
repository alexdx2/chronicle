# Chronicle MCP — Developer Guide

## Project overview

Chronicle MCP is a persistent knowledge graph for Claude Code. Go binary, SQLite storage, embedded admin dashboard. Published to npm as `@alexdx/chronicle-mcp`.

Two repos:
- **chronicle-core** (this repo) — single-repo graph engine, CLI, MCP server, dashboard
- **chronicle-pro** (`../chronicle-pro`) — multi-repo federation, extends core

## Architecture

```
cmd/chronicle/          CLI entry point
graph/                  Graph engine (public — imported by pro)
store/                  SQLite storage (public)
registry/               Type registry + defaults.yaml (public)
validate/               Key normalization + validation (public)
internal/cli/           CLI commands (internal)
internal/admin/         Dashboard server + embedded static/ (internal)
internal/mcp/           MCP server + tools (internal)
internal/manifest/      Domain manifest parsing (internal)
```

Public packages (`graph/`, `store/`, `registry/`, `validate/`) are importable by chronicle-pro. Internal packages are only used by this binary.

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
