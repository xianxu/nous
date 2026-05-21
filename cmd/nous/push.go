// nous push — checkpoint and flush the brain enclosing the current
// directory. The operator-facing complement to the daemon's autosave
// loop: where autosave hides git on the common path (operator just
// saves files), `nous push` is the explicit gesture for "label and
// publish this state now."
//
// Operates directly on the brain's git repo. No IPC with the
// daemon — git's own lock files serialize the rare case where the
// daemon's push debouncer fires at the same moment.

package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/xianxu/nous/lib/brain"
	"github.com/xianxu/nous/lib/brainsync"
)

func newPushCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "push [message]",
		Short: "Checkpoint and push the brain containing the current directory",
		Long: `Push the brain enclosing the current directory. If there are
uncommitted modifications to tracked files, they are committed first
— with the provided message if given, otherwise an autosave message.

Untracked / deleted files are NEVER auto-committed; use git add or
git rm explicitly for those. nous push prints a hint listing them if
present, then proceeds with whatever's already in the index.

Examples:
  nous push                          # flush whatever's pending
  nous push "finished tokyo draft"   # name the checkpoint`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("get cwd: %w", err)
			}
			m, err := brain.EnclosingBrain(cwd)
			if err != nil {
				if errors.Is(err, brain.ErrNotInBrain) {
					return errors.New("not inside a brain — cd into a brain dir first")
				}
				return err
			}
			var userMsg string
			if len(args) == 1 {
				userMsg = strings.TrimSpace(args[0])
			}
			return runPushOnBrain(cmd, m.Path, userMsg)
		},
	}
}

// runPushOnBrain is the actual flush: optional commit of modified-
// tracked files, then PushBrain. Factored from the cobra wrapper so
// tests can exercise the flow without standing up a cobra command.
func runPushOnBrain(cmd *cobra.Command, brainPath, userMsg string) error {
	out := cmd.OutOrStdout()

	if inProgress, marker := brainsync.MergeOrRebaseInProgress(brainPath); inProgress {
		return fmt.Errorf("%s has %s in progress — finish or abort it, then retry",
			brainPath, marker)
	}

	// Surface untracked / deleted before doing anything that mutates
	// the repo, so the operator sees the hint even on a no-op flush.
	if hint := untrackedHint(brainPath); hint != "" {
		fmt.Fprintln(out, hint)
	}

	// Stage modified-tracked files. Excludes untracked, deletions,
	// renames — those need an explicit operator gesture.
	modOut, err := brainsync.RunGit(brainPath, "diff",
		"--name-only", "--diff-filter=M", "--no-renames")
	if err != nil {
		return fmt.Errorf("git diff: %w", err)
	}
	modified := strings.Fields(string(modOut))
	if len(modified) > 0 {
		args := append([]string{"add", "--"}, modified...)
		if _, err := brainsync.RunGit(brainPath, args...); err != nil {
			return fmt.Errorf("git add: %w", err)
		}
	}

	// Is anything staged now (either by us above, or by the operator
	// earlier with an explicit `git add`)?
	staged := false
	if _, err := brainsync.RunGit(brainPath, "diff", "--cached", "--quiet"); err != nil {
		staged = true
	}

	switch {
	case staged:
		msg := userMsg
		if msg == "" {
			msg = fmt.Sprintf("autosave: %s [%d file(s)]",
				time.Now().UTC().Format(time.RFC3339), len(modified))
		}
		if _, err := brainsync.RunGit(brainPath, "commit", "-m", msg); err != nil {
			return fmt.Errorf("git commit: %w", err)
		}
		fmt.Fprintf(out, "committed: %s\n", msg)
	case userMsg != "":
		// v1: don't create empty commits. If the operator wanted a
		// labeled moment but nothing changed, tell them so they can
		// decide whether to add something and retry.
		fmt.Fprintf(out, "note: nothing to commit; message %q not used (no empty commits in v1)\n", userMsg)
	}

	peer := brainsync.PeerIDFor(brainPath)
	pushed, err := brainsync.PushBrain(brainPath, peer, time.Now)
	if err != nil {
		return fmt.Errorf("push: %w", err)
	}
	if pushed {
		fmt.Fprintf(out, "pushed %s\n", brainPath)
	} else {
		fmt.Fprintf(out, "nothing to push for %s (already in sync)\n", brainPath)
	}
	return nil
}

// untrackedHint formats a one-line hint listing files that fall
// outside autosave's purview. Empty string when the working tree
// has none of them.
func untrackedHint(brainPath string) string {
	un, del, err := brainsync.UntrackedAndDeleted(brainPath)
	if err != nil || (len(un) == 0 && len(del) == 0) {
		return ""
	}
	var parts []string
	if len(un) > 0 {
		parts = append(parts, fmt.Sprintf("%d untracked (use `git add` to include): %s",
			len(un), strings.Join(un, ", ")))
	}
	if len(del) > 0 {
		parts = append(parts, fmt.Sprintf("%d deleted (use `git rm` to record): %s",
			len(del), strings.Join(del, ", ")))
	}
	return "hint: " + strings.Join(parts, "; ")
}
