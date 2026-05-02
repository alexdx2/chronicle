# Chronicle MCP — Knowledge Graph

This project uses Chronicle for code analysis. Chronicle MCP tools are available.

## Commands

- /chronicle-scan — Full project scan (data models → code → endpoints → cross-service)
- /chronicle-update — Incremental update (changed files only, via git diff)
- /chronicle-data — Analyze data models (Prisma, TypeORM, entities)
- /chronicle-language — Define and check domain language (ubiquitous language)
- /chronicle-impact — "What breaks if I change X?" (blast radius analysis)
- /chronicle-deps — "What does X depend on?" (forward + reverse dependencies)
- /chronicle-path — "How does A connect to B?" (shortest path)
- /chronicle-flows — Business use case analysis (end-to-end processes)
- /chronicle-services — Service architecture overview (cross-service deps)
- /chronicle-diagram — Live architecture diagram in the browser
- /chronicle-status — Current graph state + dashboard URL
- /chronicle-help — Show all commands

## How it works

When you see a /chronicle-* command, call chronicle_command(command='<name>') and follow the returned instructions.
The admin dashboard runs automatically at the URL shown by /chronicle-status.
