package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/xianxu/nous/lib/brain"
	"github.com/xianxu/nous/lib/gh"
	"github.com/xianxu/nous/lib/identity"
	"github.com/xianxu/nous/lib/workspace"
)

// newBrainNewCmd builds `nous brain new <path>`: multi-recipient
// provisioning, replacing `make new-brain`. Single-recipient case
// still works — the operator is just the only recipient.
//
// Architecture: cmd/nous handles the recipient ceremony + canonical
// manifest/gcrypt config; substrate setup (gh repo create, git init,
// initial push) is delegated to scripts/new-brain.sh. The script does
// a single-recipient bootstrap; nous brain new then rewrites the
// manifest + gcrypt-participants for the full recipient list and does
// a second commit+push so gcrypt re-encrypts to all recipients.
//
// Two pushes is benign — both target the same gcrypt object store
// which gets fully replaced on each push. The first push encrypts
// the bootstrap commit to the operator only; the second re-keys to
// the full recipient set and supersedes the first.
//
// Future: full Go port when the surface stabilizes (M4 plan calls for
// "deletes bash scripts" but defers actual deletion to keep this
// chunk shippable).
func newBrainNewCmd() *cobra.Command {
	var recipientFiles []string
	var fingerprints []string
	var anchorFp string

	cmd := &cobra.Command{
		Use:   "new BRAIN-PATH",
		Short: "Provision a new brain (local by default)",
		Long: `Provision a fresh brain at the given path.

With no recipient flags, the brain is LOCAL-ONLY: a git repo on this
machine with no remote, no GitHub, no network. Encrypted at rest by
FileVault; gcrypt only engages once it's published. This is the bottom
rung of the topology ladder — promote it with ` + "`nous brain publish`" + `
(→ private GitHub) and then ` + "`nous brain invite`" + ` (→ shared).

With --recipient or --fingerprint, the brain is provisioned directly as
a GitHub-backed shared brain (the multi-recipient path): additional GPG
keys are admitted, each through the verify-fingerprint ceremony if it's
not already in the local keyring.

Flags:
  --as FP                   (when keyring has multiple secret keys)
                            Which of YOUR keys to anchor this brain on.
                            Defaults to the only secret key when there's
                            just one.
  --recipient PUBKEY-FILE   Admit a peer; runs the verify-fingerprint
                            ceremony before importing. Repeatable.
  --fingerprint FP          Admit an already-imported pubkey by
                            fingerprint. Still runs a confirmation
                            prompt before admitting. Repeatable.

TTY-only when --recipient or --fingerprint is passed (the ceremony
needs a human). Pure-private brains (no flags) can run non-TTY.

Substrate setup (gh repo create, git init, gcrypt configure, initial
push) is delegated to scripts/new-brain.sh as a single-recipient
bootstrap. nous brain new then re-keys to the full recipient set in
a second commit + push (gcrypt re-encrypts to all recipients).`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			brainPath := args[0]
			out := cmd.OutOrStdout()

			multiRecipient := len(recipientFiles) > 0 || len(fingerprints) > 0
			if multiRecipient && !term.IsTerminal(int(os.Stdin.Fd())) {
				return fmt.Errorf("multi-recipient provisioning requires an interactive terminal (TTY-only safeguard)")
			}

			// Resolve operator's primary fingerprint (will always be a
			// recipient).
			ownKeys, err := identity.List()
			if err != nil {
				return fmt.Errorf("list own keys: %w", err)
			}
			if len(ownKeys) == 0 {
				return fmt.Errorf("no secret key in keyring; run `nous identity init` first")
			}
			ownFp, err := pickAnchor(ownKeys, anchorFp)
			if err != nil {
				return err
			}

			// No recipient flags → bottom rung of the topology ladder: a
			// local-only brain (git repo, no remote, no GitHub, no
			// network). `nous brain publish` promotes it to a
			// GitHub-backed encrypted brain later. The multi-recipient
			// GitHub path below stays available for provisioning a shared
			// brain directly during the transition.
			if !multiRecipient {
				return provisionLocal(cmd, brainPath, ownFp)
			}

			// Collect peers.
			peers := []identity.Key{}
			for _, path := range recipientFiles {
				k, err := importPubkeyFromFile(out, path)
				if err != nil {
					return err
				}
				peers = append(peers, k)
			}
			for _, fp := range fingerprints {
				k, err := lookupKey(fp)
				if err != nil {
					return err
				}
				if err := confirmKey(out, k); err != nil {
					return err
				}
				peers = append(peers, k)
			}

			// Final recipient set: operator + peers, deduped.
			fpSet := map[string]bool{strings.ToUpper(ownFp): true}
			recipients := []string{ownFp}
			for _, p := range peers {
				up := strings.ToUpper(p.Fingerprint)
				if !fpSet[up] {
					fpSet[up] = true
					recipients = append(recipients, p.Fingerprint)
				}
			}

			// Confirm before launching the script (last chance to abort).
			fmt.Fprintf(out, "About to provision brain at %s with %d recipient(s):\n", brainPath, len(recipients))
			for _, fp := range recipients {
				short := fp
				if len(short) >= 8 {
					short = strings.ToLower(short[len(short)-8:])
				}
				fmt.Fprintf(out, "  %s  (%s)\n", short, fp)
			}
			fmt.Fprintln(out)

			// Substrate setup via scripts/new-brain.sh. The script does
			// a single-recipient bootstrap (operator's primary key);
			// we re-key to the full recipient set in a second commit
			// below. The script is unmodified — keeps it as a stable
			// known-working bootstrap.
			script, err := findNewBrainScript()
			if err != nil {
				return err
			}
			c := exec.Command("bash", script, brainPath)
			// Propagate the resolved anchor fingerprint so the script
			// doesn't re-prompt for the same identity we already
			// picked above (CLI's --as flag or the TUI's picker
			// stage). The script honors NOUS_BRAIN_ANCHOR_FP via
			// last-8/full-FP match.
			c.Env = append(os.Environ(), "NOUS_BRAIN_ANCHOR_FP="+ownFp)
			c.Stdin = os.Stdin
			c.Stdout = os.Stdout
			c.Stderr = os.Stderr
			if err := c.Run(); err != nil {
				return fmt.Errorf("scripts/new-brain.sh: %w", err)
			}

			abs, err := filepath.Abs(brainPath)
			if err != nil {
				return err
			}

			// Rewrite manifest with our canonical form (sorted
			// recipients, no mode:, multi-recipient body) and update
			// gcrypt-participants to match. For single-recipient
			// brains this is mostly a no-op (the script already wrote
			// a single-recipient setup with a `mode:` field — we drop
			// the field). For multi-recipient, this is the moment we
			// admit the peers.
			m := brain.Manifest{
				Name:          filepath.Base(abs),
				Recipients:    recipients,
				SyncSubstrate: pickSubstrate(len(recipients)),
			}
			if err := brain.WriteManifest(abs, m); err != nil {
				return fmt.Errorf("rewrite manifest: %w", err)
			}
			// gcrypt-participants will be synced from the manifest by
			// the push wrapper below (nous#24); no explicit
			// SetGcryptParticipants call needed.

			// If we changed anything, commit + push so gcrypt
			// re-encrypts to the full recipient set. Without this
			// step, the remote would be encrypted to the operator
			// only — peers added via --recipient would still be
			// listed in the manifest but locked out of the actual
			// blobs. Skipped when nothing changed locally (single-
			// recipient with no `mode:` already, e.g.).
			changed, err := hasUnstagedChanges(abs)
			if err != nil {
				return fmt.Errorf("git status: %w", err)
			}
			if changed {
				fmt.Fprintln(out)
				fmt.Fprintln(out, "Re-keying for the full recipient set …")
				msg := "init: rekey to full recipient list"
				if len(recipients) == 1 {
					msg = "init: drop mode: from manifest (per nous#14 M4c)"
				}
				if err := exec.Command("git", "-C", abs, "add", "-A").Run(); err != nil {
					return fmt.Errorf("git add: %w", err)
				}
				if err := exec.Command("git", "-C", abs, "commit", "-q", "-m", msg).Run(); err != nil {
					return fmt.Errorf("git commit: %w", err)
				}
				if err := exec.Command("git", "-C", abs, "push", "origin", "main").Run(); err != nil {
					return fmt.Errorf("git push: %w", err)
				}
				fmt.Fprintln(out, "Pushed.")
			}

			// Publish every recipient's pubkey to the brain's `keys`
			// filestore branch (nous#23). Peers cloning the brain
			// later can auto-import all pubkeys before gcrypt's
			// signature verification needs them — eliminates the
			// manual sneakernet step.
			//
			// Best-effort: a keys-branch publish failure doesn't
			// undo the gcrypt provisioning above. The brain works
			// without it; peers would need the legacy sneakernet
			// flow until publish succeeds. Surface failures loudly
			// so operator knows to remediate (re-run nous brain new,
			// or a future `nous brain publish-keys` verb).
			fmt.Fprintln(out)
			fmt.Fprintln(out, "Publishing recipient pubkeys to the keys branch …")
			ctx := cmd.Context()
			anyFailed := false
			for _, fp := range recipients {
				if err := brain.PublishPubkey(ctx, abs, fp); err != nil {
					fmt.Fprintf(out, "  warning: publish %s: %v\n", shortFp(fp), err)
					anyFailed = true
					continue
				}
				fmt.Fprintf(out, "  published %s\n", shortFp(fp))
			}
			// Also publish operator's own pubkey under the new
			// nous#26 `<login>.asc` convention. Joiners running
			// `nous brain join` orphan-create the keys branch if it's
			// empty; without this publish, a joiner's first push
			// would replace the keys branch with just their own key,
			// leaving the operator's pubkey missing for subsequent
			// signature verification. (Yesterday's brain-family bug,
			// nous#26 M7 regression test.)
			if myLogin, err := gh.AuthLogin(); err == nil && myLogin != "" {
				if err := brain.PublishOwnPubkey(ctx, abs, myLogin, ownFp); err != nil {
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

			fmt.Fprintln(out)
			fmt.Fprintln(out, "Brain provisioned.")
			return nil
		},
	}
	cmd.Flags().StringSliceVar(&recipientFiles, "recipient", nil, "pubkey file to admit as recipient (repeatable)")
	cmd.Flags().StringSliceVar(&fingerprints, "fingerprint", nil, "already-imported pubkey to admit (repeatable)")
	cmd.Flags().StringVar(&anchorFp, "as", "", "which of your own secret keys to anchor this brain on (full FP or last-8); required when keyring has multiple")
	return cmd
}

// pickAnchor resolves which of the operator's secret keys serves as
// the new brain's anchor identity. Rules:
//
//   - exactly one secret key: use it; ignore anchorArg.
//   - multiple secret keys, anchorArg empty: error with a list of
//     available keys so the operator can pass --as.
//   - multiple secret keys, anchorArg present: match against full
//     fingerprint or last-8 (case-insensitive). Error if no match
//     or if anchorArg is shorter than 8 chars (typo guard).
func pickAnchor(keys []identity.Key, anchorArg string) (string, error) {
	if len(keys) == 1 {
		return keys[0].Fingerprint, nil
	}
	if anchorArg == "" {
		var b strings.Builder
		fmt.Fprintf(&b, "multiple secret keys in keyring (%d); pass --as FP to pick which anchors this brain.\n", len(keys))
		b.WriteString("Available keys:\n")
		for _, k := range keys {
			short := k.Fingerprint
			if len(short) >= 8 {
				short = strings.ToLower(short[len(short)-8:])
			}
			fmt.Fprintf(&b, "  %s  %s\n", short, k.UID)
		}
		b.WriteString("Example: nous brain new <path> --as " + strings.ToLower(keys[0].Fingerprint[len(keys[0].Fingerprint)-8:]))
		return "", fmt.Errorf("%s", b.String())
	}
	want := strings.ToUpper(strings.TrimSpace(anchorArg))
	if len(want) != 40 && len(want) < 8 {
		return "", fmt.Errorf("--as %q is too short — pass the last 8 hex chars (or full 40-char fingerprint) to avoid accidental matches", anchorArg)
	}
	for _, k := range keys {
		up := strings.ToUpper(k.Fingerprint)
		if up == want || (len(want) >= 8 && strings.HasSuffix(up, want)) {
			return k.Fingerprint, nil
		}
	}
	return "", fmt.Errorf("--as %q matches no secret key in your keyring", anchorArg)
}

// findNousFile locates a file relative to the nous repo root (e.g.
// "scripts/new-brain.sh", "construct/setup.sh") across the common
// install layouts. Order-sensitive: more specific + operator-controlled
// paths come first.
//
// Resolution order:
//  1. $NOUS_DIR/<rel> — explicit override.
//  2. <dir-of-binary>/../<rel> — the `make nous-build` dev layout where
//     the binary is at `nous/bin/nous` (so ../ is the repo root).
//     EvalSymlinks first so bin/nous → cmd/nous/bin/nous resolves.
//  3. <workspace-root>/nous/<rel> — binary outside the nous source tree
//     (historically ~/.local/bin/nous from the retired
//     `make nous-install`); defensive fallback.
//  4. ./<rel> — CWD relative; ad-hoc dev invocations from inside nous.
func findNousFile(rel string) (string, error) {
	var candidates []string
	if dir := os.Getenv("NOUS_DIR"); dir != "" {
		candidates = append(candidates, filepath.Join(dir, rel))
	}
	if exe, err := os.Executable(); err == nil {
		real := exe
		if r, err := filepath.EvalSymlinks(exe); err == nil {
			real = r
		}
		repo := filepath.Dir(filepath.Dir(real)) // <repo>/bin/<exe> → <repo>
		candidates = append(candidates, filepath.Join(repo, rel))
	}
	if root, err := workspace.Root(); err == nil {
		candidates = append(candidates, filepath.Join(root, "nous", rel))
	}
	if p, err := filepath.Abs(rel); err == nil {
		candidates = append(candidates, p)
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("%s not found in $NOUS_DIR, binary-relative path, "+
		"workspace-root (`<workspace>/nous/%s`), or CWD-relative. "+
		"Set $NOUS_DIR to the nous repo root, or run from inside it.", rel, rel)
}

// findNewBrainScript locates scripts/new-brain.sh (multi-recipient
// GitHub provisioning path). See findNousFile for resolution order.
func findNewBrainScript() (string, error) {
	return findNousFile(filepath.Join("scripts", "new-brain.sh"))
}

// findSetupScript locates construct/setup.sh (the substrate installer
// that wires nous/ariadne symlinks + go.mod tooling into a brain).
func findSetupScript() (string, error) {
	return findNousFile(filepath.Join("construct", "setup.sh"))
}

// provisionLocal creates a local-only private brain: git init + go.mod +
// manifest (single recipient, sync_substrate none, no remote) + initial
// commit, with construct/setup.sh wiring the substrate in between. No
// GitHub, no gcrypt, no network — the bottom rung of the topology
// ladder. Delegates the scaffold to brain.InitLocal and passes a
// closure that runs setup.sh from inside the new brain.
func provisionLocal(cmd *cobra.Command, brainPath, ownFp string) error {
	out := cmd.OutOrStdout()
	abs, err := filepath.Abs(brainPath)
	if err != nil {
		return err
	}
	name := filepath.Base(abs)

	fmt.Fprintf(out, "Provisioning local brain %q (on this device only — no remote) …\n", name)

	setup := func() error {
		script, err := findSetupScript()
		if err != nil {
			// Substrate wiring is best-effort: the brain is a valid git
			// repo + manifest without it, just missing the nous/ariadne
			// symlinks. Surface the miss so the operator can re-run
			// setup.sh manually, but don't fail provisioning.
			fmt.Fprintf(out, "  note: construct/setup.sh not found (%v); skipping substrate wiring.\n", err)
			fmt.Fprintln(out, "        Run `make refresh` (or construct/setup.sh --yes) inside the brain later.")
			return nil
		}
		c := exec.Command("bash", script, "--yes")
		c.Dir = abs
		c.Stdin = os.Stdin
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr
		return c.Run()
	}

	if err := brain.InitLocal(abs, name, ownFp, setup); err != nil {
		return err
	}

	fmt.Fprintln(out)
	fmt.Fprintf(out, "Local brain provisioned at %s\n", abs)
	fmt.Fprintln(out, "  • on this device only — no remote; encrypted at rest by FileVault")
	fmt.Fprintln(out, "  • `nous brain publish` backs it up to GitHub (gcrypt-encrypted) when you're ready")
	return nil
}

// pickSubstrate returns "none" for single-recipient (private) brains
// and "" (empty — caller can override) for shared. Concrete substrate
// choice (syncthing vs git-daemon) is operator-decided post-creation;
// nous brain new doesn't pick for them.
func pickSubstrate(n int) string {
	if n <= 1 {
		return "none"
	}
	return ""
}

// hasUnstagedChanges returns true when `git status --porcelain` shows
// at least one entry — used by `nous brain new` to decide whether the
// re-key commit is needed (skip when manifest + gcrypt config already
// matched what we wanted to write).
func hasUnstagedChanges(repo string) (bool, error) {
	out, err := exec.Command("git", "-C", repo, "status", "--porcelain").Output()
	if err != nil {
		return false, err
	}
	return len(strings.TrimSpace(string(out))) > 0, nil
}

// silence unused — io is referenced via importPubkeyFromFile elsewhere.
var _ io.Reader = (*os.File)(nil)

// shortFp returns the lowercase last-8 hex chars of a fingerprint —
// the operator-facing short form used by every nous log line that
// mentions a key. Centralized here to avoid the inline len-slice
// snippet creeping into every call site.
func shortFp(fp string) string {
	if len(fp) < 8 {
		return strings.ToLower(fp)
	}
	return strings.ToLower(fp[len(fp)-8:])
}
