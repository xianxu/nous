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
	"github.com/spf13/cobra"
)

func newBrainCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "brain",
		Short: "Brain provisioning, recipients, sync, resolve",
		Long: `Brain cluster — provision a brain (private or shared), inspect
state, manage recipients (with TTY-only safeguards + verify-fingerprint
ceremony for admission), and resolve sync conflicts.

Subcommands:
  new                      provision a brain (single or multi-recipient)
  list                     show brains under the workspace root
  recipient list           list recipients on a brain
  recipient add            admit a recipient (TTY-only; verify-fingerprint)
  recipient remove         revoke a recipient (TTY-only; with safeguards)
  resolve                  mechanical conflict-find for /nous-resolve`,
		Args: cobra.NoArgs,
	}
	cmd.AddCommand(
		newBrainNewCmd(),
		newBrainListCmd(),
		newBrainRecipientCmd(),
		newBrainResolveCmd(),
	)
	return cmd
}
