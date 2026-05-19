// Command nous is the unified entry point for nous-substrate operations.
// nous#14 M3b ships the cobra root + cluster scaffolding, reusing
// lib/charoncli's constructors for the provider cluster + top-level
// instructions/manifest commands.
//
// Subcommand tree (per nous#14's spec):
//
//	nous                            cobra-default help
//	nous identity ...               (M4 — net-new; placeholder for now)
//	nous brain ...                  (M4 — net-new; placeholder for now)
//	nous provider                   TUI for auth flows + config (charoncli.AuthCmd)
//	nous provider list              machine-readable inspection (charoncli.ManifestCmd)
//	nous service ...                (M3c — install/start/stop unifying brain-sync + proxy)
//	nous instructions [topic]       canonical agent guide (charoncli.InstructionsCmd)
//	nous manifest [topic[:filter]]  machine-readable state (charoncli.ManifestCmd)
//
// Two TUIs only — `nous brain` and `nous provider`. Identity ops use
// interactive CLI prompts. Service is pure CLI. See nous#14 spec for
// the audience-tag scheme and the agent-vs-human help split.
package main

import (
	"os"

	"github.com/spf13/cobra"
	"github.com/xianxu/nous/lib/charoncli"
)

func main() {
	root := &cobra.Command{
		Use:   "nous",
		Short: "Unified CLI + TUI for nous-substrate operations",
		Long: `nous is the substrate tool for AI-coding workflows: identity (GPG keys
+ agent), brains (provision, sync, recipients, resolve), AI providers
(auth + config + proxy), and services (install/start/stop).

Run with no subcommand for this help. See ` + "`nous instructions`" + ` for the
canonical agent guide, or ` + "`nous instructions <topic>`" + ` for cluster-
specific docs.`,
	}

	root.AddCommand(newIdentityCmd())
	root.AddCommand(newBrainCmd())
	root.AddCommand(newSecurityCmd())
	root.AddCommand(providerCmd())
	root.AddCommand(serviceCmdImpl())
	root.AddCommand(newServeCmd())
	root.AddCommand(newStatusCmd())
	root.AddCommand(charoncli.RunCmd())
	root.AddCommand(charoncli.ArmCmd())
	root.AddCommand(charoncli.DisarmCmd())
	root.AddCommand(charoncli.VaultCmd())
	root.AddCommand(instructionsCmd())
	root.AddCommand(manifestCmd())

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}


// providerCmd mounts charon's auth TUI as `nous provider` (the bare cluster
// command IS the TUI entry, not `nous provider auth`). Adds `nous provider
// manifest` as an alias for charon manifest's machine-readable view.
func providerCmd() *cobra.Command {
	auth := charoncli.AuthCmd()
	auth.Use = "provider"
	auth.Short = "AI provider config + auth (interactive TUI) (h)"
	auth.Long = `Interactive TUI for managing AI provider credentials. List configured
providers, drill into auth flows (OAuth dance for gcp / anthropic /
openai / etc., or paste an API key). Add and remove operations also
happen in the TUI — no separate CLI subcommand for those.

Audience tags:
  - provider           (h)  bare cluster command launches the TUI on
                            a TTY; non-TTY callers should use the
                            manifest subcommand for a scriptable view.
  - provider manifest  (a)  JSON: configured providers + granted scopes.

Agent-facing read: ` + "`nous provider manifest`" + `.`
	// Reject unknown positional args. Without this, cobra falls through
	// to RunE (the TUI) when a non-existent subcommand is passed —
	// `nous provider whatever` would silently launch the TUI as if the
	// arg weren't there. NoArgs makes the error explicit:
	// `unknown command "whatever" for "nous provider"`.
	// Subcommands (`manifest`) are resolved before Args validation,
	// so this doesn't block them.
	auth.Args = cobra.NoArgs

	manifest := charoncli.ManifestCmd()
	manifest.Use = "manifest"
	manifest.Short = "Machine-readable: configured providers + granted scopes (JSON)"

	auth.AddCommand(manifest)
	auth.AddCommand(charoncli.GcpCmd())
	auth.AddCommand(charoncli.WhoCmd())
	auth.AddCommand(charoncli.StatsCmd())
	auth.AddCommand(charoncli.ScopesCmd())
	return auth
}

// service cluster impl lives in service.go (serviceCmdImpl). Doctor +
// audit subcommands come in M4.
