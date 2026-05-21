// nous brain leave — collaborator-side leave gesture. Removes self
// from the brain's manifest (pushes, gcrypt re-encrypts to remaining
// collaborators minus me), then revokes my GitHub collaborator
// status. Optional --delete-local to nuke the local clone.
//
// Refuses for the GitHub repo owner (orphans the brain) and refuses
// when I'd be the last recipient (would lock everyone out).

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/xianxu/nous/lib/brain"
	"github.com/xianxu/nous/lib/brainsync"
)

func newBrainLeaveCmd() *cobra.Command {
	var brainPath string
	var deleteLocal bool
	var assumeYes bool

	cmd := &cobra.Command{
		Use:   "leave",
		Short: "Leave a shared brain — removes self from collaborators + revokes GitHub access",
		Long: `Leave the brain enclosing the current directory (or --brain PATH).

Three things happen, in this exact order:

  1. Your fingerprint is removed from .brain/config.md and the change
     is committed + pushed. gcrypt re-encrypts the brain to the
     remaining collaborator set, minus you. After this lands, others
     can verify you're gone.
  2. Your GitHub collaborator status on the repo is revoked
     (DELETE /repos/{owner}/{repo}/collaborators/<you>). Without this
     the repo would still appear in your accessible-but-not-cloned
     list even though you can no longer decrypt commits.
  3. With --delete-local: the local clone directory is removed.
     Without the flag, the directory stays on disk so you can salvage
     anything you had locally before deleting manually.

Refuses to act when:
  - you're the GitHub owner of the repo (use ownership-transfer or
    delete the brain instead — both out of scope for this command),
  - you're not actually a collaborator on this brain,
  - you'd be the last collaborator (would orphan the brain — admit
    someone else first, or delete the brain).`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			path, err := resolveLeaveTarget(brainPath)
			if err != nil {
				return err
			}
			confirm := func() (bool, error) {
				if assumeYes {
					return true, nil
				}
				return promptYesNoFor(cmd, path)
			}
			return runBrainLeave(ctx, cmd.OutOrStdout(), path, deleteLocal, confirm)
		},
	}
	cmd.Flags().StringVar(&brainPath, "brain", "", "path to brain (default: enclosing brain of cwd)")
	cmd.Flags().BoolVar(&deleteLocal, "delete-local", false, "rm -rf the brain directory after pushing the manifest update")
	cmd.Flags().BoolVarP(&assumeYes, "yes", "y", false, "skip the interactive confirmation prompt")
	return cmd
}

// resolveLeaveTarget returns the absolute path of the brain to leave.
// Either --brain PATH (validated as a brain) or the brain enclosing
// cwd. Returns a clear error when neither resolves.
func resolveLeaveTarget(brainPathFlag string) (string, error) {
	if brainPathFlag != "" {
		m, err := brain.Read(brainPathFlag)
		if err != nil {
			return "", fmt.Errorf("--brain %s: %w", brainPathFlag, err)
		}
		return m.Path, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get cwd: %w", err)
	}
	m, err := brain.EnclosingBrain(cwd)
	if err != nil {
		if errors.Is(err, brain.ErrNotInBrain) {
			return "", errors.New("not inside a brain — cd into one or pass --brain PATH")
		}
		return "", err
	}
	return m.Path, nil
}

// promptYesNoFor prints the leave summary + reads y/n from stdin.
func promptYesNoFor(cmd *cobra.Command, brainPath string) (bool, error) {
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Leave brain at %s?\n", brainPath)
	fmt.Fprintln(out, "  - your fingerprint is removed from the manifest")
	fmt.Fprintln(out, "  - the change is committed + pushed (gcrypt re-encrypts)")
	fmt.Fprintln(out, "  - your GitHub collaborator status is revoked")
	fmt.Fprint(out, "[y/N]: ")
	var resp string
	_, _ = fmt.Fscanln(cmd.InOrStdin(), &resp)
	resp = strings.ToLower(strings.TrimSpace(resp))
	return resp == "y" || resp == "yes", nil
}

// runBrainLeave wraps brainsync.LeaveBrain with the CLI's confirm
// prompt + human-readable output. Brain resolution + refuse-checks
// happen inside brainsync.LeaveBrain; we only own the confirmation
// gating and how to render the result.
func runBrainLeave(ctx context.Context, out io.Writer, brainPath string, deleteLocal bool, confirm func() (bool, error)) error {
	ok, err := confirm()
	if err != nil {
		return err
	}
	if !ok {
		fmt.Fprintln(out, "cancelled.")
		return nil
	}
	res, err := brainsync.LeaveBrain(brainPath, deleteLocal)
	if err != nil {
		return err
	}
	fmt.Fprintln(out, "✓ removed self from manifest and pushed")
	if res.CollaboratorRevoked {
		fmt.Fprintf(out, "✓ revoked GitHub collaborator status on %s/%s\n", res.Owner, res.Repo)
	} else {
		fmt.Fprintf(out, "! GitHub collaborator removal failed: %v\n", res.CollaboratorRevokeErr)
		fmt.Fprintln(out, "  Manifest update did land. Retry the gh revoke later:")
		fmt.Fprintf(out, "    gh api -X DELETE repos/%s/%s/collaborators/%s\n", res.Owner, res.Repo, res.MyLogin)
	}
	if res.LocalDeleted {
		fmt.Fprintf(out, "✓ deleted local clone at %s\n", brainPath)
	} else {
		fmt.Fprintf(out, "\nLocal clone retained at %s — rm -rf manually when ready.\n", brainPath)
	}
	_ = ctx
	return nil
}
