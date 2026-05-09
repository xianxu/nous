package security

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/charmbracelet/lipgloss"
	"golang.org/x/term"
)

// Severity orders findings from least to most urgent. Exit codes and
// terminal coloring derive from this.
type Severity int

const (
	SevHygiene Severity = iota
	SevInfo
	SevImportant
	SevCritical
)

func (s Severity) String() string {
	switch s {
	case SevCritical:
		return "CRITICAL"
	case SevImportant:
		return "IMPORTANT"
	case SevInfo:
		return "INFO"
	case SevHygiene:
		return "HYGIENE"
	default:
		return fmt.Sprintf("Severity(%d)", int(s))
	}
}

// MarshalJSON serializes Severity as its uppercase string label so
// `--json` output is human-readable and stable.
func (s Severity) MarshalJSON() ([]byte, error) {
	return json.Marshal(s.String())
}

// BarItem is the numbered "reasonable bar" item from
// docs/threat-model.md (the user-best-practices checklist). Each
// check tags its findings with the bar item it covers; the audit
// renders a per-bar-item status summary at the top of the output so
// the user can see at-a-glance which items pass / fail / skipped.
//
// Numbering is the canonical reference between threat model and
// audit. Stable; new items append at the end.
type BarItem int

const (
	BarNone               BarItem = 0 // finding doesn't map to a numbered bar item
	BarSIP                BarItem = 1 // SIP enabled
	BarTerminalFDA        BarItem = 2 // Terminal/IDE has no FDA
	BarTerminalA11y       BarItem = 3 // Terminal/IDE has no Accessibility
	BarTerminalScreen     BarItem = 4 // Terminal/IDE has no Screen Recording
	BarTerminalEvents     BarItem = 5 // Terminal/IDE has no AppleEvents to credential apps
	BarSudoCache          BarItem = 6 // Sudo cache empty when launching agents
	BarSigningKeyACL      BarItem = 7  // Empty signing-key trusted-apps list
	BarKeychainEntries    BarItem = 8  // Charon keychain entries have ACLs
	BarLaunchdPersistence BarItem = 9  // No suspicious launchd persistence
	BarCharonBinary       BarItem = 10 // Installed charon binary signed + hardened with expected identifier
	BarFileVault          BarItem = 11 // FileVault enabled (at-rest disk encryption)
	BarTimeMachine        BarItem = 12 // Time Machine destinations are encrypted
)

// barItemLabels mirror the "reasonable bar" wording in
// docs/threat-model.md. Keep them in sync — the bar-status table is
// the user's at-a-glance map between audit output and threat model.
var barItemLabels = map[BarItem]string{
	BarSIP:                "SIP enabled",
	BarTerminalFDA:        "No terminal/IDE or dangerous path has Full Disk Access",
	BarTerminalA11y:       "No terminal/IDE or dangerous path has Accessibility",
	BarTerminalScreen:     "No terminal/IDE or dangerous path has Screen Recording",
	BarTerminalEvents:     "No terminal/IDE or dangerous path has AppleEvents to credential apps",
	BarSudoCache:          "Sudo cache empty in this shell",
	BarSigningKeyACL:      "Charon signing-key has no dangerous trusted apps",
	BarKeychainEntries:    "Charon keychain entries have ACLs",
	BarLaunchdPersistence: "No suspicious launchd persistence",
	BarCharonBinary:       "Installed charon CLI is signed + hardened",
	BarFileVault:          "FileVault enabled (at-rest disk encryption)",
	BarTimeMachine:        "Time Machine destinations are encrypted",
}

// allBarItems is the canonical ordered enumeration. Used when
// rendering the per-bar-item status table.
var allBarItems = []BarItem{
	BarSIP, BarTerminalFDA, BarTerminalA11y, BarTerminalScreen,
	BarTerminalEvents, BarSudoCache, BarSigningKeyACL,
	BarKeychainEntries, BarLaunchdPersistence, BarCharonBinary,
	BarFileVault, BarTimeMachine,
}

// Finding is one item the audit produced. ID is stable across runs and
// is the key into remedy text. Affects holds app names / paths the
// finding pertains to (may be empty). BarItem links the finding back
// to a numbered "reasonable bar" item (0 = doesn't map).
type Finding struct {
	ID        string   `json:"id"`
	Severity  Severity `json:"severity"`
	Title     string   `json:"title"`
	Detail    string   `json:"detail,omitempty"`
	RemedyRef string   `json:"remedy_ref,omitempty"`
	Affects   []string `json:"affects,omitempty"`
	BarItem   BarItem  `json:"bar_item,omitempty"`
}

