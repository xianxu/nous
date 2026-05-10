// nous identity cluster: keypair generation (init), sneakernet
// export/import (with the verify-fingerprint ceremony on import), the
// joined keyring × brains list view, and gpg-agent lifecycle (prewarm /
// flush / status — wraps lib/agent/).
//
// Audience tags:
//   - identity init   (h)  TTY-only — guides the operator through keygen
//   - identity export (a)  prints armored pubkey to stdout, scriptable
//   - identity import (h)  TTY-only — verify-fingerprint ceremony
//   - identity list   (a)  scriptable joined view
//   - identity agent  (a)  scriptable agent ops
//
// Per nous#14 spec: identity ops use interactive CLI prompts, not a
// full-screen TUI — the surface is small and human-driven, not browse-
// and-act.
package main

import (
	"bufio"
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

func newIdentityCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "identity",
		Short: "GPG keys + agent + peer pubkeys",
		Long: `Identity cluster — keypair generation (TTY-only), sneakernet
export/import with verify-fingerprint ceremony, joined keyring×brains
list, and gpg-agent lifecycle.

Subcommands:
  init             generate a new GPG keypair (TTY-only)
  export [FP]      print armored public key to stdout (default: own primary key)
  import FILE      admit a peer's pubkey (TTY-only; verify-fingerprint ceremony)
  list             list local keys joined with brain recipient assignments
  agent <verb>     gpg-agent lifecycle: prewarm | flush | status`,
		Args: cobra.NoArgs,
	}
	cmd.AddCommand(
		newIdentityInitCmd(),
		newIdentityExportCmd(),
		newIdentityImportCmd(),
		newIdentityListCmd(),
		newIdentityAgentCmd(),
	)
	return cmd
}

// ─── list ─────────────────────────────────────────────────────────────

func newIdentityListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List GPG keys joined with brain recipient assignments",
		Long: `List GPG keys in the local keyring, annotated with which brains
list each key as a recipient. Secret keys (your own identities) and
peer public keys are listed separately.

Brain discovery: $WORKSPACE_ROOT (or $HOME/workspace) one level deep,
matching nous brain-sync's convention.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runIdentityList(cmd.OutOrStdout())
		},
	}
}

func runIdentityList(w io.Writer) error {
	secret, err := identity.List()
	if err != nil {
		return err
	}
	pub, err := identity.ListPublic()
	if err != nil {
		return err
	}
	brains, err := brain.DiscoverAll()
	if err != nil {
		// Brain discovery is non-fatal — operator can still see keys
		// if WORKSPACE_ROOT is unreadable. Surface as a warning.
		fmt.Fprintf(os.Stderr, "warning: brain discovery failed: %v\n", err)
	}

	fmt.Fprintln(w, "Secret keys (own identities):")
	if len(secret) == 0 {
		fmt.Fprintln(w, "  (none — run `nous identity init` to create one)")
	}
	for _, k := range secret {
		fmt.Fprintf(w, "  %s  %s  %s\n", k.Last8(), keyBrains(k, brains), displayUID(k))
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Public keys (peers admitted to brains):")
	if len(pub) == 0 {
		fmt.Fprintln(w, "  (none — peer pubkeys appear here after `nous identity import`)")
	}
	for _, k := range pub {
		fmt.Fprintf(w, "  %s  %s  %s\n", k.Last8(), keyBrains(k, brains), displayUID(k))
	}
	return nil
}

// keyBrains returns the brain names that list this key as a recipient.
// Format: "[brain1, brain2]" or "" when the key isn't admitted anywhere.
// Matches case-insensitive on the full fingerprint since manifests
// occasionally use lowercase.
func keyBrains(k identity.Key, brains []brain.Manifest) string {
	fp := strings.ToUpper(k.Fingerprint)
	var names []string
	for _, b := range brains {
		for _, r := range b.Recipients {
			if strings.ToUpper(r) == fp {
				name := b.Name
				if name == "" {
					name = filepath.Base(b.Path)
				}
				names = append(names, name)
				break
			}
		}
	}
	if len(names) == 0 {
		return "(no brain)"
	}
	return "[" + strings.Join(names, ", ") + "]"
}

func displayUID(k identity.Key) string {
	if k.UID != "" {
		return k.UID
	}
	return "(no UID)"
}

// ─── export ───────────────────────────────────────────────────────────

func newIdentityExportCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "export [FINGERPRINT]",
		Short: "Print armored public key to stdout (default: own primary key)",
		Long: `Print the armored public key to stdout. With no argument, exports
