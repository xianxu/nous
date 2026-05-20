package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/xianxu/nous/lib/brain"
	"github.com/xianxu/nous/lib/gh"
)

// readBrainOriginURL returns the brain's remote.origin.url, or empty
// string when unset. Duplicates lib/brain/status.go's readOriginURL
// (unexported) — small enough that a cross-package export isn't worth
// the surface widening.
func readBrainOriginURL(brainRoot string) string {
	out, err := exec.Command("git", "-C", brainRoot, "config", "--get", "remote.origin.url").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// newBrainInviteCmd builds `nous brain invite <gh-login> [--brain
// <path>]`: sends a GitHub collaborator invitation to <gh-login> for
// the picked brain's underlying repo. Trust anchor for nous#26's
// recipient-onboarding flow: the act of inviting IS the operator's
// admission decision; auto-admit in brainsync handles the
// gcrypt-participants update once the invitee publishes their pubkey
// to the keys branch via `nous brain join`.
//
// Audience: (h) human operator, interactive prompt for brain selection
// when there are multiple. With `--brain <path>` and `--yes`, it's
// scriptable but still requires the operator to know the path.
func newBrainInviteCmd() *cobra.Command {
	var brainPath string
	var force bool
	var yes bool

	cmd := &cobra.Command{
		Use:   "invite GITHUB-LOGIN",
		Short: "Invite a GitHub user as a recipient (collaborator + auto-admit)",
		Long: `Send a GitHub collaborator invitation to GITHUB-LOGIN for the picked
brain. This is the operator's act of admission — once the invitee
accepts (via 'nous brain join' or the web UI) and publishes their
pubkey to the keys branch, brain-sync's auto-admit will append them
to the manifest's recipients and re-encrypt on the next push.

The operator does NOT need to know the invitee's GPG fingerprint up
front. Fingerprint verification is an opt-in tamper-check (via
'nous brain recipient verify'), not a precondition. Same trust model
as WhatsApp: phone-number-add is the admission; safety-number
verification is the suspicion-mode escape.

Flags:
  --brain PATH   Skip the brain picker and target this brain directly.
  --force        Skip the public 'gh api users/<login>' existence check.
                 Useful for brand-new accounts whose /users endpoint
                 hasn't propagated yet (see nous#25).
  --yes          Skip the final "send invitation? [y/N]" confirmation.
                 Operator already committed by typing the command.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBrainInvite(cmd.OutOrStdout(), cmd.InOrStdin(), args[0], brainPath, force, yes)
		},
	}
	cmd.Flags().StringVar(&brainPath, "brain", "", "target brain path (skips picker)")
	cmd.Flags().BoolVar(&force, "force", false, "skip gh user existence check (for fresh-account-lag cases)")
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the final confirmation prompt")
	return cmd
}

func runBrainInvite(w io.Writer, in io.Reader, ghLogin, brainPathFlag string, force, yes bool) error {
	// Strip a leading @ if the operator pasted "@yingtest42" out of muscle
	// memory — github CLI accepts the bare login.
	ghLogin = strings.TrimPrefix(strings.TrimSpace(ghLogin), "@")
	if ghLogin == "" {
		return errors.New("github login is empty")
	}

	// 1. Validate GitHub user exists (unless --force).
	if !force {
		if err := gh.UserExists(ghLogin); err != nil {
			if errors.Is(err, gh.ErrUserNotVisible) {
				return fmt.Errorf("%w\n  (this can happen for brand-new accounts; pass --force to skip the check)", err)
			}
			return err
		}
	}

	// 2. Pick the target brain.
	target, err := resolveInviteTargetBrain(w, in, brainPathFlag)
	if err != nil {
		return err
	}

	// 3. Resolve owner/repo from the brain's git remote.
	remoteURL := readBrainOriginURL(target.Path)
	if remoteURL == "" {
		return fmt.Errorf("brain %q has no remote.origin.url configured — can't invite without a hosted repo", target.Path)
	}
	owner, repo, err := brain.GitHubOwnerRepo(remoteURL)
	if err != nil {
		return fmt.Errorf("parse remote %q: %w", remoteURL, err)
	}

	// 4. Confirm.
	fmt.Fprintf(w, "About to send a GitHub collaborator invitation:\n")
	fmt.Fprintf(w, "  brain:  %s\n", filepath.Base(target.Path))
	fmt.Fprintf(w, "  repo:   %s/%s\n", owner, repo)
	fmt.Fprintf(w, "  invitee: %s\n", ghLogin)
	fmt.Fprintln(w)
	if !yes {
		if !term.IsTerminal(int(os.Stdin.Fd())) {
			return errors.New("stdin is not a TTY and --yes wasn't passed; refusing to invite without confirmation")
		}
		if !confirmYN(w, in, "Send invitation?") {
			return errors.New("aborted by operator")
		}
	}

	// 5. Send.
	if err := gh.AddCollaborator(owner, repo, ghLogin, "push"); err != nil {
		return fmt.Errorf("add collaborator: %w", err)
	}

	fmt.Fprintf(w, "✓ Invitation sent.\n")
	fmt.Fprintf(w, "  %s can accept via 'nous brain join' or:\n", ghLogin)
	fmt.Fprintf(w, "  https://github.com/%s/%s/invitations\n", owner, repo)
	fmt.Fprintln(w)
	fmt.Fprintf(w, "Once they accept and run 'nous brain join', brain-sync\n")
	fmt.Fprintf(w, "will auto-admit them on the next pull cycle.\n")
	return nil
}

// resolveInviteTargetBrain returns the brain to invite into, either:
//   - the brain at --brain PATH (if non-empty), validated as a brain
//   - the only brain in the workspace (no prompt)
//   - the brain picked by the operator from a numbered list
func resolveInviteTargetBrain(w io.Writer, in io.Reader, brainPathFlag string) (brain.Manifest, error) {
	if brainPathFlag != "" {
		m, err := brain.Read(brainPathFlag)
		if err != nil {
			return m, fmt.Errorf("read brain %q: %w", brainPathFlag, err)
		}
		return m, nil
	}

	brains, err := brain.DiscoverAll()
	if err != nil {
		return brain.Manifest{}, err
	}
	if len(brains) == 0 {
		return brain.Manifest{}, errors.New("no brains under workspace root — run `nous brain new` first")
	}
	if len(brains) == 1 {
		return brains[0], nil
	}

	// Multi-brain: numbered picker. Sorted by basename for stability.
	sort.Slice(brains, func(i, j int) bool {
		return filepath.Base(brains[i].Path) < filepath.Base(brains[j].Path)
	})
	fmt.Fprintln(w, "Pick a brain to invite into:")
	for i, b := range brains {
		kind := "private"
		if b.Shared() {
			kind = "shared"
		}
		fmt.Fprintf(w, "  [%d] %-22s  (%s, %d recipient%s)\n",
			i+1, filepath.Base(b.Path), kind, len(b.Recipients), pluralS(len(b.Recipients)))
	}
	fmt.Fprintln(w)
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return brain.Manifest{}, errors.New("multiple brains and stdin is not a TTY; pass --brain PATH to disambiguate")
	}
	idx, err := promptIndex(w, in, "Select [1-"+strconv.Itoa(len(brains))+"]: ", len(brains))
	if err != nil {
		return brain.Manifest{}, err
	}
	return brains[idx], nil
}

// confirmYN prompts the operator for [y/N]. Returns true on yes only.
func confirmYN(w io.Writer, in io.Reader, prompt string) bool {
	fmt.Fprintf(w, "%s [y/N] ", prompt)
	r := bufio.NewReader(in)
	line, _ := r.ReadString('\n')
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes"
}

// promptIndex reads a 1-based selection in [1, n] and returns the 0-based
// index. Errors on EOF, non-numeric input, or out-of-range.
func promptIndex(w io.Writer, in io.Reader, prompt string, n int) (int, error) {
	fmt.Fprint(w, prompt)
	r := bufio.NewReader(in)
	line, err := r.ReadString('\n')
	if err != nil && line == "" {
		return 0, err
	}
	s := strings.TrimSpace(line)
	i, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("not a number: %q", s)
	}
	if i < 1 || i > n {
		return 0, fmt.Errorf("out of range: %d not in [1, %d]", i, n)
	}
	return i - 1, nil
}