// Report aggregates findings and produces the rollup exit code.
//
// Evaluated lists the bar items this run actually checked. Items
// not present here are reported as "skipped" in the bar-status
// table — important for distinguishing "passed because no problem
// found" from "passed by accident because we couldn't check"
// (e.g. when --no-tcc is set, items 2–5 are skipped; when FDA
// isn't granted, the TCC checks emit findings AND the items are
// still skipped).
type Report struct {
	Findings  []Finding
	Evaluated []BarItem
}

// MarkEvaluated records that a bar item was checked in this run.
// Idempotent.
func (r *Report) MarkEvaluated(items ...BarItem) {
	seen := map[BarItem]bool{}
	for _, b := range r.Evaluated {
		seen[b] = true
	}
	for _, b := range items {
		if !seen[b] {
			r.Evaluated = append(r.Evaluated, b)
			seen[b] = true
		}
	}
}

// BarStatus is the rolled-up state for one numbered bar item, used
// by the summary table at the top of audit output.
type BarStatus int

const (
	BarPass    BarStatus = iota // evaluated, no findings (or only Hygiene)
	BarReview                   // evaluated, has Info findings the user should look at
	BarFail                     // evaluated, has Important or Critical findings
	BarSkipped                  // not evaluated this run (e.g. --no-tcc skips items 2–5)
)

func (s BarStatus) String() string {
	switch s {
	case BarPass:
		return "pass"
	case BarReview:
		return "review"
	case BarFail:
		return "FAIL"
	case BarSkipped:
		return "skipped"
	default:
		return "unknown"
	}
}

// barStatuses computes per-bar-item rollups from r.Evaluated +
// r.Findings.
func (r Report) barStatuses() map[BarItem]BarStatus {
	out := make(map[BarItem]BarStatus, len(allBarItems))
	evaluated := map[BarItem]bool{}
	for _, b := range r.Evaluated {
		evaluated[b] = true
	}
	worstByBar := map[BarItem]Severity{}
	for _, f := range r.Findings {
		if f.BarItem == BarNone {
			continue
		}
		if f.Severity > worstByBar[f.BarItem] {
			worstByBar[f.BarItem] = f.Severity
		}
	}
	for _, b := range allBarItems {
		if !evaluated[b] {
			out[b] = BarSkipped
			continue
		}
		switch worstByBar[b] {
		case SevCritical, SevImportant:
			out[b] = BarFail
		case SevInfo:
			out[b] = BarReview
		default:
			out[b] = BarPass
		}
	}
	return out
}

// ExitCode maps the worst finding's severity to a process exit code.
//
//	any Critical  -> 2
//	any Important -> 1
//	otherwise     -> 0
func (r Report) ExitCode() int {
	worst := SevHygiene
	for _, f := range r.Findings {
		if f.Severity > worst {
			worst = f.Severity
		}
	}
	switch worst {
	case SevCritical:
		return 2
	case SevImportant:
		return 1
	default:
		return 0
	}
}

// Counts returns per-severity counts for summary lines.
func (r Report) Counts() map[Severity]int {
	c := map[Severity]int{}
	for _, f := range r.Findings {
		c[f.Severity]++
	}
	return c
}

// PrintOptions controls Report.Print output shape.
type PrintOptions struct {
	NoColor    bool // force ANSI off (default: auto-detect TTY on stderr)
	ForceColor bool // force ANSI on (overrides TTY detection — for `make security`'s tempfile-redirect path)
	JSON       bool // emit JSON instead of human text
}

// Print renders the report to w using the requested format. JSON
// output is dependable for CI consumption; text output is colorized
// per severity when stderr is a TTY (and `--no-color` isn't set).
func (r Report) Print(w io.Writer, opts PrintOptions) error {
	if opts.JSON {
		return r.printJSON(w)
	}
	r.printText(w, opts)
	return nil
}

// reportJSON is the top-level shape we emit, distinct from Report so
// we can attach Counts and ExitCode without leaking internal types
// (Severity-keyed maps don't serialize cleanly).
type reportJSON struct {
	Summary  reportSummaryJSON `json:"summary"`
	Bar      []barRowJSON      `json:"bar"`
	Findings []Finding         `json:"findings"`
}

type reportSummaryJSON struct {
	Total      int            `json:"total"`
	BySeverity map[string]int `json:"by_severity"`
	ExitCode   int            `json:"exit_code"`
}

type barRowJSON struct {
	Number int    `json:"number"`
	Label  string `json:"label"`
	Status string `json:"status"`
}