the operator's primary secret key (errors if multiple secret keys
exist — pass the fingerprint explicitly to disambiguate).

Pipe into a peer's nous identity import to admit them to a brain:

  nous identity export | ssh peer 'nous identity import /dev/stdin'

Or save and sneakernet:

  nous identity export > ~/Desktop/me.pub`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var fp string
			if len(args) == 1 {
				fp = args[0]
			} else {
				keys, err := identity.List()
				if err != nil {
					return err
				}
				if len(keys) == 0 {
					return fmt.Errorf("no secret key in keyring; run `nous identity init` first")
				}
				if len(keys) > 1 {
					return fmt.Errorf("multiple secret keys in keyring; pass the fingerprint explicitly:\n  nous identity export <FINGERPRINT>")
				}
				fp = keys[0].Fingerprint
			}
			armor, err := identity.Export(fp)
			if err != nil {
				return err
			}
			_, err = io.WriteString(cmd.OutOrStdout(), armor)
			return err
		},
	}
}

// ─── import (TTY-only + verify-fingerprint ceremony) ──────────────────

func newIdentityImportCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "import FILE",
		Short: "Admit a peer's public key (TTY-only; verify-fingerprint ceremony)",
		Long: `Read an armored public key, show its fingerprint and UID, prompt
the operator to type the last 8 hex chars of the fingerprint for
confirmation, then commit it to the local keyring.

The verify-fingerprint ceremony catches a class of attacks where an
attacker substitutes their own pubkey before the import — the operator
should have received the expected last-8 out of band (phone, in person,
signed message) and notice the mismatch.

TTY-only: refuses to run when stdin is not a terminal. Identity-
admission is a delegation boundary; agents on the device cannot
silently expand their own access by importing peer keys (see
brain/atlas/threat-model-shared-brain.md).

  nous identity import wife.pub
  nous identity import -            # read from stdin (still requires TTY for the prompt)`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !term.IsTerminal(int(os.Stdin.Fd())) {
				return fmt.Errorf("nous identity import requires an interactive terminal (TTY-only safeguard)")
			}
			path := args[0]
			var data []byte
			var err error
			if path == "-" {
				data, err = io.ReadAll(os.Stdin)
			} else {
				data, err = os.ReadFile(path)
			}
			if err != nil {
				return fmt.Errorf("read pubkey: %w", err)
			}
			armor := string(data)

			// Inspect first — show what's about to be admitted.
			peer, err := identity.Inspect(armor)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Pubkey to admit:\n")
			fmt.Fprintf(out, "  fingerprint: %s\n", peer.Fingerprint)
			fmt.Fprintf(out, "  last-8:      %s\n", peer.Last8())
			fmt.Fprintf(out, "  uid:         %s\n", displayUID(peer))
			fmt.Fprintln(out)
			fmt.Fprintln(out, "Verify the last 8 hex chars match what the peer sent you OUT OF BAND")
			fmt.Fprintln(out, "(phone, in-person, signed message — NOT the same channel as the pubkey).")
			fmt.Fprintln(out)

			expected := peer.Last8()
			if err := promptVerify(cmd.InOrStdin(), out, expected); err != nil {
				return err
			}

			if _, err := identity.Import(armor); err != nil {
				return err
			}
			fmt.Fprintf(out, "Imported %s.\n", peer.Last8())
			return nil
		},
	}
}

