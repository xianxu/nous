package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/xianxu/nous/lib/brain"
	"github.com/xianxu/nous/lib/gh"
)

// newBrainPublishCmd builds `nous brain publish [--brain PATH]`: the
// "local → private" rung of the topology ladder (nous#33). It takes a
// local-only brain (created by `nous brain new`, no remote) and gives
// it a hosted encrypted backup — a private GitHub repo, gcrypt remote,
// first push. Single-recipient throughout: a local brain is always
// private (one recipient = the operator); going shared comes afterward
// via `nous brain invite`.
//
// Audience: (h) human operator — TTY confirmation by default (the push
// has outward-facing side effects: it creates a GitHub repo). Scriptable
// with --brain + --yes.
func newBrainPublishCmd() *cobra.Command {
	var brainPath string
	var yes bool

	cmd := &cobra.Command{
		Use:   "publish",
		Short: "Publish a local brain to GitHub (encrypted; local → private)",
		Long: `Promote a local-only brain to a GitHub-backed private brain: create a
private GitHub repo, wire the gcrypt remote (encrypted to the operator's
key), and push. Only gcrypt ciphertext touches GitHub.

This is the middle rung of the topology ladder — ` + "`nous brain new`" + ` makes
a local brain, ` + "`nous brain publish`" + ` backs it up to GitHub, and
` + "`nous brain invite`" + ` shares it with others.

Refuses if the brain already has a remote (it's already published).

Flags:
  --brain PATH   Skip the brain picker and target this brain directly.
  --yes          Skip the confirmation prompt.

Env overrides (advanced): NOUS_GH_OWNER, NOUS_GH_NAME pick the GitHub
owner/repo name (defaults: your gh login / the brain dir's basename);
SKIP_REPO_CREATE=1 skips repo creation when you've made it manually.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBrainPublish(cmd, brainPath, yes)
		},
	}
	cmd.Flags().StringVar(&brainPath, "brain", "", "target brain path (skips picker)")
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt")
	return cmd
}

func runBrainPublish(cmd *cobra.Command, brainPathFlag string, yes bool) error {
	out := cmd.OutOrStdout()
	in := cmd.InOrStdin()

	m, err := resolvePublishTargetBrain(out, in, brainPathFlag)
	if err != nil {
		return err
	}
	abs, err := filepath.Abs(m.Path)
	if err != nil {
		return err
	}

	// Guard: publish is local → private. A brain with a remote is past
	// this rung.
	if err := ensureLocalForPublish(abs); err != nil {
		return err
	}

	recipients := m.Recipients
	if len(recipients) == 0 {
		return fmt.Errorf("brain %q manifest has no recipients — refusing to publish a brain with no one to encrypt to", abs)
	}
	// A local brain is single-recipient by construction (InitLocal seeds
	// exactly the operator); recipients[0] is the operator's key, used
	// for the keys-branch <login>.asc publish below.
	ownFp := recipients[0]

	name := filepath.Base(abs)
	owner, _ := gh.AuthLogin() // best-effort, for the confirm message only
	fmt.Fprintf(out, "About to publish local brain to GitHub:\n")
	fmt.Fprintf(out, "  brain:      %s\n", name)
	fmt.Fprintf(out, "  repo:       %s/%s (private)\n", orPlaceholder(owner, "<your-login>"), name)
	fmt.Fprintf(out, "  recipients: %s\n", strings.Join(shortFps(recipients), ", "))
	fmt.Fprintln(out, "  Only gcrypt ciphertext touches GitHub.")
	fmt.Fprintln(out)
	if !yes {
		if !term.IsTerminal(int(os.Stdin.Fd())) {
			return errors.New("stdin is not a TTY and --yes wasn't passed; refusing to publish without confirmation")
		}
		if !confirmYN(out, in, "Publish?") {
			return errors.New("aborted by operator")
		}
	}

	// GitHub half: gh repo create + gcrypt remote + push. Delegated to
	// scripts/publish-brain.sh (the proven gh-create ceremony); the
	// recipient list is passed in so the script doesn't parse YAML.
	script, err := findPublishScript()
	if err != nil {
		return err
	}
	c := exec.Command("bash", script, abs)
	c.Env = append(os.Environ(), "NOUS_GCRYPT_PARTICIPANTS="+strings.Join(recipients, " "))
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	if err := c.Run(); err != nil {
		return fmt.Errorf("scripts/publish-brain.sh: %w", err)
	}

	// Keys branch: publish recipient pubkeys so future peers can
	// auto-import before gcrypt signature verification (nous#23). Shared
	// with `nous brain new`'s multi-recipient path.
	publishKeysBranch(out, cmd.Context(), abs, recipients, ownFp)

	fmt.Fprintln(out)
	fmt.Fprintf(out, "Published. %q is now private (GitHub-backed, gcrypt-encrypted).\n", name)
	fmt.Fprintln(out, "  • `nous brain invite <github-login>` to share it (→ shared)")
	return nil
}

// ensureLocalForPublish refuses to publish a brain that already has a
// remote — publish is the local → private transition, and re-pointing
// an existing origin is not what the operator means.
func ensureLocalForPublish(brainRoot string) error {
	if url := readBrainOriginURL(brainRoot); url != "" {
		return fmt.Errorf("brain %q already has a remote (%s) — it's already published; use `nous brain invite` to share it", filepath.Base(brainRoot), url)
	}
	return nil
}

// resolvePublishTargetBrain returns the brain to publish: the one at
// --brain PATH, the sole brain in the workspace, or an operator pick
// from a numbered list. Mirrors resolveInviteTargetBrain.
func resolvePublishTargetBrain(w io.Writer, in io.Reader, brainPathFlag string) (brain.Manifest, error) {
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

	sort.Slice(brains, func(i, j int) bool {
		return filepath.Base(brains[i].Path) < filepath.Base(brains[j].Path)
	})
	fmt.Fprintln(w, "Pick a brain to publish:")
	for i, b := range brains {
		marker := ""
		if readBrainOriginURL(b.Path) == "" {
			marker = " (local)"
		} else {
			marker = " (already published)"
		}
		fmt.Fprintf(w, "  [%d] %s%s\n", i+1, filepath.Base(b.Path), marker)
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return brain.Manifest{}, errors.New("multiple brains and stdin is not a TTY; pass --brain PATH")
	}
	idx, err := promptIndex(w, in, "Select brain", len(brains))
	if err != nil {
		return brain.Manifest{}, err
	}
	return brains[idx], nil
}

// publishKeysBranch publishes every recipient's pubkey to the brain's
// `keys` filestore branch (nous#23), plus the operator's own
// `<login>.asc` (nous#26). Best-effort: a keys-branch failure doesn't
// undo the gcrypt provisioning — the brain works without it, peers just
// fall back to sneakernet until a later publish succeeds. Shared by
// `nous brain new` (multi-recipient) and `nous brain publish`.
func publishKeysBranch(out io.Writer, ctx context.Context, brainAbs string, recipients []string, ownFp string) {
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Publishing recipient pubkeys to the keys branch …")
	anyFailed := false
	for _, fp := range recipients {
		if err := brain.PublishPubkey(ctx, brainAbs, fp); err != nil {
			fmt.Fprintf(out, "  warning: publish %s: %v\n", shortFp(fp), err)
			anyFailed = true
			continue
		}
		fmt.Fprintf(out, "  published %s\n", shortFp(fp))
	}
	if myLogin, err := gh.AuthLogin(); err == nil && myLogin != "" {
		if err := brain.PublishOwnPubkey(ctx, brainAbs, myLogin, ownFp); err != nil {
			fmt.Fprintf(out, "  warning: publish %s.asc: %v\n", myLogin, err)
			anyFailed = true
		} else {
			fmt.Fprintf(out, "  published %s.asc (operator)\n", myLogin)
		}
	} else {
		fmt.Fprintln(out, "  note: couldn't resolve github login (gh auth?); skipping <login>.asc publish.")
		fmt.Fprintln(out, "        Run `nous brain join <owner>/<repo>` from this host later to publish.")
	}
	if anyFailed {
		fmt.Fprintln(out, "  (some publishes failed; peers may need sneakernet pubkey exchange)")
	}
}

// findPublishScript locates scripts/publish-brain.sh (the GitHub half of
// the local → private transition). See findNousFile for resolution order.
func findPublishScript() (string, error) {
	return findNousFile(filepath.Join("scripts", "publish-brain.sh"))
}

// orPlaceholder returns s, or placeholder when s is empty.
func orPlaceholder(s, placeholder string) string {
	if s == "" {
		return placeholder
	}
	return s
}

// shortFps maps shortFp over a fingerprint list.
func shortFps(fps []string) []string {
	out := make([]string, len(fps))
	for i, fp := range fps {
		out[i] = shortFp(fp)
	}
	return out
}