func (r Report) printJSON(w io.Writer) error {
	counts := r.Counts()
	statuses := r.barStatuses()
	bar := make([]barRowJSON, 0, len(allBarItems))
	for _, b := range allBarItems {
		bar = append(bar, barRowJSON{
			Number: int(b),
			Label:  barItemLabels[b],
			Status: statuses[b].String(),
		})
	}
	out := reportJSON{
		Summary: reportSummaryJSON{
			Total:    len(r.Findings),
			ExitCode: r.ExitCode(),
			BySeverity: map[string]int{
				"critical":  counts[SevCritical],
				"important": counts[SevImportant],
				"info":      counts[SevInfo],
				"hygiene":   counts[SevHygiene],
			},
		},
		Bar:      bar,
		Findings: r.Findings,
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

// Severity colors are vetted against both light and dark terminal
// themes — pure-red is illegible on light, pure-yellow on light;
// these are the lipgloss adaptive picks.
var (
	styleCritical  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.AdaptiveColor{Light: "#a40000", Dark: "#ff5f5f"})
	styleImportant = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#af5f00", Dark: "#ffaf00"})
	styleInfo      = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#005f87", Dark: "#5fafff"})
	styleHygiene   = lipgloss.NewStyle().Faint(true)
	styleHint      = lipgloss.NewStyle().Faint(true)
)

func styleFor(s Severity) lipgloss.Style {
	switch s {
	case SevCritical:
		return styleCritical
	case SevImportant:
		return styleImportant
	case SevInfo:
		return styleInfo
	default:
		return styleHygiene
	}
}

func (r Report) printText(w io.Writer, opts PrintOptions) {
	noColor := opts.NoColor
	useColor := opts.ForceColor || (!noColor && term.IsTerminal(int(os.Stderr.Fd())))
	render := func(s Severity, text string) string {
		if !useColor {
			return text
		}
		return styleFor(s).Render(text)
	}
	hint := func(text string) string {
		if !useColor {
			return text
		}
		return styleHint.Render(text)
	}
	statusStyle := func(s BarStatus, text string) string {
		if !useColor {
			return text
		}
		switch s {
		case BarFail:
			return styleCritical.Render(text)
		case BarReview:
			return styleInfo.Render(text)
		case BarSkipped:
			return styleHygiene.Render(text)
		}
		return text // pass: no styling
	}

	statuses := r.barStatuses()
	fmt.Fprintf(w, "\nBest-practices status (see docs/threat-model.md):\n")
	for _, b := range allBarItems {
		s := statuses[b]
		marker := map[BarStatus]string{
			BarPass:    "✓",
			BarReview:  "•",
			BarFail:    "✗",
			BarSkipped: "—",
		}[s]
		fmt.Fprintf(w, "  [%d] %-55s %s %s\n",
			int(b), barItemLabels[b], statusStyle(s, marker), statusStyle(s, s.String()))
	}

	counts := r.Counts()
	fmt.Fprintf(w, "\nFinding counts: %d total  (%s=%d  %s=%d  %s=%d  %s=%d)\n",
		len(r.Findings),
		render(SevCritical, "critical"), counts[SevCritical],
		render(SevImportant, "important"), counts[SevImportant],
		render(SevInfo, "info"), counts[SevInfo],
		render(SevHygiene, "hygiene"), counts[SevHygiene],
	)

	// Severity-desc sort puts Critical at the top so actionable
	// findings aren't buried. Stable secondary sort by ID for
	// deterministic ordering across runs. Hygiene-tier findings are
	// suppressed from the human-readable list — they don't carry
	// actionable signal (the situations they describe are either
	// out of the user's hands, like third-party apps' entitlements,
	// or already known-benign, like Apple-default trusted apps).
	// They remain in counts above and in --json output for tooling.
	sorted := make([]Finding, 0, len(r.Findings))
	for _, f := range r.Findings {
		if f.Severity == SevHygiene {
			continue
		}
		sorted = append(sorted, f)
	}
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Severity != sorted[j].Severity {
			return sorted[i].Severity > sorted[j].Severity
		}
		return sorted[i].ID < sorted[j].ID
	})

	for _, f := range sorted {
		tag := render(f.Severity, "["+f.Severity.String()+"]")
		fmt.Fprintf(w, "  %s %s — %s\n", tag, f.ID, f.Title)
		for _, a := range f.Affects {
			fmt.Fprintf(w, "      %s\n", a)
		}
		if f.RemedyRef != "" {
			fmt.Fprintf(w, "      %s\n", hint("→ details: nous-security remedy "+f.RemedyRef))
		}
	}

	if hidden := counts[SevHygiene]; hidden > 0 {
		fmt.Fprintf(w, "  %s\n", hint(fmt.Sprintf("(%d hygiene finding%s hidden — use --json to see them)",
			hidden, pluralS(hidden))))
	}
}

func pluralS(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
