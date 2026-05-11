package main

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/xianxu/nous/lib/charoncli"
)

// instructionsCmd implements progressive disclosure on `nous instructions`:
//
//	nous instructions               nous-overall scope (clusters + entry points)
//	nous instructions proxy         credential proxy + provider auth (charon-origin)
//	nous instructions brain         brain provisioning, sync, recipients (M4)
//	nous instructions identity      keys + agent + peers (M4)
//	nous instructions service       service lifecycle (today: install/start/stop)
//
// Topics that haven't been written yet emit a placeholder explaining what
// they'll cover and pointing at the issue tracking it. Cobra's per-
// subcommand --help follows the same shape, so an agent walking the
// help tree never gets a kitchen-sink dump.
func instructionsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "instructions [topic]",
		Short: "nous agent guide (progressive disclosure)",
		Long: `Progressive-disclosure agent guide for nous-substrate operations.

Without a topic, prints a short overview listing the cluster topics
and where to drill in. With a topic, prints the cluster-specific
guide. Agents fetch only the topic they need.

Topics:
  proxy      credential proxy + provider auth (origin: charon)
  brain      brain provisioning, sync, recipients [M4 — placeholder]
  identity   GPG keys + gpg-agent lifecycle + peers [M4 — placeholder]
  service    service install/start/stop unified across subsystems`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return printOverviewInstructions(cmd)
			}
			switch args[0] {
			case "proxy":
				// Delegate to charoncli's existing instructions cmd —
				// preserves charon's canonical agent guide as the
				// proxy-topic content. Future M4+ may rewrite to
				// reframe charon-the-binary references as nous-the-
				// binary, but the procedure itself doesn't change.
				return charoncli.InstructionsCmd().RunE(cmd, nil)
			case "brain":
				return printBrainInstructionsPlaceholder(cmd)
			case "identity":
				return printIdentityInstructionsPlaceholder(cmd)
			case "service":
				return printServiceInstructions(cmd)
			default:
				return fmt.Errorf("unknown topic %q. Available: proxy, brain, identity, service", args[0])
			}
		},
	}
	return cmd
}

func printOverviewInstructions(cmd *cobra.Command) error {
	const overview = `# nous — Agent Instructions (overview)

nous is the substrate tool for AI-coding workflows. Four use-case clusters:

- ` + "`identity`" + ` — GPG keys, gpg-agent lifecycle, peer pubkeys
- ` + "`brain`" + `    — provision, sync, recipients, conflict resolve
- ` + "`provider`" + ` — AI provider config + OAuth auth (the credential proxy)
- ` + "`service`" + `  — install/start/stop the brain-sync watcher + proxy together

Plus two top-level agent-facing entry points:

- ` + "`nous instructions [topic]`" + ` — cluster-specific guide (this is the entry).
  Topics: ` + "`proxy`" + `, ` + "`brain`" + `, ` + "`identity`" + `, ` + "`service`" + `.
- ` + "`nous manifest [topic[:filter]]`" + ` — machine-readable state introspection.
  Topics: ` + "`proxy`" + ` (configured providers + scopes); narrower filters TBD.

To drive a cluster:

- ` + "`nous <cluster> --help`" + ` lists subcommands.
- ` + "`nous <cluster> <subcmd> --help`" + ` documents the procedure for that op.
- For interactive flows (` + "`nous provider`" + `, future ` + "`nous brain`" + `), bare cluster
  command opens a TUI. Humans use TUIs; agents use subcommands.

Human-vs-agent surface split:

- Subcommands' --help text is the agent manual (skill-as-script
  pattern; see brain/data/life/42shots/ideas/2026-05-07-01-pensive-
  skill-as-script.md). Dense, scriptable, scriptable.
- TUIs (bubbletea-rendered) are for humans. They never appear in
  agent transcripts when the agent only calls the CLI subcommands.

Today (post-` + "`nous#14`" + ` M3): proxy and service clusters are functional;
brain and identity are placeholders pending M4. See
` + "`nous instructions proxy`" + ` for the proxy + auth deep-dive (charon-
origin content).
`
	fmt.Fprint(cmd.OutOrStdout(), overview)
	return nil
}

func printBrainInstructionsPlaceholder(cmd *cobra.Command) error {
	const txt = `# nous brain — Agent Instructions

The brain cluster covers: provision a private/shared gcrypt brain
(` + "`nous brain new`" + `), manage recipients with TTY-only safeguards +
verify-fingerprint ceremony (` + "`nous brain recipient add/remove`" + `),
inspect state (` + "`nous brain list`" + `, ` + "`nous brain status`" + `), resolve
brain-sync conflicts mechanically (` + "`nous brain resolve`" + ` —
called by the /nous-resolve Claude Code skill).

** Coming in nous#14 M4. **

For now, use the legacy entry points:
  - make new-brain         provision a single-recipient brain
  - make brain-sync        run the sync daemon (or use ` + "`nous service`" + `)
  - /nous-resolve <root>   the Claude Code skill (lib already wires it
                           via the resolve mechanical ops in lib/brainsync)

The substrate that this cluster will sit on is already in lib/brainsync,
lib/agent, and lib/charoncli. M4 wires it up under ` + "`nous brain`" + `.
`
	fmt.Fprint(cmd.OutOrStdout(), txt)
	return nil
}

