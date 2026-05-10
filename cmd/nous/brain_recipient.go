package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/xianxu/nous/lib/brain"
	"github.com/xianxu/nous/lib/brainsync"
	"github.com/xianxu/nous/lib/identity"
)

func newBrainRecipientCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "recipient",
		Short: "List, admit, or revoke recipients on a brain",
		Args:  cobra.NoArgs,
	}
	cmd.AddCommand(
		newBrainRecipientListCmd(),
		newBrainRecipientAddCmd(),
		newBrainRecipientRemoveCmd(),
	)
	return cmd
}

// ─── list ─────────────────────────────────────────────────────────────

func newBrainRecipientListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list BRAIN",
		Short: "List recipients on a brain (manifest + gcrypt-participants)",
		Long: `Print the recipient list two ways: from the .brain/config.md manifest
(operator-authored, the source of truth for who *should* be admitted)
and from gcrypt-participants (git config, what gcrypt actually
encrypts to on push). They should agree; mismatch is a sign of
hand-editing one without the other and is worth flagging.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBrainRecipientList(cmd.OutOrStdout(), args[0])
		},
	}
}

func runBrainRecipientList(w io.Writer, brainPath string) error {
	m, err := brain.Read(brainPath)
	if err != nil {
		return err
	}
	gcrypt, err := brain.ReadGcryptParticipants(brainPath)
	if err != nil {
		return err
	}

	// Build a known-key index for human-readable annotation. Same data
	// as `nous identity list`'s joined view.
	annotate, err := buildKeyAnnotator()
	if err != nil {
		// Annotation is best-effort — surfacing a gpg outage shouldn't
		// block recipient inspection. Fall through with a noop annotator.
		fmt.Fprintf(os.Stderr, "warning: keyring annotation unavailable: %v\n", err)
		annotate = func(string) string { return "" }
	}

	fmt.Fprintf(w, "Brain: %s\n", brainPath)
	if m.Name != "" {
		fmt.Fprintf(w, "Name:  %s\n", m.Name)
	}
	fmt.Fprintf(w, "Shared: %v (%d recipient%s)\n", m.Shared(), len(m.Recipients), pluralS(len(m.Recipients)))
	fmt.Fprintln(w)

	fmt.Fprintln(w, "Recipients (manifest):")
	if len(m.Recipients) == 0 {
		fmt.Fprintln(w, "  (none)")
	}
	for _, fp := range m.Recipients {
		fmt.Fprintf(w, "  %s  %s\n", fp, annotate(fp))
	}

	if len(gcrypt) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "gcrypt-participants (git config):")
		for _, fp := range gcrypt {
			fmt.Fprintf(w, "  %s  %s\n", fp, annotate(fp))
		}

		// Mismatch detection: order doesn't matter; set equality does.
		ms := setOf(m.Recipients)
		gs := setOf(gcrypt)
		mOnly := setDiff(ms, gs)
		gOnly := setDiff(gs, ms)
		if len(mOnly) > 0 || len(gOnly) > 0 {
			fmt.Fprintln(w)
			fmt.Fprintln(w, "WARNING: manifest and gcrypt-participants disagree.")
			if len(mOnly) > 0 {
				fmt.Fprintf(w, "  Only in manifest:           %s\n", strings.Join(mOnly, ", "))
			}
			if len(gOnly) > 0 {
				fmt.Fprintf(w, "  Only in gcrypt-participants: %s\n", strings.Join(gOnly, ", "))
			}
			fmt.Fprintln(w, "  Run `nous brain recipient add/remove` (or hand-edit) to reconcile.")
		}
	}
	return nil
}

// buildKeyAnnotator returns a function that, given a fingerprint,
// returns a human-readable annotation like "(self) Xian Xu <...>" or
// "(peer) Wife <wife@...>" or "(unknown)" — for use in `recipient
// list` output.
func buildKeyAnnotator() (func(string) string, error) {
	secret, err := identity.List()
	if err != nil {
		return nil, err
	}
	pub, err := identity.ListPublic()
	if err != nil {
		return nil, err
	}
	return func(fp string) string {
		fpU := strings.ToUpper(fp)
		for _, k := range secret {
			if strings.EqualFold(k.Fingerprint, fpU) {
				return fmt.Sprintf("(self) %s", k.UID)
			}
		}
		for _, k := range pub {
			if strings.EqualFold(k.Fingerprint, fpU) {
				return fmt.Sprintf("(peer) %s", k.UID)
			}
		}
		return "(unknown — not in keyring)"
	}, nil
}

