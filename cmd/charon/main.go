// Command charon is the legacy entry point for the credential proxy + provider auth
// subcommand surface. As of nous#14 M3, the cobra subcommands live in lib/charoncli/;
// this file is a thin shim that builds the root command and runs it.
//
// The cmd/nous binary (also nous#14 M3) imports the same lib/charoncli constructors
// and mounts them at cluster paths (`nous provider auth`, `nous instructions`, etc.).
// Both binaries share one source of truth for subcommand behavior.
package main

import (
	"os"

	"github.com/xianxu/nous/lib/charoncli"
)

func main() {
	if err := charoncli.BuildRoot().Execute(); err != nil {
		os.Exit(1)
	}
}
