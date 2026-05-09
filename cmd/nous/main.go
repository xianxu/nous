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
	"fmt"
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

	root.AddCommand(identityCmd())
	root.AddCommand(brainCmd())
	root.AddCommand(providerCmd())
	root.AddCommand(serviceCmd())
	root.AddCommand(charoncli.InstructionsCmd())
	root.AddCommand(charoncli.ManifestCmd())

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

// identityCmd is the placeholder identity cluster. M4 ships init/export/
// import/list/agent subcommands.
func identityCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "identity",
		Short: "GPG keys + agent + peer pubkeys (placeholder; M4)",
		Long: `Identity cluster — keypair generation, sneakernet export/import with
verify-fingerprint ceremony, gpg-agent lifecycle.

Coming in nous#14 M4 (issue 000014-absorb-charon-unified-nous-cli.md).
For now, use the legacy entry points:
  - make identity              keypair generation
  - gpg --armor --export <fp>  sneakernet export`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("nous identity is not yet implemented (nous#14 M4); see legacy 'make identity' for now")
		},
	}
	return cmd
}

// brainCmd is the placeholder brain cluster. M4 ships new/list/recipient/
// resolve/status. Bare `nous brain` will open the TUI.
func brainCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "brain",
		Short: "Brain provisioning, recipients, sync, resolve (placeholder; M4)",
		Long: `Brain cluster — provision a private/shared brain, manage recipients
(with TTY-only safeguards + verify-fingerprint ceremony), resolve
sync conflicts via /nous-resolve.

Coming in nous#14 M4. For now, use the legacy entry points:
  - make new-brain         provision a brain (single-recipient only)
  - make brain-sync        run the sync daemon
  - /nous-resolve <root>   resolve conflicts via the Claude Code skill`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("nous brain is not yet implemented (nous#14 M4); see legacy 'make new-brain' / 'make brain-sync' for now")
		},
	}
	return cmd
}

// providerCmd mounts charon's auth TUI as `nous provider` (the bare cluster
// command IS the TUI entry, not `nous provider auth`). Adds `nous provider
// list` as an alias for charon manifest's machine-readable view.
func providerCmd() *cobra.Command {
	auth := charoncli.AuthCmd()
	auth.Use = "provider"
	auth.Short = "AI provider config + auth (TUI à la 'charon auth')"
	auth.Long = `Interactive TUI for managing AI provider credentials. List configured
providers, drill into auth flows (OAuth dance for gcp / anthropic /
openai / etc., or paste an API key). Add and remove operations also
happen in the TUI — no separate CLI subcommand for those.

Agent-facing read: ` + "`nous provider list`" + ` (machine-readable JSON of
configured providers + their granted scopes). Equivalent of today's
` + "`charon manifest`" + ` filtered to the provider view.`

	list := charoncli.ManifestCmd()
	list.Use = "list"
	list.Short = "Machine-readable list of configured providers + state (JSON)"

	auth.AddCommand(list)
	return auth
}

// serviceCmd is the service cluster placeholder. M3c ships install/start/
// stop/status that unify brain-sync + proxy plists; M4 adds doctor + audit.
func serviceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "service",
		Short: "Service control: install/start/stop brain-sync + proxy together (placeholder; M3c)",
		Long: `Service cluster — install, start, stop, status across all nous services
(brain-sync + provider proxy as one unit, no per-subsystem control).

Coming in nous#14 M3c. For now, use the legacy entry points:
  - make brain-sync        run the brain-sync watcher
  - charon serve           run the credential proxy
  - charon service ...     existing per-subsystem service control`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("nous service is not yet implemented (nous#14 M3c); see legacy 'charon service' / 'brain-sync service' for now")
		},
	}
	return cmd
}
