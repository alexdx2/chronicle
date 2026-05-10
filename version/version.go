// Package version is the single source of truth for the Chronicle version.
// Used by: CLI, MCP server, npm package (synced by CI), debug logger, git tags.
package version

const Version = "0.7.0"

// BuildHash is set at compile time via -ldflags.
// go build -ldflags "-X github.com/alexdx2/chronicle-core/version.BuildHash=$(git rev-parse --short HEAD)"
var BuildHash = "dev"
