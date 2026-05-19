package main

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/spf13/cobra"

	"github.com/xianxu/nous/lib/brain"
)

// newBrainCloneCmd builds `nous brain clone <gcrypt-url> [target-dir]`:
// the convenience wrapper around `git clone gcrypt::...` that handles
// the pubkey bootstrap step before the actual clone.
//
// Without this wrapper, a peer would hit `gpg: Can't check signature:
// No public key` mid-clone, because gcrypt signs every manifest with
// the producer's GPG key and the consumer needs all recipients'
// pubkeys to verify before decrypting. The fix is a 4-step dance:
//
//	git clone --branch keys --single-branch <plain-url> /tmp/keys
//	gpg --import /tmp/keys/*.asc
//	rm -rf /tmp/keys
//	git clone <gcrypt-url> [target-dir]
//
// This subcommand bundles that into one verb. The bootstrap is
// idempotent + graceful: brains provisioned before #23 don't have a
// `keys` branch, in which case the bootstrap step no-ops and the
// operator falls back to sneakernet pubkey hand-off as before.
func newBrainCloneCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "clone GCRYPT-URL [TARGET-DIR]",
		Short: "Clone a shared brain (auto-imports peer pubkeys first)",
		Long: `Clones a gcrypt-encrypted brain, first bootstrapping all peer
pubkeys from the brain's keys branch so gcrypt's signature
verification works without manual sneakernet hand-off.

For brains provisioned with nous#23 or later, every peer's pubkey
(operator + admitted recipients) lives on the gcrypt repo's
` + "`keys`" + ` branch as plaintext. This command fetches the keys
branch first, imports every pubkey into the local GPG keyring,
then runs the actual gcrypt clone. The peer doesn't need to receive
any pubkey out-of-band beyond an initial fingerprint exchange to
verify the keys-branch contents weren't tampered with (use
` + "`nous brain recipient verify`" + ` for the opt-in ceremony).

For brains older than nous#23 (no keys branch), the bootstrap is a
silent no-op and the operator falls back to the pre-#23 manual
sneakernet workflow:

  nous identity import <peer-pubkey-file>
  git clone <gcrypt-url>

Args:
  GCRYPT-URL    The same URL ` + "`git clone`" + ` would accept —
                e.g. ` + "`gcrypt::ssh://git@github.com/owner/brain.git`" + `.
                The ` + "`gcrypt::`" + ` prefix is stripped for the
                keys-branch fetch and re-applied for the brain clone.
  TARGET-DIR    Optional target directory (default: derived from
                URL, matching ` + "`git clone`" + `'s default).`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			gcryptURL := args[0]
			var targetDir string
			if len(args) >= 2 {
				targetDir = args[1]
			}
			out := cmd.OutOrStdout()
			ctx := cmd.Context()

			// 1. Bootstrap pubkeys via peerkeys (handles temp clone,
			// per-file import, cleanup). Graceful when the keys
			// branch is absent.
			fmt.Fprintln(out, "Fetching peer pubkeys from keys branch …")
			imported, errs, err := brain.BootstrapPubkeys(ctx, gcryptURL)
			if err != nil {
				fmt.Fprintf(out, "  warning: %v\n", err)
				fmt.Fprintln(out, "  (continuing with gcrypt clone; you may need to gpg --import the operator's pubkey manually if verification fails)")
			} else if imported == 0 && len(errs) == 0 {
				fmt.Fprintln(out, "  (no keys branch found — brain may predate nous#23; fall back to sneakernet pubkey hand-off if the clone errors with 'No public key')")
			} else {
				fmt.Fprintf(out, "  imported %d peer pubkey(s)\n", imported)
				for _, e := range errs {
					fmt.Fprintf(out, "  warning: %v\n", e)
				}
			}

			// 2. The actual gcrypt clone. Stream stdout/stderr so
			// the operator sees gcrypt's progress live (manifest
			// decrypt, object fetch, etc.).
			fmt.Fprintln(out)
			fmt.Fprintln(out, "Cloning brain …")
			cargs := []string{"clone", gcryptURL}
			if targetDir != "" {
				cargs = append(cargs, targetDir)
			}
			c := exec.CommandContext(ctx, "git", cargs...)
			c.Stdout = os.Stdout
			c.Stderr = os.Stderr
			if err := c.Run(); err != nil {
				return fmt.Errorf("git clone: %w", err)
			}
			// No explicit gcrypt-participants sync needed here: the
			// brainsync push wrapper syncs from the manifest before
			// every push (nous#24). The local config is stale until
			// the first push, but nothing reads it before then.
			return nil
		},
	}
}
