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
	"time"

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
  primary [FP]     show or set the operator's primary identity
  agent <verb>     gpg-agent lifecycle: prewarm | flush | status`,
		Args: cobra.NoArgs,
	}
	cmd.AddCommand(
		newIdentityInitCmd(),
		newIdentityExportCmd(),
		newIdentityImportCmd(),
		newIdentityListCmd(),
		newIdentityPrimaryCmd(),
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
	// Build fp → github-user map once; cheaper than re-loading per peer.
	// Errors loading the peer store aren't fatal for listing — fall
	// through with an empty map and print peers without the github tag.
	githubByFP := map[string]string{}
	if metas, err := identity.ListPeerMeta(); err == nil {
		for _, m := range metas {
			if m.GithubUser != "" {
				githubByFP[strings.ToUpper(m.Fingerprint)] = m.GithubUser
			}
		}
	}
	for _, k := range pub {
		gh := ""
		if u := githubByFP[strings.ToUpper(k.Fingerprint)]; u != "" {
			gh = fmt.Sprintf(" (github:%s)", u)
		}
		fmt.Fprintf(w, "  %s  %s  %s%s\n", k.Last8(), keyBrains(k, brains), displayUID(k), gh)
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
		Use:   "import [FILE]",
		Short: "Admit a peer's public key (TTY-only; verify-fingerprint ceremony)",
		Long: `Read an armored public key, show its fingerprint and UID, prompt
the operator to type the last 8 hex chars of the fingerprint for
confirmation, prompt for the peer's GitHub username (required — used
by 'nous brain share' to add them as a collaborator on the brain's
gcrypt remote), then commit the pubkey to the local keyring and the
peer metadata to ~/.config/nous/peers/<fp>.json.

If FILE is omitted, scan the current directory for *.pub files: with
exactly one, use it; with multiple, prompt the operator to pick; with
none, prompt for the path.

The verify-fingerprint ceremony catches a class of attacks where an
attacker substitutes their own pubkey before the import — the operator
should have received the expected last-8 out of band (phone, in person,
signed message) and notice the mismatch.

TTY-only: refuses to run when stdin is not a terminal. Identity-
admission is a delegation boundary; agents on the device cannot
silently expand their own access by importing peer keys (see
brain/atlas/threat-model-shared-brain.md).

  nous identity import wife.pub
  nous identity import              # auto-detect *.pub in current dir
  nous identity import -            # read from stdin (still requires TTY for the prompt)`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !term.IsTerminal(int(os.Stdin.Fd())) {
				return fmt.Errorf("nous identity import requires an interactive terminal (TTY-only safeguard)")
			}
			out := cmd.OutOrStdout()
			in := cmd.InOrStdin()

			path := ""
			if len(args) == 1 {
				path = args[0]
			} else {
				resolved, err := resolvePubFile(in, out)
				if err != nil {
					return err
				}
				path = resolved
			}

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
			fmt.Fprintf(out, "Pubkey to admit:\n")
			fmt.Fprintf(out, "  fingerprint: %s\n", peer.Fingerprint)
			fmt.Fprintf(out, "  last-8:      %s\n", peer.Last8())
			fmt.Fprintf(out, "  uid:         %s\n", displayUID(peer))
			fmt.Fprintln(out)
			fmt.Fprintln(out, "Verify the last 8 hex chars match what the peer sent you OUT OF BAND")
			fmt.Fprintln(out, "(phone, in-person, signed message — NOT the same channel as the pubkey).")
			fmt.Fprintln(out)

			expected := peer.Last8()
			if err := promptVerify(in, out, expected); err != nil {
				return err
			}

			// GitHub username is required: every brain-recipient also
			// needs to be added as a collaborator on the brain's GitHub
			// repo (the gcrypt transit layer uses GitHub's identity
			// system, not GPG's). Capture it now so 'nous brain share'
			// has both halves without a second prompt.
			githubUser, err := promptGithubUser(in, out)
			if err != nil {
				return err
			}

			if _, err := identity.Import(armor); err != nil {
				return err
			}
			if err := identity.SavePeerMeta(identity.PeerMeta{
				Fingerprint: peer.Fingerprint,
				GithubUser:  githubUser,
				ImportedAt:  time.Now().UTC(),
			}); err != nil {
				// Pubkey landed in keyring but sidecar write failed —
				// surface the partial state so the operator can re-run
				// `nous identity peer set` (or the upcoming `share`) to
				// backfill rather than silently dropping the github user.
				fmt.Fprintf(out, "Imported %s (pubkey in keyring), but failed to save peer metadata: %v\n", peer.Last8(), err)
				return err
			}
			fmt.Fprintf(out, "Imported %s with github user %q.\n", peer.Last8(), githubUser)
			return nil
		},
	}
}

