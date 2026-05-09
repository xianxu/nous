// Command nous-security audits a personal Mac for the hygiene
// baseline charon's threat model assumes (see docs/threat-model.md):
// SIP enabled, no TCC grants on terminals/IDEs, no suspicious launchd
// agents, charon's keychain ACLs intact.
//
// Designed to be packaged as Charon Security.app so TCC attributes
// permissions to com.charon.security specifically; run from
// `make security` after `make security-install`.
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

var (
	flagNoTCC      bool
	flagNoColor    bool
	flagForceColor bool
	flagJSON       bool
	flagStrict     bool
	flagYes        bool
)

func main() {
	root := &cobra.Command{
		Use:   "nous-security",
		Short: "Audit macOS hygiene + run the runtime-consent menubar",
		Long: "Charon Security has two modes:\n\n" +
			"  check  — audit macOS hygiene assumptions charon's threat\n" +
			"           model relies on (SIP, TCC grants, keychain ACL).\n" +
			"  menubar — run as a menubar agent that arms/disarms the\n" +
			"            proxy's runtime-consent gate.\n\n" +
			"Default (no subcommand): launch menubar mode. The .app\n" +
			"bundle's LSUIElement=true setting keeps it dock-less.\n" +
			"See docs/threat-model.md.",
		// No-args default → menubar. The .app bundle launched via
		// Finder/launchd/`open` invokes the binary with no args; we
		// want that to mean "show the menubar item." Explicit
		// subcommands (check, remedy, menubar) all still work.
		Run: func(cmd *cobra.Command, args []string) {
			runMenubar()
		},
	}
	root.PersistentFlags().BoolVar(&flagNoColor, "no-color", false, "disable colored output")
	root.PersistentFlags().BoolVar(&flagForceColor, "force-color", false,
		"force colored output even when stdout/stderr isn't a TTY (used by `make security` whose `open -W` indirection redirects to a tempfile)")
	root.PersistentFlags().BoolVar(&flagJSON, "json", false, "emit findings as JSON (overrides text output)")

	root.AddCommand(checkCmd())
	root.AddCommand(remedyCmd())
	root.AddCommand(menubarCmd())

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func checkCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "check",
		Short: "Run the audit and report findings",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCheck()
		},
	}
	cmd.Flags().BoolVar(&flagNoTCC, "no-tcc", false,
		"skip TCC.db reads (no FDA needed); fall back to manual System Settings walk")
	cmd.Flags().BoolVar(&flagStrict, "strict", false,
		"promote every severity tier up by one before exit-code rollup")
	cmd.Flags().BoolVar(&flagYes, "yes", false,
		"skip the pre-flight consent gate (for non-interactive runs)")
	return cmd
}

func remedyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remedy [finding-id]",
		Short: "Print remediation steps (all findings, or one by ID)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRemedy(args)
		},
	}
}

func runCheck() error {
	// Lipgloss/glamour decide whether to emit ANSI based on
	// termenv's TTY detection. `make security` routes the
	// bundle's output through a tempfile (LaunchServices doesn't
	// pipe stdout to the calling terminal), so by default the
	// rendered output is uncolored even though we then `cat` it
	// back to a TTY. --force-color overrides termenv's verdict.
	if flagForceColor {
		lipgloss.SetColorProfile(termenv.ANSI256)
	}

	self, err := security.LoadSelfInfo()
	if err != nil {
		return fmt.Errorf("inspect self: %w", err)
	}

	opts := security.PreflightOptions{
		// Toggled on as M6 lands. Keeping these honest now is the
		// difference between a transparency block and a fairy tale.
		WillReadTCC:      !flagNoTCC,
		WillCheckCharon:  true,
		WillPromptRevoke: false,
	}
	security.PrintPreflight(os.Stderr, self, opts)

	if flagYes {
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

	if !flagNoTCC {
		tccFindings := security.CheckTCC(apps)
		report.Findings = append(report.Findings, tccFindings...)
		// Mark items 2–5 evaluated only if TCC.db was actually
		// readable. The "tcc-no-fda-*" finding signals the read
		// failed and we couldn't see the state; in that case we
		// leave 2–5 as Skipped so the user knows the audit is
		// incomplete.
		if !sawNoFDA(tccFindings) {
			report.MarkEvaluated(
				security.BarTerminalFDA,
				security.BarTerminalA11y,
				security.BarTerminalScreen,
				security.BarTerminalEvents,
			)
		}
		offerFDAGrantIfNeeded(tccFindings, self)
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

	if flagNoTCC {
		if !flagYes && security.IsInteractive() {
			security.RunVisualWalk(os.Stderr)
		} else {
			fmt.Fprintln(os.Stderr, "(skipping visual TCC walk; re-run interactively without --yes for the System Settings audit)")
		}
	}

	if flagStrict {
		// Promote every finding's severity by one before rollup.
		for i := range report.Findings {
			if report.Findings[i].Severity < security.SevCritical {
				report.Findings[i].Severity++
			}
		}
	}

	out := os.Stderr
	if flagJSON {
		out = os.Stdout
	}
	if err := report.Print(out, security.PrintOptions{
		NoColor:    flagNoColor,
		ForceColor: flagForceColor,
		JSON:       flagJSON,
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
// or when running outside a .app bundle (where granting FDA wouldn't
// be scoped to com.charon.security).
func offerFDAGrantIfNeeded(findings []security.Finding, self security.SelfInfo) {
	needsFDA := false
	for _, f := range findings {
		if strings.HasPrefix(f.ID, "tcc-no-fda-") {
			needsFDA = true
			break
		}
	}
	if !needsFDA || flagYes || !security.IsInteractive() {
		return
	}
	if self.BundleID == "" {
		fmt.Fprintln(os.Stderr, "\nNote: running outside a .app bundle. Granting FDA now would attach to your terminal, not to nous-security. Run via `make security` for proper TCC attribution.")
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
	fmt.Fprintln(os.Stderr, "  1. Drag \"Charon Security.app\" from Finder into the list, OR click + and pick it.")
	fmt.Fprintln(os.Stderr, "  2. Toggle the switch ON.")
	fmt.Fprintln(os.Stderr, "  3. Re-run `make security` to read TCC.db.")
}

func runRemedy(args []string) error {
	opts := security.RenderOptions{NoColor: flagNoColor}
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

