#!/bin/bash
# Build and install chronicle globally with git build hash embedded.
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
DEPBOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$DEPBOT_DIR"

BUILD_HASH="$(git rev-parse --short HEAD 2>/dev/null || echo "unknown")"
LDFLAGS="-X github.com/alexdx2/chronicle-core/version.BuildHash=${BUILD_HASH}"

echo "Building chronicle (${BUILD_HASH})..."
go build -ldflags "${LDFLAGS}" -o ./tmp/chronicle ./cmd/chronicle

echo "Installing to $(go env GOPATH)/bin/chronicle ..."
go install -ldflags "${LDFLAGS}" ./cmd/chronicle

# Optional: refresh npm wrapper binary for local @alexdx/chronicle-mcp installs
NPM_BIN="${DEPBOT_DIR}/npm/bin/chronicle"
if [ -d "$(dirname "$NPM_BIN")" ]; then
  cp -f ./tmp/chronicle "$NPM_BIN"
  chmod +x "$NPM_BIN"
  echo "Updated npm wrapper binary at ${NPM_BIN}"
fi

echo ""
./tmp/chronicle version
echo ""
"${SCRIPT_DIR}/verify-mcp.sh"