// promptVerify reads up to 3 attempts at the last-8 fingerprint. Match
// is case-insensitive (humans don't preserve case across phone/sms).
func promptVerify(in io.Reader, out io.Writer, expected string) error {
	r := bufio.NewReader(in)
	for attempt := 1; attempt <= 3; attempt++ {
		fmt.Fprintf(out, "Type the last-8 to confirm (attempt %d/3): ", attempt)
		line, err := r.ReadString('\n')
		if err != nil {
			return fmt.Errorf("read confirmation: %w", err)
		}
		got := strings.ToLower(strings.TrimSpace(line))
		if got == strings.ToLower(expected) {
			return nil
		}
		fmt.Fprintln(out, "  mismatch — try again, or Ctrl-C to abort.")
	}
	return fmt.Errorf("fingerprint mismatch after 3 attempts; aborting import")
}

// ─── init ─────────────────────────────────────────────────────────────

func newIdentityInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Generate a new GPG keypair (TTY-only)",
		Long: `Generate a new GPG keypair for brain encryption. Idempotent: if a
key already exists in the keyring, prints the existing fingerprint and
exits without re-generating.

Currently delegates to scripts/identity.sh under the hood — that script
encodes 200 lines of macOS gpg-agent + pinentry-mac configuration that
isn't worth re-porting until the surface stabilizes. The script itself
is TTY-aware; this command adds the explicit TTY check and bails early
with a clear error.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !term.IsTerminal(int(os.Stdin.Fd())) {
				return fmt.Errorf("nous identity init requires an interactive terminal (TTY-only safeguard)")
			}
			scriptPath, err := findIdentityScript()
			if err != nil {
				return err
			}
			c := exec.Command("bash", scriptPath)
			c.Stdin = os.Stdin
			c.Stdout = os.Stdout
			c.Stderr = os.Stderr
			return c.Run()
		},
	}
}

// findIdentityScript locates scripts/identity.sh relative to the nous
// install. Looks at $NOUS_DIR first (set by Makefile); falls back to a
// repo-relative path when run from a working tree.
func findIdentityScript() (string, error) {
	if dir := os.Getenv("NOUS_DIR"); dir != "" {
		p := filepath.Join(dir, "scripts", "identity.sh")
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	// Last resort: assume we're running from the repo root.
	if p, err := filepath.Abs("scripts/identity.sh"); err == nil {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("scripts/identity.sh not found; set $NOUS_DIR to the nous repo root")
}

// ─── agent ────────────────────────────────────────────────────────────

func newIdentityAgentCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agent",
		Short: "gpg-agent lifecycle: prewarm | flush | status",
		Long: `Manage gpg-agent's passphrase cache for the operator's key:

  prewarm   present the passphrase to gpg-agent (so subsequent gcrypt /
            vault ops within the cache TTL don't prompt).
  flush     wipe all cached passphrases (security hygiene at session end).
  status    enumerate cached keygrips + their TTLs.

Wraps lib/agent. The passphrase comes from the macOS login Keychain
(item name "nous-identity"); if missing, prewarm prompts via pinentry.`,
		Args: cobra.NoArgs,
	}
	cmd.AddCommand(
		newIdentityAgentPrewarmCmd(),
		newIdentityAgentFlushCmd(),
		newIdentityAgentStatusCmd(),
	)
	return cmd
}

func newIdentityAgentPrewarmCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "prewarm",
		Short: "Push the GPG passphrase into gpg-agent's cache",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("agent prewarm: lib/agent prewarm primitive not yet implemented (nous#14 M4a follow-up; charon#21 M2)")
		},
	}
}

func newIdentityAgentFlushCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "flush",
		Short: "Wipe gpg-agent's passphrase cache (reloadagent)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c := exec.Command("gpg-connect-agent", "reloadagent", "/bye")
			c.Stdout = cmd.OutOrStdout()
			c.Stderr = cmd.ErrOrStderr()
			return c.Run()
		},
	}
}

func newIdentityAgentStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show which keygrips are cached + cache TTLs",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c := exec.Command("gpg-connect-agent", "KEYINFO --list", "/bye")
			c.Stdout = cmd.OutOrStdout()
			c.Stderr = cmd.ErrOrStderr()
			return c.Run()
		},
	}
}
