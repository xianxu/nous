// nous security cluster: macOS hygiene audit (`check`), per-finding
// remediation lookup (`remedy`), and the runtime-consent menubar agent
// (`menubar`). Ported from the standalone cmd/nous-security/ binary
// per nous#22 — same lib/security/* implementation, same flags, same
// output, just hosted inside nous.
//
// Audience tags:
//   - security check    (h) audits the host; prints findings to stderr.
//   - security remedy   (h) prints remediation steps for a finding ID.
//   - security menubar  (h) UI surface; arms/disarms via the proxy socket.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"github.com/spf13/cobra"

	"github.com/xianxu/nous/lib/security"
)

// securityFlags scopes the shared `nous security` flag set to a
// closure rather than package globals — matches the convention the
// rest of cmd/nous uses, and avoids polluting the package's namespace
// with names like `flagNoColor` that other clusters might collide on.
type securityFlags struct {
	noTCC      bool
	noColor    bool
	forceColor bool
	json       bool
	strict     bool
	yes        bool
}

func newSecurityCmd() *cobra.Command {
	flags := &securityFlags{}

	cmd := &cobra.Command{
		Use:   "security",
		Short: "macOS hygiene audit + runtime-consent menubar",
		Long: `nous security has three modes:

  check    — audit macOS hygiene assumptions charon's threat model
             relies on (SIP, TCC grants, keychain ACL).
  remedy   — print remediation steps for a finding ID.
  menubar  — run as a menubar agent that arms/disarms the proxy's
             runtime-consent gate.

See atlas/ + brain/atlas/threat-model-shared-brain.md for context.`,
		Args: cobra.NoArgs,
	}
	cmd.PersistentFlags().BoolVar(&flags.noColor, "no-color", false, "disable colored output")
	cmd.PersistentFlags().BoolVar(&flags.forceColor, "force-color", false,
		"force colored output even when stdout/stderr isn't a TTY (used when "+
			"output is routed through a tempfile, as `open -W` does for .app launches)")
	cmd.PersistentFlags().BoolVar(&flags.json, "json", false,
		"emit findings as JSON (overrides text output)")

	cmd.AddCommand(newSecurityCheckCmd(flags))
	cmd.AddCommand(newSecurityRemedyCmd(flags))
	cmd.AddCommand(newSecurityMenubarCmd())
	return cmd
}

func newSecurityCheckCmd(flags *securityFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "check",
		Short: "Run the audit and report findings",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSecurityCheck(flags)
		},
	}
	cmd.Flags().BoolVar(&flags.noTCC, "no-tcc", false,
		"skip TCC.db reads (no FDA needed); fall back to manual System Settings walk")
	cmd.Flags().BoolVar(&flags.strict, "strict", false,
		"promote every severity tier up by one before exit-code rollup")
	cmd.Flags().BoolVar(&flags.yes, "yes", false,
		"skip the pre-flight consent gate (for non-interactive runs)")
	return cmd
}

func newSecurityRemedyCmd(flags *securityFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "remedy [finding-id]",
		Short: "Print remediation steps (all findings, or one by ID)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSecurityRemedy(flags, args)
		},
	}
}

