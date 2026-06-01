package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/xianxu/nous/lib/brain"
	"github.com/xianxu/nous/lib/brainsync"
	"github.com/xianxu/nous/lib/gh"
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
		newBrainRecipientVerifyCmd(),
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

// buildKeyAnnotator delegates to lib/brain.Annotator so the cmd-level
// caller doesn't have to reach into lib/identity directly. Kept as a
// shim for now; the brain TUI calls brain.Annotator() directly.
func buildKeyAnnotator() (func(string) string, error) {
	return brain.Annotator()
}

// ─── add ──────────────────────────────────────────────────────────────

func newBrainRecipientAddCmd() *cobra.Command {
	var fingerprint string
	var verifiedLast8 string

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
leaves the local config staged + committed; re-running the same
command detects the unpushed commit and retries the push (no double
manifest mutation).

TTY-only: identity admission is a delegation boundary. See
brain/atlas/threat-model-shared-brain.md.`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if verifiedLast8 == "" && !term.IsTerminal(int(os.Stdin.Fd())) {
				return fmt.Errorf("nous brain recipient add requires an interactive terminal, or pass --verified-last8 <8hex> for scripted use (TTY-only safeguard)")
			}
			brainPath := args[0]
			out := cmd.OutOrStdout()

			var key identity.Key
			switch {
			case len(args) == 2 && fingerprint != "":
				return fmt.Errorf("pass either PUBKEY-FILE or --fingerprint, not both")
			case len(args) == 2:
				// Import path. Reuse the identity-import ceremony.
				k, err := importPubkeyFromFile(out, args[1], verifiedLast8)
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
				if err := confirmKey(out, key, verifiedLast8); err != nil {
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
			if brain.ContainsRecipient(m.Recipients, key.Fingerprint) {
				// Already a recipient locally. If we have unpushed
				// commits (e.g., previous push failed and operator
				// re-ran), retry the push so the remote catches up.
				// Otherwise, true no-op.
				unpushed, _ := brainsync.HasUnpushedCommits(brainPath)
				if unpushed {
					fmt.Fprintf(out, "Already a recipient locally: %s. Retrying push …\n", key.Last8())
					if err := brainsync.Push(brainPath); err != nil {
						return fmt.Errorf("push: %w", err)
					}
					fmt.Fprintln(out, "Pushed.")
					return nil
				}
				fmt.Fprintf(out, "Already a recipient: %s\n", key.Last8())
				return nil
			}
			m.Recipients = append(m.Recipients, key.Fingerprint)
			if err := brain.RewriteFrontmatter(brainPath, m); err != nil {
				return err
			}
			// gcrypt-participants derives from the manifest at push
			// time (nous#24) — no explicit SetGcryptParticipants here.

			fmt.Fprintf(out, "Admitted %s to %s.\n", key.Last8(), brainPath)
			fmt.Fprintln(out, "Pushing so gcrypt re-encrypts to the new recipient set …")
			if err := brainsync.AddCommitPush(brainPath, fmt.Sprintf("recipient: admit %s", key.Last8())); err != nil {
				return fmt.Errorf("push: %w (manifest + git config committed locally; re-run to retry push)", err)
			}
			fmt.Fprintln(out, "Pushed.")

			// Publish the new recipient's pubkey to the keys filestore
			// (nous#23). Best-effort: a failure here leaves the brain
			// gcrypt-functional but the new peer would need a sneakernet
			// pubkey hand-off until publish succeeds.
			if err := brain.PublishPubkey(cmd.Context(), brainPath, key.Fingerprint); err != nil {
				fmt.Fprintf(out, "  warning: publish %s to keys branch: %v\n", key.Last8(), err)
				fmt.Fprintln(out, "  (peer may need sneakernet pubkey hand-off until keys-branch publish succeeds)")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&fingerprint, "fingerprint", "", "use an already-imported pubkey by fingerprint")
	cmd.Flags().StringVar(&verifiedLast8, "verified-last8", "", "last-8 hex of the recipient's fingerprint, verified out-of-band — satisfies the ceremony non-interactively (scripted/test use; lifts the TTY gate)")
	return cmd
}

// importPubkeyFromFile is the same flow as cmd/nous/identity.go's
// import RunE — read the file, Inspect, prompt for last-8 confirmation,
// commit. Lifted into a helper so brain recipient add can reuse it
// without re-prompting twice.
func importPubkeyFromFile(out io.Writer, path, verifiedLast8 string) (identity.Key, error) {
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

	if err := verifyLast8(os.Stdin, out, peer.Last8(), verifiedLast8); err != nil {
		return identity.Key{}, err
	}
	if _, err := identity.Import(armor); err != nil {
		return identity.Key{}, err
	}
	fmt.Fprintf(out, "Imported %s.\n\n", peer.Last8())
	return peer, nil
}

// lookupKey finds a key in the keyring by full fingerprint (40 hex
// chars) or short fingerprint (8+ trailing hex chars). Shorter-than-8
// inputs error explicitly — typo-protection against accidentally
// matching the wrong key with a 2-char suffix.
//
// Errors when the lookup is ambiguous (multiple last-8 collisions;
// rare but possible) or absent.
func lookupKey(fingerprint string) (identity.Key, error) {
	want := strings.ToUpper(strings.TrimSpace(fingerprint))
	if len(want) != 40 && len(want) < 8 {
		return identity.Key{}, fmt.Errorf("fingerprint %q is too short — pass the last 8 hex chars (or full 40-char form) to avoid accidental matches", fingerprint)
	}

	all := []identity.Key{}
	if secret, err := identity.List(); err == nil {
		all = append(all, secret...)
	}
	if pub, err := identity.ListPublic(); err == nil {
		all = append(all, pub...)
	}
	var hits []identity.Key
	for _, k := range all {
		fp := strings.ToUpper(k.Fingerprint)
		if fp == want || (len(want) >= 8 && strings.HasSuffix(fp, want)) {
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
func confirmKey(out io.Writer, k identity.Key, verifiedLast8 string) error {
	fmt.Fprintf(out, "About to admit:\n")
	fmt.Fprintf(out, "  fingerprint: %s\n", k.Fingerprint)
	fmt.Fprintf(out, "  last-8:      %s\n", k.Last8())
	fmt.Fprintf(out, "  uid:         %s\n", displayUID(k))
	fmt.Fprintln(out)
	return verifyLast8(os.Stdin, out, k.Last8(), verifiedLast8)
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
			match, err := brain.MatchRecipient(m.Recipients, fpArg)
			if err != nil {
				return err
			}
			if match == "" {
				// Not in the manifest. Either the operator typo'd, or
				// a previous remove succeeded locally but the push
				// failed — in which case the local commit is sitting
				// unpushed and a re-run should retry the push rather
				// than confusingly error out.
				unpushed, _ := brainsync.HasUnpushedCommits(brainPath)
				if unpushed {
					fmt.Fprintf(out, "Not a recipient locally (already removed?). Retrying push …\n")
					if err := brainsync.Push(brainPath); err != nil {
						return fmt.Errorf("push: %w", err)
					}
					fmt.Fprintln(out, "Pushed.")
					return nil
				}
				return fmt.Errorf("not a recipient of %s: %s", filepath.Base(brainPath), fpArg)
			}

			// Last-recipient guard.
			if err := brain.CanRemoveRecipient(m); err != nil {
				return fmt.Errorf("%w (removing %s from %s)", err, match, brainPath)
			}

			// Self-removal warning: refuse when the removal would leave
			// no local-secret recipient on the brain (real lockout
			// floor, not just "you happen to have the secret half").
			wouldLock, err := brain.WouldLockOut(m.Recipients, match)
			if err != nil {
				return fmt.Errorf("check decrypt path: %w", err)
			}
			if wouldLock && !force {
				return fmt.Errorf("refusing — removing %s leaves you with no decrypt path on %s. Re-run with --force if intentional", match, filepath.Base(brainPath))
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
			m.Recipients = brain.WithoutRecipient(m.Recipients, match)
			if err := brain.RewriteFrontmatter(brainPath, m); err != nil {
				return err
			}
			// gcrypt-participants derives from the manifest at push
			// time (nous#24) — no explicit SetGcryptParticipants here.

			fmt.Fprintf(out, "Removed locally. Pushing so gcrypt re-encrypts …\n")
			short := match
			if len(short) >= 8 {
				short = strings.ToLower(short[len(short)-8:])
			}
			if err := brainsync.AddCommitPush(brainPath, fmt.Sprintf("recipient: revoke %s", short)); err != nil {
				return fmt.Errorf("push: %w (manifest + git config committed locally; re-run to retry push)", err)
			}
			fmt.Fprintln(out, "Pushed.")

			// Revoke the pubkey from the keys filestore (nous#23). The
			// removed peer can still decrypt any blob they had access
			// to during their admission window — that's the structural
			// "revocation is heavy" caveat from the threat model. What
			// the keys-branch revoke does prevent: new peers cloning
			// after revocation auto-importing the gone peer's pubkey
			// and thinking they're still part of the set.
			if err := brain.RevokePubkey(cmd.Context(), brainPath, match); err != nil {
				fmt.Fprintf(out, "  warning: revoke %s from keys branch: %v\n", short, err)
				fmt.Fprintln(out, "  (manifest update succeeded; keys-branch entry left in place — re-run revoke to retry)")
			}
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

// ─── verify (opt-in ceremony for paranoid users) ──────────────────────

func newBrainRecipientVerifyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "verify BRAIN-PATH FINGERPRINT",
		Short: "Confirm a recipient's pubkey matches an out-of-band fingerprint (opt-in)",
		Long: `Verify-fingerprint ceremony for a recipient already on the brain.
Use when you want to confirm — out-of-band — that the pubkey in
your local keyring (auto-imported via the keys branch in nous#23,
or sneakernet'd in the legacy flow) really belongs to the peer
who claims to own it.

Renders the pubkey's full fingerprint + last-8 + UID, then prompts
you to type the last-8 the peer sent you via an out-of-band
channel (phone, in-person, signed message — NOT the same channel
that delivered the pubkey itself). A match prints ✓; a mismatch
surfaces the discrepancy and exits non-zero.

On a successful match, the verification is persisted to
.brain/verified.yaml — keyed by the recipient's github login —
recording the fingerprint at verify time, who verified it, and
when. The auto-admit loop reads this file: if the recipient's
keys-branch fingerprint ever changes from the verified one, auto-
admit pauses for that login (the "drift" path), forcing operator
attention before a substituted key gets silently admitted.

Persistence requires the recipient to have been admitted via the
nous#26 GitHub-mediated path (<login>.asc on the keys branch).
Legacy <FP>.asc admissions don't carry a login, so the verify
ceremony still runs but the result isn't persisted — the operator
sees a soft notice in that case.

Args:
  BRAIN-PATH    Path to the brain (relative or absolute).
  FINGERPRINT   The recipient's fingerprint to verify. Full
                40-hex or last-8 — looked up in the local keyring.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			brainPath := args[0]
			fpArg := args[1]
			out := cmd.OutOrStdout()
			in := cmd.InOrStdin()

			// Resolve the fingerprint (full or short) to a Key.
			key, err := lookupKey(fpArg)
			if err != nil {
				return fmt.Errorf("lookup %s: %w", fpArg, err)
			}

			// Confirm this key is actually a recipient of the named
			// brain — verifying a random key in the keyring isn't
			// useful, and refusing here protects against typos that
			// would let an unrelated key pass the ceremony.
			m, err := brain.Read(brainPath)
			if err != nil {
				return fmt.Errorf("read brain %s: %w", brainPath, err)
			}
			if !brain.ContainsRecipient(m.Recipients, key.Fingerprint) {
				return fmt.Errorf("%s is not a recipient of %s", key.Last8(), brainPath)
			}

			fmt.Fprintln(out, "Pubkey to verify:")
			fmt.Fprintf(out, "  fingerprint: %s\n", key.Fingerprint)
			fmt.Fprintf(out, "  last-8:      %s\n", key.Last8())
			fmt.Fprintf(out, "  uid:         %s\n", displayUID(key))
			fmt.Fprintln(out)
			fmt.Fprintln(out, "Type the last-8 the peer sent you OUT OF BAND")
			fmt.Fprintln(out, "(phone, in-person, signed message — NOT the same channel as the pubkey).")
			fmt.Fprintln(out)

			if err := promptVerify(in, out, key.Last8()); err != nil {
				fmt.Fprintln(out)
				fmt.Fprintln(out, "✗ Mismatch — the pubkey in your keyring does NOT match what you typed.")
				fmt.Fprintln(out, "  Possible causes:")
				fmt.Fprintln(out, "    (a) you typed the wrong value (re-check OOB)")
				fmt.Fprintln(out, "    (b) the keys branch was tampered with by someone with push access")
				fmt.Fprintln(out, "    (c) the pubkey was delivered through a compromised channel")
				return err
			}
			fmt.Fprintln(out)
			fmt.Fprintln(out, "✓ Match. The pubkey in your keyring is what the peer claims it is.")

			// Persist the verification to .brain/verified.yaml. Best-
			// effort: persistence requires the recipient to have a
			// <login>.asc on the keys branch (nous#26 path). Legacy
			// <FP>.asc admissions get a soft notice and the verify
			// ceremony's match is one-shot for them.
			if err := persistVerify(cmd.Context(), brainPath, key.Fingerprint, out); err != nil {
				return fmt.Errorf("persist verify: %w", err)
			}
			return nil
		},
	}
}