// ─── add ──────────────────────────────────────────────────────────────

func newBrainRecipientAddCmd() *cobra.Command {
	var fingerprint string

	cmd := &cobra.Command{
		Use:   "add BRAIN [PUBKEY-FILE]",
		Short: "Admit a recipient to a brain (TTY-only; verify-fingerprint ceremony)",
		Long: `Admit a recipient to a brain. Two paths:

  nous brain recipient add BRAIN PUBKEY-FILE
    Imports the pubkey first (running the same verify-fingerprint
    ceremony as `+"`nous identity import`"+`), then admits it.

  nous brain recipient add BRAIN --fingerprint FP
    The pubkey must already be in the keyring (e.g. earlier import).
    Still runs a verify-fingerprint confirmation before admitting.

After admission: appends to manifest + gcrypt-participants, re-renders
the manifest, and pushes (gcrypt re-encrypts on push). Push failure
leaves the local config inconsistent — re-run after fixing the remote.

TTY-only: identity admission is a delegation boundary. See
brain/atlas/threat-model-shared-brain.md.`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !term.IsTerminal(int(os.Stdin.Fd())) {
				return fmt.Errorf("nous brain recipient add requires an interactive terminal (TTY-only safeguard)")
			}
			brainPath := args[0]
			out := cmd.OutOrStdout()

			var key identity.Key
			switch {
			case len(args) == 2 && fingerprint != "":
				return fmt.Errorf("pass either PUBKEY-FILE or --fingerprint, not both")
			case len(args) == 2:
				// Import path. Reuse the identity-import ceremony.
				k, err := importPubkeyFromFile(out, args[1])
				if err != nil {
					return err
				}
				key = k
			case fingerprint != "":
				// Already-imported path. Look up + run a confirmation
				// ceremony anyway — admitting to a brain is a separate
				// trust event from the original import.
				k, err := lookupKey(fingerprint)
				if err != nil {
					return err
				}
				key = k
				if err := confirmKey(out, key); err != nil {
					return err
				}
			default:
				return fmt.Errorf("supply PUBKEY-FILE or --fingerprint")
			}

			// Apply: manifest + gcrypt-participants.
			m, err := brain.Read(brainPath)
			if err != nil {
				return err
			}
			if containsFold(m.Recipients, key.Fingerprint) {
				fmt.Fprintf(out, "Already a recipient: %s\n", key.Last8())
				return nil
			}
			m.Recipients = append(m.Recipients, key.Fingerprint)
			if err := brain.WriteManifest(brainPath, m); err != nil {
				return err
			}
			if err := brain.SetGcryptParticipants(brainPath, m.Recipients); err != nil {
				return err
			}

			fmt.Fprintf(out, "Admitted %s to %s.\n", key.Last8(), brainPath)
			fmt.Fprintln(out, "Pushing so gcrypt re-encrypts to the new recipient set …")
			if err := brainsync.AddCommitPush(brainPath, fmt.Sprintf("recipient: admit %s", key.Last8())); err != nil {
				return fmt.Errorf("push: %w (manifest + git config updated locally; re-run after fixing the remote)", err)
			}
			fmt.Fprintln(out, "Pushed.")
			return nil
		},
	}
	cmd.Flags().StringVar(&fingerprint, "fingerprint", "", "use an already-imported pubkey by fingerprint")
	return cmd
}

