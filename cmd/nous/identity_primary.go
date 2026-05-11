// nous identity primary — show or set the operator's primary identity.
// Distinct from "any key with a secret half on this machine"; primary
// is the *one* key nous treats as "you" (annotations, self-removal
// safeguards, future signing identity).
//
// Resolution order when called with no args:
//
//	1. Stored state file (~/Library/Application Support/nous/primary-identity
//	   on macOS, $XDG_CONFIG_HOME/nous/primary-identity on Linux).
//	2. Exactly one secret key in the keyring → implicit primary (printed
//	   without prompting to persist; explicit set still recommended once
//	   a second key is generated).
//	3. Brain-aware heuristic: a private brain (single recipient) whose
//	   recipient is one of the local secret keys. Operator-private
//	   brains are by definition encrypted to the operator's primary —
//	   strong hint. If matched, offer to persist.
//	4. Punt: list the local secret keys + prompt the operator to pass
//	   one as an arg.
//
// Audience: (h). Interactive prompt for the persist confirmation when
// the heuristic resolves a candidate; non-TTY callers see the
// resolved value without prompting (read-only).

package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/xianxu/nous/lib/brain"
	"github.com/xianxu/nous/lib/identity"
)

func newIdentityPrimaryCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "primary [FINGERPRINT]",
		Short: "Show or set the operator's primary identity",
		Long: `Show or set the primary identity — the key nous treats as "you"
for annotations and self-removal safeguards.

Without arguments: prints the currently-stored primary, or runs the
heuristic resolver (single secret key → that one; private brain
recipient that's also a local secret → strong hint, offers to
persist).

With a fingerprint: persists that key as primary. Refuses unless the
key has a secret half on this machine.

Audience: (h) when interactive (persist confirmation prompt fires);
read-only display otherwise.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			if len(args) == 1 {
				return setPrimaryByArg(out, args[0])
			}
			return showOrResolvePrimary(out)
		},
	}
}

func setPrimaryByArg(out io.Writer, input string) error {
	fp, err := canonicalSecretFingerprint(input)
	if err != nil {
		return err
	}
	if err := identity.SetPrimary(fp); err != nil {
		return err
	}
	statePath, _ := identity.PrimaryStatePath()
	fmt.Fprintf(out, "Primary identity set to %s.\nStored at: %s\n", fp, statePath)
	return nil
}

// canonicalSecretFingerprint resolves a user-typed fingerprint (full
// 40-char or last 8+ hex chars) to the canonical 40-char form, gating
// on the key actually existing as a secret key on this machine.
// Typo-protection mirrors `nous brain recipient {add,remove}`.
func canonicalSecretFingerprint(input string) (string, error) {
	want := strings.ToUpper(strings.TrimSpace(input))
	if len(want) != 40 && len(want) < 8 {
		return "", fmt.Errorf("fingerprint %q is too short — pass the last 8 hex chars (or full 40-char form)", input)
	}
	keys, err := identity.List()
	if err != nil {
		return "", err
	}
	for _, k := range keys {
		up := strings.ToUpper(k.Fingerprint)
		if up == want || (len(want) >= 8 && strings.HasSuffix(up, want)) {
			return up, nil
		}
	}
	return "", fmt.Errorf("no secret key matching %q on this machine — run `nous identity list` to see candidates", input)
}

func showOrResolvePrimary(out io.Writer) error {
	// Stored / single-secret resolution first.
	if key, err := identity.Primary(); err == nil {
		fmt.Fprintf(out, "Primary identity: %s\n", key.Fingerprint)
		fmt.Fprintf(out, "  uid:    %s\n", displayUID(key))
		fmt.Fprintf(out, "  last-8: %s\n", key.Last8())
		statePath, _ := identity.PrimaryStatePath()
		if _, statErr := os.Stat(statePath); statErr == nil {
			fmt.Fprintf(out, "  source: %s\n", statePath)
		} else {
			fmt.Fprintln(out, "  source: implicit (only one secret key in keyring; run `nous identity primary <FP>` to make persistent)")
		}
		return nil
	} else if err != identity.ErrPrimaryUnset {
		// Stale state, gpg outage, etc. Surface and bail rather than
		// silently fall into the heuristic — the operator should know
		// the stored state is broken.
		return err
	}

	// Heuristic: scan brains for a private-recipient hint.
	candidate, hint, err := resolvePrimaryHeuristic()
	if err != nil {
		return err
	}
	if candidate == "" {
		return printAmbiguousAndExit(out)
	}
	fmt.Fprintf(out, "Heuristic primary candidate: %s\n", candidate)
	fmt.Fprintf(out, "  reason: %s\n", hint)
	fmt.Fprintln(out)
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		fmt.Fprintln(out, "(non-TTY: not persisting. Run interactively to confirm, or pass the fingerprint explicitly to persist.)")
		return nil
	}
	if !confirmPersist(out) {
		fmt.Fprintln(out, "Skipped persistence. Re-run with the fingerprint to make it sticky.")
		return nil
	}
	if err := identity.SetPrimary(candidate); err != nil {
		return err
	}
	statePath, _ := identity.PrimaryStatePath()
	fmt.Fprintf(out, "Persisted to %s.\n", statePath)
	return nil
}

// resolvePrimaryHeuristic looks for a private brain (single recipient)
// whose recipient is also a local secret key. Returns the matching
// fingerprint + a human-readable hint, or ("", "", nil) if none match.
func resolvePrimaryHeuristic() (fp, hint string, err error) {
	secret, err := identity.List()
	if err != nil {
		return "", "", err
	}
	if len(secret) == 0 {
		return "", "", nil
	}
	secretSet := map[string]bool{}
	for _, k := range secret {
		secretSet[strings.ToUpper(k.Fingerprint)] = true
	}
	manifests, err := brain.DiscoverAll()
	if err != nil {
		return "", "", err
	}
	for _, m := range manifests {
		if len(m.Recipients) != 1 {
			continue
		}
		fpU := strings.ToUpper(m.Recipients[0])
		if secretSet[fpU] {
			return fpU, fmt.Sprintf("private brain %s has this key as its sole recipient", m.Path), nil
		}
	}
	return "", "", nil
}

func printAmbiguousAndExit(out io.Writer) error {
	keys, err := identity.List()
	if err != nil {
		return err
	}
	fmt.Fprintln(out, "Primary identity unset; multiple local secret keys present and no private-brain hint:")
	for _, k := range keys {
		fmt.Fprintf(out, "  %s  %s\n", k.Last8(), displayUID(k))
	}
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Pick one with: nous identity primary <FP>")
	return nil
}

func confirmPersist(out io.Writer) bool {
	fmt.Fprint(out, "Persist this as the primary identity? [Y/n] ")
	var line string
	fmt.Fscanln(os.Stdin, &line)
	v := strings.ToLower(strings.TrimSpace(line))
	return v == "" || v == "y" || v == "yes"
}
