#!/bin/bash
# Build chronicle from source and run MCP server.
# Used as Claude Code MCP command so the server is always fresh.
# Preserves the caller's working directory so the MCP server sees the right project.
set -e
PROJECT_DIR="$(pwd)"
cd "$(dirname "$0")/.."
go build -o ./tmp/chronicle ./cmd/chronicle >/dev/null 2>&1
cd "$PROJECT_DIR"
exec "$(dirname "$0")/../tmp/chronicle" "$@"
