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
	"github.com/xianxu/nous/lib/identity"
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

	cmd := &cobra.Command{
		Use:   "new BRAIN-PATH",
		Short: "Provision a new brain (single or multi-recipient)",
		Long: `Provision a fresh gcrypt-encrypted brain at the given path. With no
recipient flags, the brain is private (single recipient = the operator).
With --recipient or --fingerprint, additional GPG keys are admitted —
each goes through the verify-fingerprint ceremony if it's not already
in the local keyring.

Flags (repeatable):
  --recipient PUBKEY-FILE   Admit a peer; runs the verify-fingerprint
                            ceremony before importing.
  --fingerprint FP          Admit an already-imported pubkey by
                            fingerprint. Still runs a confirmation
                            prompt before admitting.

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
			if len(ownKeys) > 1 {
				return fmt.Errorf("multiple secret keys in keyring (%d); pass --fingerprint to disambiguate which is the brain anchor", len(ownKeys))
			}
			ownFp := ownKeys[0].Fingerprint

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
			c.Env = os.Environ()
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
	return cmd
}

func findNewBrainScript() (string, error) {
	if dir := os.Getenv("NOUS_DIR"); dir != "" {
		p := filepath.Join(dir, "scripts", "new-brain.sh")
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	if exe, err := os.Executable(); err == nil {
		// <root>/<repo>/bin/<exe> → <root>/<repo>/scripts/new-brain.sh
		bin := filepath.Dir(exe)
		repo := filepath.Dir(bin)
		p := filepath.Join(repo, "scripts", "new-brain.sh")
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	if p, err := filepath.Abs("scripts/new-brain.sh"); err == nil {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("scripts/new-brain.sh not found; set $NOUS_DIR to the nous repo root")
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