func runSecurityCheck(flags *securityFlags) error {
	// Lipgloss/glamour decide whether to emit ANSI based on termenv's
	// TTY detection. .app bundles launched via `open` route output
	// through a tempfile, defeating that detection — --force-color is
	// the override.
	if flags.forceColor {
		lipgloss.SetColorProfile(termenv.ANSI256)
	}

	self, err := security.LoadSelfInfo()
	if err != nil {
		return fmt.Errorf("inspect self: %w", err)
	}

	opts := security.PreflightOptions{
		WillReadTCC:      !flags.noTCC,
		WillCheckCharon:  true,
		WillPromptRevoke: false,
	}
	security.PrintPreflight(os.Stderr, self, opts)

	if flags.yes {
		fmt.Fprintln(os.Stderr, "(--yes specified, skipping consent gate)")
	} else {
		if !security.ConfirmDefaultDeny("Continue with the audit?") {
			fmt.Fprintln(os.Stderr, "aborted.")
			return nil
		}
	}

	report := security.Report{}
	report.Findings = append(report.Findings, security.CheckSIP()...)
	report.MarkEvaluated(security.BarSIP)

	report.Findings = append(report.Findings, security.CheckSudoCache()...)
	report.MarkEvaluated(security.BarSudoCache)

	report.Findings = append(report.Findings, security.CheckLaunchdAgents()...)
	report.MarkEvaluated(security.BarLaunchdPersistence)

	apps := security.DetectInstalledApps()
	fmt.Fprintf(os.Stderr, "\nDetected %d known terminals/editors/IDEs:\n", len(apps))
	for _, a := range apps {
		fmt.Fprintf(os.Stderr, "  %-30s %s  (%s)\n", a.BundleID, a.Path, a.Category)
	}
	report.Findings = append(report.Findings, security.CheckCodesignEntitlements(apps)...)

	if !flags.noTCC {
		tccFindings := security.CheckTCC(apps)
		report.Findings = append(report.Findings, tccFindings...)
		// Mark items 2–5 evaluated only if TCC.db was actually readable.
		// The "tcc-no-fda-*" finding signals the read failed; in that
		// case leave 2–5 as Skipped so the user knows the audit is
		// incomplete.
		if !sawNoFDA(tccFindings) {
			report.MarkEvaluated(
				security.BarTerminalFDA,
				security.BarTerminalA11y,
				security.BarTerminalScreen,
				security.BarTerminalEvents,
			)
		}
		offerFDAGrantIfNeeded(flags, tccFindings, self)
	}

	report.Findings = append(report.Findings, security.CheckCharonKeychainACLs()...)
	report.MarkEvaluated(security.BarKeychainEntries)

	report.Findings = append(report.Findings, security.CheckCharonSigningKeyACL()...)
	report.MarkEvaluated(security.BarSigningKeyACL)

	report.Findings = append(report.Findings, security.CheckCharonBinary()...)
	report.MarkEvaluated(security.BarCharonBinary)

	report.Findings = append(report.Findings, security.CheckFileVault()...)
	report.MarkEvaluated(security.BarFileVault)

	report.Findings = append(report.Findings, security.CheckTimeMachine()...)
	report.MarkEvaluated(security.BarTimeMachine)

	if flags.noTCC {
		if !flags.yes && security.IsInteractive() {
			security.RunVisualWalk(os.Stderr)
		} else {
			fmt.Fprintln(os.Stderr,
				"(skipping visual TCC walk; re-run interactively without --yes for the System Settings audit)")
		}
	}

	if flags.strict {
		// Promote every finding's severity by one before rollup.
		for i := range report.Findings {
			if report.Findings[i].Severity < security.SevCritical {
				report.Findings[i].Severity++
			}
		}
	}

	out := os.Stderr
	if flags.json {
		out = os.Stdout
	}
	if err := report.Print(out, security.PrintOptions{
		NoColor:    flags.noColor,
		ForceColor: flags.forceColor,
		JSON:       flags.json,
	}); err != nil {
		return err
	}

	os.Exit(report.ExitCode())
	return nil
}

// sawNoFDA reports whether the TCC check returned a "couldn't read
// TCC.db" signal. Used to decide whether bar items 2–5 should be
// marked Evaluated or left as Skipped.
func sawNoFDA(findings []security.Finding) bool {
	for _, f := range findings {
		if strings.HasPrefix(f.ID, "tcc-no-fda-") {
			return true
		}
	}
	return false
}

// offerFDAGrantIfNeeded looks for the tcc-no-fda-* findings produced
// by CheckTCC and, when running interactively, walks the user through
// adding the .app to the FDA pane. No-op on --yes (non-interactive)
// or when running outside a .app bundle.
func offerFDAGrantIfNeeded(flags *securityFlags, findings []security.Finding, self security.SelfInfo) {
	needsFDA := false
	for _, f := range findings {
		if strings.HasPrefix(f.ID, "tcc-no-fda-") {
			needsFDA = true
			break
		}
	}
	if !needsFDA || flags.yes || !security.IsInteractive() {
		return
	}
	if self.BundleID == "" {
		fmt.Fprintln(os.Stderr,
			"\nNote: running outside a .app bundle. Granting FDA now would attach to your terminal, "+
				"not to nous. Wrap `nous security check` in a signed .app to get proper TCC attribution.")
		return
	}
	fmt.Fprintf(os.Stderr, "\nFull Disk Access not granted to %s.\n", self.BundleID)
	if !security.ConfirmDefaultYes("Open System Settings → Full Disk Access now?") {
		return
	}
	if err := exec.Command("open",
		"x-apple.systempreferences:com.apple.preference.security?Privacy_AllFiles").Run(); err != nil {
		fmt.Fprintf(os.Stderr, "(could not open System Settings: %v)\n", err)
		return
	}
	if self.BundlePath != "" {
		// Reveal the .app in Finder so the user can drag-drop.
		_ = exec.Command("open", "-R", self.BundlePath).Run()
	}
	fmt.Fprintln(os.Stderr, "\nIn the System Settings pane:")
	fmt.Fprintln(os.Stderr, "  1. Drag the .app from Finder into the list, OR click + and pick it.")
	fmt.Fprintln(os.Stderr, "  2. Toggle the switch ON.")
	fmt.Fprintln(os.Stderr, "  3. Re-run `nous security check` to read TCC.db.")
}

func runSecurityRemedy(flags *securityFlags, args []string) error {
	opts := security.RenderOptions{NoColor: flags.noColor}
	if len(args) == 0 {
		security.PrintAllRemedies(os.Stdout, opts)
		return nil
	}
	ref := args[0]
	entry := security.LookupRemedy(ref)
	if entry == nil {
		security.PrintUnknownRef(os.Stderr, ref)
		os.Exit(1)
	}
	security.PrintRemedy(os.Stdout, entry, opts)
	return nil
}
