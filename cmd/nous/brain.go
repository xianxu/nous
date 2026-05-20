// nous brain cluster: provision (`new`), inspect (`list`), manage
// recipients (`recipient list/add/remove`), and dispatch the mechanical
// pieces of conflict resolution (`resolve`).
//
// Audience tags:
//   - brain new                  (h)  TTY-only when admitting non-self
//                                     recipients during creation.
//   - brain list                 (a)  read-only; scriptable.
//   - brain recipient list       (a)  read-only.
//   - brain recipient add        (h)  TTY-only; verify-fingerprint ceremony.
//   - brain recipient remove     (h)  TTY-only; safeguards (last-recipient,
//                                     self-removal, revocation warning).
//   - brain resolve              (a)  mechanical; called by /nous-resolve.
package main

import (
	"os"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/xianxu/nous/lib/brainsync"
)

func newBrainCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "brain",
		Short: "Brain provisioning, recipients, sync, resolve",
		Long: `Brain cluster — provision a brain (private or shared), inspect
state, manage recipients (with TTY-only safeguards + verify-fingerprint
ceremony for admission), and resolve sync conflicts.

Bare ` + "`nous brain`" + ` (TTY) launches the interactive brain TUI:
list of brains → drill-in (recipients, sync state, conflicts) →
actions. Use the subcommands below for scriptable / agent-driven use.

Subcommands:
  new                      provision a brain (single or multi-recipient)
  clone                    clone a shared brain (auto-imports peer pubkeys)
  list                     show brains under the workspace root
  recipient list           list recipients on a brain
  recipient add            admit a recipient (TTY-only; verify-fingerprint)
  recipient remove         revoke a recipient (TTY-only; with safeguards)
  resolve                  mechanical conflict-find for /nous-resolve`,
		Args: cobra.NoArgs,
		// RunE only fires when no subcommand was selected. TTY → launch
		// the TUI; non-TTY (agent, pipe) → fall through to help so the
		// agent's transcript doesn't fill with bubbletea escape codes.
		RunE: func(cmd *cobra.Command, args []string) error {
			if term.IsTerminal(int(os.Stdout.Fd())) {
				return runBrainTUI()
			}
			return cmd.Help()
		},
		// User contract: "if you can see it in `nous brain`, the daemon
		// watches it." Every subcommand under nous-brain (including this
		// command itself when it falls through to help / TUI) touches the
		// rescan-signal file on completion, waking any running `nous
		// serve` to re-discover. Best-effort; touch failures are logged
		// but don't fail the user's command.
		PersistentPostRunE: func(cmd *cobra.Command, args []string) error {
			if err := brainsync.TouchRescanSignal(); err != nil {
				cmd.PrintErrf("brainsync: rescan-signal touch failed: %v\n", err)
			}
			return nil
		},
	}
	cmd.AddCommand(
		newBrainNewCmd(),
		newBrainCloneCmd(),
		newBrainInviteCmd(),
		newBrainListCmd(),
		newBrainRecipientCmd(),
		newBrainResolveCmd(),
	)
	return cmd
}