// importPubkeyFromFile is the same flow as cmd/nous/identity.go's
// import RunE — read the file, Inspect, prompt for last-8 confirmation,
// commit. Lifted into a helper so brain recipient add can reuse it
// without re-prompting twice.
func importPubkeyFromFile(out io.Writer, path string) (identity.Key, error) {
	var data []byte
	var err error
	if path == "-" {
		data, err = io.ReadAll(os.Stdin)
	} else {
		data, err = os.ReadFile(path)
	}
	if err != nil {
		return identity.Key{}, fmt.Errorf("read pubkey: %w", err)
	}
	armor := string(data)

	peer, err := identity.Inspect(armor)
	if err != nil {
		return identity.Key{}, err
	}
	fmt.Fprintf(out, "Pubkey to admit:\n")
	fmt.Fprintf(out, "  fingerprint: %s\n", peer.Fingerprint)
	fmt.Fprintf(out, "  last-8:      %s\n", peer.Last8())
	fmt.Fprintf(out, "  uid:         %s\n", displayUID(peer))
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Verify the last 8 hex chars match what the peer sent you OUT OF BAND")
	fmt.Fprintln(out, "(phone, in-person, signed message — NOT the same channel as the pubkey).")
	fmt.Fprintln(out)

	if err := promptVerify(os.Stdin, out, peer.Last8()); err != nil {
		return identity.Key{}, err
	}
	if _, err := identity.Import(armor); err != nil {
		return identity.Key{}, err
	}
	fmt.Fprintf(out, "Imported %s.\n\n", peer.Last8())
	return peer, nil
}

// lookupKey finds a key in the keyring by full fingerprint or last-8.
// Errors when ambiguous (multiple last-8 matches; rare but possible)
// or absent.
func lookupKey(fingerprint string) (identity.Key, error) {
	all := []identity.Key{}
	if secret, err := identity.List(); err == nil {
		all = append(all, secret...)
	}
	if pub, err := identity.ListPublic(); err == nil {
		all = append(all, pub...)
	}
	want := strings.ToUpper(fingerprint)
	var hits []identity.Key
	for _, k := range all {
		fp := strings.ToUpper(k.Fingerprint)
		if fp == want || strings.HasSuffix(fp, want) {
			hits = append(hits, k)
		}
	}
	if len(hits) == 0 {
		return identity.Key{}, fmt.Errorf("no key in keyring matching %q (run `nous identity import` first)", fingerprint)
	}
	if len(hits) > 1 {
		var fps []string
		for _, h := range hits {
			fps = append(fps, h.Fingerprint)
		}
		return identity.Key{}, fmt.Errorf("ambiguous fingerprint %q (matches %d keys: %s); pass the full 40-char form", fingerprint, len(hits), strings.Join(fps, ", "))
	}
	return hits[0], nil
}

// confirmKey runs a one-shot verify-fingerprint prompt against an
// already-imported key. Same form as the import-time ceremony.
func confirmKey(out io.Writer, k identity.Key) error {
	fmt.Fprintf(out, "About to admit:\n")
	fmt.Fprintf(out, "  fingerprint: %s\n", k.Fingerprint)
	fmt.Fprintf(out, "  last-8:      %s\n", k.Last8())
	fmt.Fprintf(out, "  uid:         %s\n", displayUID(k))
	fmt.Fprintln(out)
	return promptVerify(os.Stdin, out, k.Last8())
}

// ─── remove ───────────────────────────────────────────────────────────