// resolvePubFile picks a pubkey file path interactively when the
// operator didn't pass one. Scans CWD for *.pub:
//   - 0 matches → prompt for the path
//   - 1 match   → use it (with confirmation)
//   - N matches → present numbered list, prompt for selection
func resolvePubFile(in io.Reader, out io.Writer) (string, error) {
	matches, err := filepath.Glob("*.pub")
	if err != nil {
		return "", fmt.Errorf("scan current directory: %w", err)
	}
	r := bufio.NewReader(in)
	switch len(matches) {
	case 0:
		fmt.Fprintln(out, "No .pub files found in current directory.")
		fmt.Fprint(out, "Pubkey file path: ")
		line, err := r.ReadString('\n')
		if err != nil {
			return "", fmt.Errorf("read pubkey path: %w", err)
		}
		path := strings.TrimSpace(line)
		if path == "" {
			return "", fmt.Errorf("no pubkey file provided")
		}
		return path, nil
	case 1:
		fmt.Fprintf(out, "Found %s in current directory. Use it? [Y/n]: ", matches[0])
		line, err := r.ReadString('\n')
		if err != nil {
			return "", fmt.Errorf("read confirmation: %w", err)
		}
		ans := strings.ToLower(strings.TrimSpace(line))
		if ans == "" || ans == "y" || ans == "yes" {
			return matches[0], nil
		}
		return "", fmt.Errorf("aborted; pass the pubkey file path explicitly")
	default:
		fmt.Fprintln(out, "Found multiple .pub files in current directory:")
		for i, m := range matches {
			fmt.Fprintf(out, "  [%d] %s\n", i+1, m)
		}
		fmt.Fprintf(out, "Select [1-%d]: ", len(matches))
		line, err := r.ReadString('\n')
		if err != nil {
			return "", fmt.Errorf("read selection: %w", err)
		}
		var idx int
		if _, err := fmt.Sscanf(strings.TrimSpace(line), "%d", &idx); err != nil {
			return "", fmt.Errorf("invalid selection %q", strings.TrimSpace(line))
		}
		if idx < 1 || idx > len(matches) {
			return "", fmt.Errorf("selection %d out of range [1-%d]", idx, len(matches))
		}
		return matches[idx-1], nil
	}
}

// promptGithubUser reads a GitHub username with up to 3 attempts at
// passing basic format validation (non-empty, GitHub's character + length
// rules). Doesn't verify the user exists on GitHub — that'd require a
// network call; let `nous brain share` catch typos when it tries to add
// the collaborator.
func promptGithubUser(in io.Reader, out io.Writer) (string, error) {
	r := bufio.NewReader(in)
	for attempt := 1; attempt <= 3; attempt++ {
		fmt.Fprint(out, "GitHub username for this peer (used by 'nous brain share'): ")
		line, err := r.ReadString('\n')
		if err != nil {
			return "", fmt.Errorf("read github username: %w", err)
		}
		user := strings.TrimSpace(line)
		if err := validateGithubUser(user); err == nil {
			return user, nil
		} else {
			fmt.Fprintf(out, "  %v — try again, or Ctrl-C to abort.\n", err)
		}
	}
	return "", fmt.Errorf("github username invalid after 3 attempts; aborting import")
}

// validateGithubUser applies GitHub's documented username rules:
// 1-39 chars, alphanumeric + single hyphens, no leading/trailing
// hyphen, no consecutive hyphens. Permissive enough that legitimate
// usernames pass; strict enough that obvious typos / pasted lines /
// emails get caught at prompt time rather than at gh-api time.
func validateGithubUser(s string) error {
	if s == "" {
		return fmt.Errorf("empty")
	}
	if len(s) > 39 {
		return fmt.Errorf("too long (max 39 chars)")
	}
	if s[0] == '-' || s[len(s)-1] == '-' {
		return fmt.Errorf("cannot start or end with hyphen")
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c == '-' || (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')) {
			return fmt.Errorf("contains invalid character %q (only alphanumeric and hyphens allowed)", c)
		}
		if c == '-' && i+1 < len(s) && s[i+1] == '-' {
			return fmt.Errorf("consecutive hyphens not allowed")
		}
	}
	return nil
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
