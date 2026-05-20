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
// screen (key: `n`). The flow is intentionally thin — the TUI is the
// launchpad, not a re-implementation of nous brain new's logic:
//
//  1. Prompt for target directory path.
//  2. Confirm.
//  3. tea.ExecProcess delegates to `nous brain new <path>`,
//     temporarily releasing the terminal so the script can do its
//     GPG-passphrase + gh-prompt + confirmation ceremony in the
//     normal terminal mode.
//  4. On subprocess completion, return to the list (refreshed).
//
// Why not re-implement the ceremony in tea? Because nous brain new
// already handles the multi-step prompts, gpg-agent invocations,
// and gh API calls correctly. Reimplementing inside bubbletea would
// require duplicating that logic AND fighting bubbletea's alternate-
// screen rendering against gpg-agent's pinentry. Delegation via
// ExecProcess is the right boundary.

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

	picked string // path after confirmation; passed through to ExecProcess
	err    error
}

func newNewBrainModel() newBrainModel {
	p := textinput.New()
	p.Placeholder = "../brain-family"
	p.Prompt = "  target path> "
	p.CharLimit = 1024
	p.Width = 64
	p.Focus()
	return newBrainModel{stage: newStagePath, path: p}
}

func (m newBrainModel) Init() tea.Cmd { return textinput.Blink }

func (m newBrainModel) Update(msg tea.Msg) (newBrainModel, tea.Cmd) {
	// Subprocess completion lands as subprocessCompletedMsg regardless
	// of stage. Translate to the public newBrainDoneMsg, return to the
	// list via cancelNewBrainMsg.
	if sc, ok := msg.(subprocessCompletedMsg); ok {
		m.stage = newStageDone
		m.err = sc.err
		return m, func() tea.Msg { return newBrainDoneMsg{path: m.picked, err: sc.err} }
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
			m.stage = newStageConfirm
			return m, nil
		}
	}
	var cmd tea.Cmd
	m.path, cmd = m.path.Update(msg)
	return m, cmd
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
		cmd := exec.Command(bin, "brain", "new", m.picked)
		// Pass through current env; nous brain new reads gh auth,
		// GNUPGHOME, etc.
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
			"That command will prompt for your GPG passphrase and gh\n" +
				"confirmations in your normal terminal — the TUI yields\n" +
				"control until it completes, then resumes."))
		b.WriteString("\n\n")
		b.WriteString(helpStyle.Render("y/enter  run    n/esc  cancel"))

	case newStageDone:
		if m.err != nil {
			b.WriteString(warnStyle.Render("✗ nous brain new failed: " + m.err.Error()))
		} else {
			b.WriteString("✓ Brain created at ")
			b.WriteString(cursorRowStyle.Render(m.picked))
		}
		b.WriteString("\n\n")
		b.WriteString(helpStyle.Render("any key  return to list"))
	}
	return b.String()
}
