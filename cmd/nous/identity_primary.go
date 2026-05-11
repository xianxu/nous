// nous identity primary — show or set the operator's primary identity.
//
// Audience: (b). TTY → heuristic + interactive persist confirm.
// Non-TTY → emits a single canonical `primary: <fp> [source]` line
// so agents can parse the resolved value without falling over on
// the verbose human-prose branch.
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
// Audience: (b). Interactive on a TTY (heuristic + persist confirm);
// machine-stable single-line output on non-TTY so agents can parse
// the resolved value reliably.

package main

import (
	"bufio"
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

Audience: (b). TTY runs the heuristic + persist confirm; non-TTY emits
a single machine-stable line.`,
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
	isTTY := term.IsTerminal(int(os.Stdin.Fd()))

	// Stored / single-secret resolution first.
	if key, err := identity.Primary(); err == nil {
		return emitResolvedPrimary(out, key, sourceForPrimary(), isTTY)
	} else if err != identity.ErrPrimaryUnset {
		// Stale state, gpg outage, etc. Surface and bail rather than
		// silently fall into the heuristic — the operator should know
		// the stored state is broken.
		return err
	}

	// Heuristic. Shared with lib/brain.Annotator's fallback so the
	// CLI and the TUI can't disagree about which key is "(self)".
	candidate, hint, err := brain.HeuristicPrimary()
	if err != nil {
		return err
	}
	if candidate == "" {
		return printAmbiguousAndExit(out, isTTY)
	}
	if !isTTY {
		// Machine-stable single line. Agents parsing this see exactly
		// one shape regardless of whether resolution went stored vs.
		// heuristic. The verbose prompt-to-persist branch is TTY-only.
		fmt.Fprintf(out, "primary: %s (heuristic; %s)\n", candidate, hint)
		return nil
	}
	fmt.Fprintf(out, "Heuristic primary candidate: %s\n", candidate)
	fmt.Fprintf(out, "  reason: %s\n", hint)
	fmt.Fprintln(out)
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

// emitResolvedPrimary prints the resolved primary identity. Verbose
// human prose on a TTY; single canonical line on non-TTY for agent
// consumption.
func emitResolvedPrimary(out io.Writer, key identity.Key, source string, isTTY bool) error {
	if !isTTY {
		fmt.Fprintf(out, "primary: %s (%s)\n", key.Fingerprint, source)
		return nil
	}
	fmt.Fprintf(out, "Primary identity: %s\n", key.Fingerprint)
	fmt.Fprintf(out, "  uid:    %s\n", displayUID(key))
	fmt.Fprintf(out, "  last-8: %s\n", key.Last8())
	if source == "stored" {
		statePath, _ := identity.PrimaryStatePath()
		fmt.Fprintf(out, "  source: %s\n", statePath)
	} else {
		fmt.Fprintln(out, "  source: implicit (only one secret key in keyring; run `nous identity primary <FP>` to make persistent)")
	}
	return nil
}

// sourceForPrimary returns "stored" if the state file exists,
// "implicit" otherwise. Read-only probe; no side effects.
func sourceForPrimary() string {
	statePath, err := identity.PrimaryStatePath()
	if err != nil {
		return "implicit"
	}
	if _, err := os.Stat(statePath); err == nil {
		return "stored"
	}
	return "implicit"
}

func printAmbiguousAndExit(out io.Writer, isTTY bool) error {
	keys, err := identity.List()
	if err != nil {
		return err
	}
	if !isTTY {
		// Machine-stable: one line per candidate, prefix marks "unset".
		fmt.Fprintln(out, "primary: unset")
		for _, k := range keys {
			fmt.Fprintf(out, "candidate: %s  %s\n", k.Fingerprint, displayUID(k))
		}
		return nil
	}
	fmt.Fprintln(out, "Primary identity unset; multiple local secret keys present and no private-brain hint:")
	for _, k := range keys {
		fmt.Fprintf(out, "  %s  %s\n", k.Last8(), displayUID(k))
	}
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Pick one with: nous identity primary <FP>")
	return nil
}

// confirmPersist prompts the operator. Empty input / EOF / any
// non-y/yes input → decline. Explicitly NOT default-yes: the M5
// review caught that fmt.Fscanln returns empty string on EOF, and
// default-yes would silently persist a heuristic candidate the
// operator never confirmed (a TTY-attached stdin closed via ctrl+d
// would do this in practice).
func confirmPersist(out io.Writer) bool {
	fmt.Fprint(out, "Persist this as the primary identity? [y/N] ")
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil && line == "" {
		return false
	}
	v := strings.ToLower(strings.TrimSpace(line))
	return v == "y" || v == "yes"
}