// persistVerify writes (or refreshes) a .brain/verified.yaml entry
// for the recipient identified by fp, then commits + pushes via the
// brainsync push wrapper. Best-effort on the github-login lookup:
// when no <login>.asc on keys branch matches the fingerprint, the
// match is one-shot (legacy admission path).
func persistVerify(ctx context.Context, brainPath, fp string, out io.Writer) error {
	login, err := brain.LoginForFingerprint(ctx, brainPath, fp)
	if err != nil {
		// Filestore failures are infrastructure issues; surface them
		// so the operator can re-verify after fixing the keys branch.
		return err
	}
	if login == "" {
		fmt.Fprintln(out)
		fmt.Fprintln(out, "  (not persisted: this recipient was admitted via the legacy <FP>.asc")
		fmt.Fprintln(out, "   path; persistent verify requires the nous#26 <login>.asc convention.)")
		return nil
	}
	verifier, err := gh.AuthLogin()
	if err != nil {
		// gh-auth outage shouldn't block the ceremony output, but the
		// operator should know we couldn't record their identity.
		fmt.Fprintln(out)
		fmt.Fprintf(out, "  (not persisted: couldn't resolve verifier identity via gh: %v)\n", err)
		return nil
	}
	v, err := brain.ReadVerified(brainPath)
	if err != nil {
		return err
	}
	v[login] = brain.VerifiedEntry{
		Fingerprint: strings.ToUpper(fp),
		VerifiedBy:  verifier,
		VerifiedAt:  time.Now().UTC().Truncate(time.Second),
	}
	if err := brain.WriteVerified(brainPath, v); err != nil {
		return err
	}
	if err := brainsync.AddCommitPush(brainPath, "verify "+login+" by "+verifier); err != nil {
		return err
	}
	fmt.Fprintln(out)
	fmt.Fprintf(out, "  Persisted: %s verified by %s — auto-admit will pause if the\n", login, verifier)
	fmt.Fprintln(out, "  keys-branch fingerprint for this login ever changes.")
	return nil
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