func printIdentityInstructionsPlaceholder(cmd *cobra.Command) error {
	const txt = `# nous identity — Agent Instructions

The identity cluster covers GPG identity (init/export/import with
verify-fingerprint ceremony) and gpg-agent lifecycle (prewarm at
session start, flush at end, status query).

** Coming in nous#14 M4. **

Foundation already shipped in M3d (lib/agent.DiscoverIdentity returns
fingerprint + UID + all keygrips for a key). M4 adds the verbs:
prewarm presents the keychain-stored passphrase to gpg-agent via
PRESET_PASSPHRASE; flush clears the cache; status queries KEYINFO.

For now, use the legacy entry points:
  - make identity                    keypair generation
  - gpg --armor --export <fp>        sneakernet export
  - gpg --import <pubkey-file>       sneakernet import (no verify ceremony)

Security posture (per nous#14): identity init/import and brain
recipient add are TTY-only when M4 lands — refuse to run from
non-interactive (CI / Claude Code subprocess). Verify-fingerprint
ceremony (type last 8 hex chars) is unforgeable from agent stdin.
Together: agents on the device cannot silently expand their access.
`
	fmt.Fprint(cmd.OutOrStdout(), txt)
	return nil
}

func printServiceInstructions(cmd *cobra.Command) error {
	const txt = `# nous service — Agent Instructions

The service cluster manages all nous services together as a unit.
brain-sync (the brain sync watcher) and the credential proxy
install/start/stop with one command — there's no value in starting
one without the other.

Subcommands:

  nous service install     Write both launchd plists, bootstrap them.
                           Idempotent — safe to re-run after binary
                           rebuilds.
  nous service uninstall   Remove the com.42shots.nous plist.
  nous service start       Start (or restart) the daemon via launchctl.
                           If the proxy can't bind 127.0.0.1:8230,
                           launchd's KeepAlive will retry; check
                           ~/Library/Logs/nous.log for the cause.
  nous service stop        Stop the daemon.
  nous service status      Show installed-and-running state.

Single-binary daemon: ` + "`nous serve`" + ` runs both the credential proxy
and brain-sync watcher as goroutines under one process. The plist
invokes ` + "`nous serve`" + ` directly — no sibling binaries needed.

Common gotcha: a manual ` + "`nous serve`" + ` left running outside launchd
will hold port 8230 and prevent the launchd-installed copy from
binding. Diagnose with ` + "`lsof -i :8230`" + ` and ` + "`pkill -f 'nous serve'`" + `
before ` + "`nous service start`" + `.

Now (M4 shipped): ` + "`nous service doctor`" + ` (prescriptive checks
with named fixes) and ` + "`nous service audit`" + ` (unified log query
over ~/Library/Logs/nous.log).
`
	fmt.Fprint(cmd.OutOrStdout(), txt)
	return nil
}

// manifestCmd implements progressive disclosure on `nous manifest`:
//
//	nous manifest             nous-summary (version, entry points, what's running)
//	nous manifest proxy       provider state (== charoncli.ManifestCmd output)
//	nous manifest all         kitchen-sink dump (proxy + future brain + service)
//
// `proxy:permission` etc. filter narrows are still TBD — keeping the
// surface minimal until M4 adds brain + identity manifest content.
func manifestCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "manifest [topic]",
		Short: "nous machine-readable state (progressive disclosure)",
		Long: `Progressive-disclosure machine-readable state dump for agents.

Without a topic, prints a short summary. With a topic, prints the
cluster-specific state. ` + "`all`" + ` is explicit kitchen-sink.

Topics:
  proxy   configured AI providers + their granted scopes (JSON)
  all     full nous state — proxy + (future) brain + service`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			topic := ""
			if len(args) > 0 {
				topic = args[0]
			}
			switch topic {
			case "", "summary":
				return printManifestSummary(cmd)
			case "proxy", "all":
				// "all" today == proxy (only manifest content we have).
				// M4 expands to also include brain + service state.
				return charoncli.ManifestCmd().RunE(cmd, nil)
			default:
				return fmt.Errorf("unknown manifest topic %q. Available: proxy, all", topic)
			}
		},
	}
	return cmd
}

func printManifestSummary(cmd *cobra.Command) error {
	const summary = `{
  "binary": "nous",
  "topics": {
    "proxy": "nous manifest proxy — provider config + granted scopes (JSON)",
    "all":   "nous manifest all — full nous state (today == proxy; M4 adds brain + service)"
  },
  "instructions_entry": "nous instructions [topic]",
  "version": "nous#14 M3 — credential proxy + provider auth functional; brain + identity placeholders pending M4"
}
`
	fmt.Fprint(cmd.OutOrStdout(), summary)
	return nil
}
