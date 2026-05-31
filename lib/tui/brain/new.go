package brain

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// new.go owns the "create a new brain" flow launched from the list
// screen (key: `n`). It creates a LOCAL brain (nous#33): no remote, no
// GitHub, no network, and — crucially — no GPG identity. The flow is
// intentionally thin:
//
//  1. Prompt for target directory path.
//  2. Confirm.
//  3. tea.ExecProcess delegates to `nous brain new <path>` (which
//     defaults to local creation), briefly releasing the terminal.
//  4. On subprocess completion, return to the list (refreshed).
//
// No identity picker, no passphrase prompt: a local brain has nothing
// to encrypt, so there's no key to choose. The recipient (and any GPG
// ceremony) is established later, by `nous brain publish` — which the
// operator reaches via the detail view's `p` action once the brain
// exists. Delegating to the CLI via ExecProcess keeps a single source
// of truth for what "create a brain" means.

type newBrainStage int

const (
	newStagePath    newBrainStage = iota // textinput for path
	newStageConfirm                      // "Run nous brain new <path>? [Y/n]"
	newStageDone                         // result rendered; any key returns
)

// launchNewBrainMsg signals "open the new-brain flow." Emitted by
// the list's `n` keybinding.
type launchNewBrainMsg struct{}

// newBrainDoneMsg signals "the new-brain flow has finished (success
// or failure)." Carries the err so the list view can render a banner.
type newBrainDoneMsg struct {
	path string
	err  error
}

// subprocessCompletedMsg is the internal signal from tea.ExecProcess's
// callback. Translated to newBrainDoneMsg at the model boundary.
type subprocessCompletedMsg struct{ err error }

type newBrainModel struct {
	stage newBrainStage
	path  textinput.Model

	picked string // resolved path; passed to ExecProcess
	err    error
}

func newNewBrainModel() newBrainModel {
	p := textinput.New()
	p.Placeholder = "../my-notes"
	p.Prompt = "  target path> "
	p.CharLimit = 1024
	p.Width = 64
	p.Focus()
	return newBrainModel{stage: newStagePath, path: p}
}

func (m newBrainModel) Init() tea.Cmd { return textinput.Blink }

func (m newBrainModel) Update(msg tea.Msg) (newBrainModel, tea.Cmd) {
	// Subprocess completion lands as subprocessCompletedMsg regardless
	// of stage.
	//
	// Failure path: quit the TUI immediately. tea.ExecProcess released
	// alt-screen while the subprocess ran (so its output is on the
	// normal-screen terminal); re-entering alt-screen on resume would
	// hide that output. tea.Quit drops alt-screen on the way out,
	// leaving the subprocess output visible in the operator's terminal
	// where they can read the actual error. The done-stage would have
	// shown only "exit status N" which is uselessly terse.
	//
	// Success path: stay in the done-stage briefly so the operator
	// gets a tight visual confirmation without needing to scroll
	// through the subprocess output.
	if sc, ok := msg.(subprocessCompletedMsg); ok {
		m.err = sc.err
		if sc.err != nil {
			return m, tea.Quit
		}
		m.stage = newStageDone
		return m, func() tea.Msg { return newBrainDoneMsg{path: m.picked, err: nil} }
	}

	switch m.stage {
	case newStagePath:
		return m.updatePath(msg)
	case newStageConfirm:
		return m.updateConfirm(msg)
	case newStageDone:
		// Any key returns to list.
		if _, ok := msg.(tea.KeyMsg); ok {
			return m, func() tea.Msg { return cancelNewBrainMsg{} }
		}
	}
	return m, nil
}

func (m newBrainModel) updatePath(msg tea.Msg) (newBrainModel, tea.Cmd) {
	if km, ok := msg.(tea.KeyMsg); ok {
		switch km.String() {
		case "esc", "ctrl+c":
			return m, func() tea.Msg { return cancelNewBrainMsg{} }
		case "enter":
			val := strings.TrimSpace(m.path.Value())
			if val == "" {
				return m, nil
			}
			abs := resolvePath(val)
			if pathExists(abs) {
				// Refuse to advance; the operator can see the ❌ in
				// the preview and pick a different path. Without
				// this, the subprocess would fail at scripts/
				// new-brain.sh's "Local path already exists" check —
				// surfacing it here is faster and clearer.
				return m, nil
			}
			m.picked = abs
			// No identity stage — a local brain needs no key. Straight
			// to confirm.
			m.stage = newStageConfirm
			return m, nil
		}
	}
	var cmd tea.Cmd
	m.path, cmd = m.path.Update(msg)
	return m, cmd
}