func newBrainRecipientRemoveCmd() *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:   "remove BRAIN FINGERPRINT",
		Short: "Revoke a recipient from a brain (TTY-only; safeguards)",
		Long: `Remove a recipient from a brain. Updates the manifest +
gcrypt-participants and pushes so future encryptions exclude the
removed recipient.

Three safeguards:

  1. Last-recipient guard: refuse to remove the only recipient
     (would orphan the brain — nobody could decrypt future pushes).
  2. Self-removal warning: if removing your own key, you lose access.
     Confirm with --force.
  3. Revocation reality: gcrypt re-encrypts only on push. Blobs
     already in the remote's object store stay readable to the
     removed recipient if they retained their refs. True revocation
     requires re-keying (out of scope here; document for the operator).

TTY-only.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !term.IsTerminal(int(os.Stdin.Fd())) {
				return fmt.Errorf("nous brain recipient remove requires an interactive terminal (TTY-only safeguard)")
			}
			brainPath := args[0]
			fpArg := args[1]
			out := cmd.OutOrStdout()

			m, err := brain.Read(brainPath)
			if err != nil {
				return err
			}
			// Find the recipient to remove (case-insensitive, last-8 OK).
			want := strings.ToUpper(fpArg)
			var match string
			for _, fp := range m.Recipients {
				up := strings.ToUpper(fp)
				if up == want || strings.HasSuffix(up, want) {
					match = fp
					break
				}
			}
			if match == "" {
				return fmt.Errorf("not a recipient of %s: %s", filepath.Base(brainPath), fpArg)
			}

			// Last-recipient guard.
			if len(m.Recipients) == 1 {
				return fmt.Errorf("refusing to remove the only recipient (%s) — would orphan %s. Add another recipient first, or delete the brain", match, brainPath)
			}

			// Self-removal warning.
			ownKeys, _ := identity.List()
			isSelf := false
			for _, k := range ownKeys {
				if strings.EqualFold(k.Fingerprint, match) {
					isSelf = true
					break
				}
			}
			if isSelf && !force {
				return fmt.Errorf("refusing to remove your own key (%s) — you would lose access to %s. Re-run with --force if intentional", match, filepath.Base(brainPath))
			}

			// Revocation reality.
			fmt.Fprintf(out, "Removing %s from %s.\n", match, filepath.Base(brainPath))
			fmt.Fprintln(out)
			fmt.Fprintln(out, "REVOCATION CAVEAT:")
			fmt.Fprintln(out, "  gcrypt re-encrypts on push, so future commits will exclude this recipient.")
			fmt.Fprintln(out, "  However: any gcrypt blob currently in the remote (or in their local clone)")
			fmt.Fprintln(out, "  remains readable to them with their existing key material. True revocation")
			fmt.Fprintln(out, "  requires re-keying the brain (rotate the operator's key + re-encrypt all")
			fmt.Fprintln(out, "  history). That's out of scope for `recipient remove`.")
			fmt.Fprintln(out)
			if !force {
				if err := promptYes(os.Stdin, out, "Proceed with removal? [y/N] "); err != nil {
					return err
				}
			}

			// Apply.
			next := make([]string, 0, len(m.Recipients)-1)
			for _, fp := range m.Recipients {
				if !strings.EqualFold(fp, match) {
					next = append(next, fp)
				}
			}
			m.Recipients = next
			if err := brain.WriteManifest(brainPath, m); err != nil {
				return err
			}
			if err := brain.SetGcryptParticipants(brainPath, m.Recipients); err != nil {
				return err
			}

			fmt.Fprintf(out, "Removed locally. Pushing so gcrypt re-encrypts …\n")
			short := match
			if len(short) >= 8 {
				short = strings.ToLower(short[len(short)-8:])
			}
			if err := brainsync.AddCommitPush(brainPath, fmt.Sprintf("recipient: revoke %s", short)); err != nil {
				return fmt.Errorf("push: %w (manifest + git config updated locally; re-run after fixing the remote)", err)
			}
			fmt.Fprintln(out, "Pushed.")
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "skip self-removal warning + interactive confirmation")
	return cmd
}

// promptYes reads a y/n confirmation. Empty / "n" / "no" / "esc" are
// treated as "no" — explicit "y"/"yes" is required to proceed.
func promptYes(in io.Reader, out io.Writer, prompt string) error {
	fmt.Fprint(out, prompt)
	r := bufio.NewReader(in)
	line, err := r.ReadString('\n')
	if err != nil {
		return fmt.Errorf("read confirmation: %w", err)
	}
	v := strings.ToLower(strings.TrimSpace(line))
	if v == "y" || v == "yes" {
		return nil
	}
	return fmt.Errorf("aborted")
}

// ─── helpers ──────────────────────────────────────────────────────────

func pluralS(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func setOf(xs []string) map[string]bool {
	m := make(map[string]bool, len(xs))
	for _, x := range xs {
		m[strings.ToUpper(x)] = true
	}
	return m
}

func setDiff(a, b map[string]bool) []string {
	var out []string
	for x := range a {
		if !b[x] {
			out = append(out, x)
		}
	}
	return out
}

func containsFold(xs []string, target string) bool {
	for _, x := range xs {
		if strings.EqualFold(x, target) {
			return true
		}
	}
	return false
}
