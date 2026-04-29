package main

import (
	"os"

	"github.com/alexdx2/chronicle-core/internal/cli"
)

func main() {
	if err := cli.NewRootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}