// shortFingerprint renders the last 8 hex chars of a fingerprint
// (lowercased) — matches the convention used by `nous identity
// list` and `nous brain` everywhere else.
func shortFingerprint(fp string) string {
	if len(fp) < 8 {
		return strings.ToLower(fp)
	}
	return strings.ToLower(fp[len(fp)-8:])
}

// pathExists reports whether the absolute path refers to an
// existing filesystem entry (file, directory, or anything else).
// Used to refuse advance + render ❌ in the live preview when the
// operator types a target nous brain new would reject.
func pathExists(abs string) bool {
	_, err := os.Stat(abs)
	return err == nil
}

// resolvePath converts the operator's input into the absolute path
// that `nous brain new` will actually create. Mirrors the resolution
// rules nous brain new applies:
//
//   - `~/...` expands to $HOME/...
//   - absolute paths pass through unchanged
//   - everything else is resolved relative to the current working
//     directory (NOT the workspace root — relative paths in Go's
//     filesystem operations are CWD-relative)
//
// Returns the input unchanged on any resolution error so the
// confirm-stage preview still shows what the operator typed.
func resolvePath(input string) string {
	if strings.HasPrefix(input, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			input = filepath.Join(home, input[2:])
		}
	}
	if filepath.IsAbs(input) {
		return filepath.Clean(input)
	}
	abs, err := filepath.Abs(input)
	if err != nil {
		return input
	}
	return abs
}

func (m newBrainModel) updateConfirm(msg tea.Msg) (newBrainModel, tea.Cmd) {
	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch strings.ToLower(km.String()) {
	case "esc", "n", "ctrl+c":
		return m, func() tea.Msg { return cancelNewBrainMsg{} }
	case "y", "enter":
		// Locate the running nous binary so we re-invoke this exact
		// build (matters during development — the binary in $PATH
		// may differ from the one running this TUI).
		bin, err := os.Executable()
		if err != nil {
			bin = "nous" // fall back to PATH lookup
		}
		// `nous brain new <path>` with no flags → a local brain. No
		// --as: local creation ignores the identity entirely.
		cmd := exec.Command(bin, "brain", "new", m.picked)
		cmd.Env = os.Environ()
		return m, tea.ExecProcess(cmd, func(err error) tea.Msg {
			return subprocessCompletedMsg{err: err}
		})
	}
	return m, nil
}

func (m newBrainModel) View() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Create a new brain"))
	b.WriteString("\n\n")

	switch m.stage {
	case newStagePath:
		b.WriteString("Where should the new brain live? Brains should be peers of nous.\n\n")
		b.WriteString(m.path.View())
		b.WriteString("\n")
		// Live preview of the resolved absolute path so the operator
		// sees exactly where the new brain will land — relative-path
		// confusion was the bug this preview was added to fix. The
		// ❌ vs → marker tells them whether the target is free
		// before they press Enter (nous brain new refuses to clobber
		// existing paths, so an already-existing target would fail
		// the subprocess; better to call it out upfront).
		if val := strings.TrimSpace(m.path.Value()); val != "" {
			abs := resolvePath(val)
			arrow := "→"
			if pathExists(abs) {
				arrow = "❌"
			}
			b.WriteString(mutedStyle.Render("  " + arrow + " " + abs))
			b.WriteString("\n")
		}
		b.WriteString("\n")
		b.WriteString(helpStyle.Render("enter  continue    esc  cancel"))

	case newStageConfirm:
		b.WriteString("About to launch:\n\n")
		b.WriteString("  ")
		b.WriteString(cursorRowStyle.Render(fmt.Sprintf("nous brain new %s", m.picked)))
		b.WriteString("\n\n")
		b.WriteString(mutedStyle.Render(
			"Creates a local brain on this device — no remote, no network,\n" +
				"no GPG key needed. Encrypted at rest by FileVault. Back it up\n" +
				"to GitHub later with `nous brain publish` (the detail view's\n" +
				"`p` action)."))
		b.WriteString("\n\n")
		b.WriteString(helpStyle.Render("y/enter  run    n/esc  cancel"))

	case newStageDone:
		if m.err != nil {
			b.WriteString(warnStyle.Render("✗ nous brain new failed: " + m.err.Error()))
		} else {
			b.WriteString("✓ Local brain created at ")
			b.WriteString(cursorRowStyle.Render(m.picked))
			b.WriteString("\n")
			b.WriteString(mutedStyle.Render("  `p` from its detail view publishes it to GitHub when you're ready."))
		}
		b.WriteString("\n\n")
		b.WriteString(helpStyle.Render("any key  return to list"))
	}
	return b.String()
}
