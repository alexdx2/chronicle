#!/bin/bash
# Build chronicle from source and run MCP server.
# Used as Claude Code MCP command so the server is always fresh.
#
# Project dir resolution (in order):
#   1. CHRONICLE_PROJECT_DIR env var (set in MCP config)
#   2. Inherited working directory (from Claude Code)
set -e
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
DEPBOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

cd "$DEPBOT_DIR"
BUILD_HASH=$(git rev-parse --short HEAD 2>/dev/null || echo "unknown")
go build -ldflags "-X github.com/alexdx2/chronicle-core/version.BuildHash=$BUILD_HASH" -o ./tmp/chronicle ./cmd/chronicle >/dev/null 2>&1

# Resolve project directory
PROJECT_DIR="${CHRONICLE_PROJECT_DIR:-$(pwd)}"

cd "$PROJECT_DIR"
exec "$DEPBOT_DIR/tmp/chronicle" "$@"
