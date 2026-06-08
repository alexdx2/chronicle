#!/bin/bash
# Verify the built chronicle binary exposes MCP identity tools and release markers.
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
DEPBOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
BIN="${DEPBOT_DIR}/tmp/chronicle"

if [ ! -x "$BIN" ]; then
  BIN="$(command -v chronicle || true)"
fi
if [ -z "$BIN" ] || [ ! -x "$BIN" ]; then
  echo "ERROR: chronicle binary not found — run scripts/install.sh first" >&2
  exit 1
fi

fail=0
check_string() {
  local needle="$1"
  local label="$2"
  if grep -aq "$needle" "$BIN"; then
    echo "OK  $label"
  else
    echo "FAIL $label (missing: $needle)" >&2
    fail=1
  fi
}

echo "Verifying MCP identity in: $BIN"
check_string "chronicle_mcp_identity" "MCP tool chronicle_mcp_identity"
check_string "kestrel-ap3" "release codename kestrel-ap3"
check_string "mcp_identity_v1" "capability mcp_identity_v1"
check_string "graph_hygiene" "capability graph_hygiene"

echo ""
echo "CLI identity:"
"$BIN" version

if [ "$fail" -ne 0 ]; then
  echo ""
  echo "Binary is missing expected MCP identity markers. Rebuild with scripts/install.sh" >&2
  exit 1
fi

echo ""
echo "MCP identity verification passed."
