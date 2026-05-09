package security

import (
	"fmt"
	"io"
	"os/exec"
)

// VisualPanes are the System Settings privacy panes the user should
// audit by hand when --no-tcc is set. Order matters: critical surfaces
// first.
var VisualPanes = []struct {
	Title    string
	URL      string
	Look     string
	Severity Severity
}{
	{
		Title:    "Full Disk Access",
		URL:      "x-apple.systempreferences:com.apple.preference.security?Privacy_AllFiles",
		Look:     "ANY terminal or IDE (Terminal, iTerm2, Ghostty, VS Code, Cursor, Warp, ...) → toggle off.",
		Severity: SevCritical,
	},
	{
		Title:    "Accessibility",
		URL:      "x-apple.systempreferences:com.apple.preference.security?Privacy_Accessibility",
		Look:     "ANY terminal or IDE → toggle off. Window managers (Rectangle, Hammerspoon) are OK.",
		Severity: SevCritical,
	},
	{
		Title:    "Screen Recording",
		URL:      "x-apple.systempreferences:com.apple.preference.security?Privacy_ScreenCapture",
		Look:     "Terminals/IDEs should not be present unless you actively need it (e.g. screenshot tooling).",
		Severity: SevImportant,
	},
	{
		Title:    "Automation",
		URL:      "x-apple.systempreferences:com.apple.preference.security?Privacy_Automation",
		Look:     "Watch for: terminals/IDEs that can drive Keychain Access, 1Password, Bitwarden, or Mail.",
		Severity: SevImportant,
	},
	{
		Title:    "Files and Folders",
		URL:      "x-apple.systempreferences:com.apple.preference.security?Privacy_FilesAndFolders",
		Look:     "Per-folder grants (Documents/Downloads/Desktop). Less catastrophic than FDA but still worth pruning.",
		Severity: SevInfo,
	},
}

// RunVisualWalk steps the user through each pane, opening it on Enter
// and waiting for the user to confirm they've audited it before moving
// on. `open` failures are warned-on but don't abort the walk — one
// missing pane shouldn't kill the rest of the audit.
func RunVisualWalk(w io.Writer) {
	fmt.Fprintln(w, "Manual TCC audit — walking the privacy panes one by one.")
	fmt.Fprintln(w, "For each pane: visually verify, toggle off anything suspect, then press Enter.")
	fmt.Fprintln(w)

	for i, p := range VisualPanes {
		fmt.Fprintf(w, "[%d/%d] %s  (%s)\n", i+1, len(VisualPanes), p.Title, p.Severity)
		fmt.Fprintf(w, "       look for: %s\n", p.Look)
		if !ConfirmDefaultYes("       open this pane now?") {
			fmt.Fprintln(w, "       skipped.")
			continue
		}
		if err := exec.Command("open", p.URL).Run(); err != nil {
			fmt.Fprintf(w, "       (could not open pane: %v)\n", err)
		}
		ConfirmDefaultYes("       audited?")
		fmt.Fprintln(w)
	}
	fmt.Fprintln(w, "Manual audit complete.")
}
