// Package wiring implements machine-level agent setup (install state,
// journaled applies, adapters) and project attachment. Everything here is
// pure logic; cobra commands in internal/cli stay thin.
package wiring

import (
	"os"
	"path/filepath"
)

// Home is the chronicle machine directory: $CHRONICLE_HOME override (tests,
// sandboxes) or ~/.chronicle.
func Home() string {
	if h := os.Getenv("CHRONICLE_HOME"); h != "" {
		return h
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".chronicle"
	}
	return filepath.Join(home, ".chronicle")
}

func StatePath() string { return filepath.Join(Home(), "install-state.json") }
