package brain

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/xianxu/nous/lib/brainsync"
)

// leave.go is the TUI flow run from the detail page when the
// operator presses `l`. Wraps the same runBrainLeave logic the
// `nous brain leave` CLI uses, with a y/N confirmation as the
// only operator gesture.
//
// Inline (no subprocess). The leave flow has no terminal-using
// step — manifest rewrite + git push (ssh-agent + Keychain on
// macOS) + a single gh.api DELETE call. None of these prompt.
//
// Flow:
//   1. Confirm stage — "Leave brain X? [y/N]" with the same
//      three-bullet summary the CLI shows.
//   2. Working stage — runBrainLeave with a confirm that always
//      returns true (the operator already said yes here).
//   3. Done stage — ✓/✗ banner; any key returns to the list.

type leaveStage int

const (
	leaveStageConfirm leaveStage = iota
	leaveStageWorking
	leaveStageDone
)

// launchLeaveMsg signals "open the leave flow for brain at path."
type launchLeaveMsg struct{ brainPath string }

// leaveDoneMsg is the outer signal back to root after the flow
// exits. Root invalidates the list cache and returns to the list.
type leaveDoneMsg struct{ err error }

// leaveWorkMsg is the internal result from the background goroutine
// that runs the leave logic.
type leaveWorkMsg struct {
	err    error
	result brainsync.LeaveResult
}

type leaveModel struct {
	brainPath string
	stage     leaveStage
	err       error
	result    brainsync.LeaveResult
}

func newLeaveModel(brainPath string) leaveModel {
	return leaveModel{brainPath: brainPath, stage: leaveStageConfirm}
}

func (m leaveModel) Init() tea.Cmd { return nil }

func (m leaveModel) Update(msg tea.Msg) (leaveModel, tea.Cmd) {
	switch msg := msg.(type) {
	case leaveWorkMsg:
		m.err = msg.err
		m.result = msg.result
		m.stage = leaveStageDone
		return m, nil
	case tea.KeyMsg:
		switch m.stage {
		case leaveStageConfirm:
			switch msg.String() {
			case "y", "Y":
				m.stage = leaveStageWorking
				path := m.brainPath
				return m, func() tea.Msg {
					// deleteLocal=false in the TUI v1 — keep that knob CLI-only
					// until we've used the default behavior once and confirmed
					// it's right.
					res, err := brainsync.LeaveBrain(context.Background(), path, false)
					return leaveWorkMsg{err: err, result: res}
				}
			case "esc", "n", "N", "ctrl+c":
				return m, func() tea.Msg { return leaveDoneMsg{} }
			}
		case leaveStageDone:
			// Any key returns to list.
			return m, func() tea.Msg { return leaveDoneMsg{err: m.err} }
		}
	}
	return m, nil
}

func (m leaveModel) View() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render(fmt.Sprintf("leave brain — %s", m.brainPath)))
	b.WriteString("\n\n")

	switch m.stage {
	case leaveStageConfirm:
		b.WriteString("This will:\n")
		b.WriteString("  - remove your fingerprint from the manifest\n")
		b.WriteString("  - commit + push the change (gcrypt re-encrypts to remaining collaborators)\n")
		b.WriteString("  - revoke your GitHub collaborator status on this repo\n")
		b.WriteString("\n")
		b.WriteString(mutedStyle.Render("  Local clone stays on disk — rm -rf manually if you want it gone."))
		b.WriteString("\n\n")
		b.WriteString(helpStyle.Render("y  leave    n/esc  cancel    ctrl+c  quit"))
	case leaveStageWorking:
		b.WriteString(mutedStyle.Render("leaving..."))
		b.WriteString("\n  manifest → push → revoke collaborator")
		b.WriteString("\n\n")
		b.WriteString(helpStyle.Render("(working...)"))
	case leaveStageDone:
		if m.err != nil {
			b.WriteString(warnStyle.Render("✗ Leave failed: " + m.err.Error()))
		} else {
			b.WriteString("✓ Removed self from manifest and pushed.\n")
			if m.result.CollaboratorRevoked {
				b.WriteString(fmt.Sprintf("✓ Revoked GitHub collaborator status on %s/%s.\n",
					m.result.Owner, m.result.Repo))
			} else if m.result.CollaboratorRevokeErr != nil {
				b.WriteString(warnStyle.Render(fmt.Sprintf("! GitHub revoke failed: %v\n",
					m.result.CollaboratorRevokeErr)))
				b.WriteString(mutedStyle.Render(fmt.Sprintf("  Retry: gh api -X DELETE repos/%s/%s/collaborators/%s\n",
					m.result.Owner, m.result.Repo, m.result.MyLogin)))
			}
			b.WriteString(mutedStyle.Render(fmt.Sprintf("\nLocal clone retained at %s — rm -rf manually when ready.",
				m.brainPath)))
		}
		b.WriteString("\n\n")
		b.WriteString(helpStyle.Render("any key  return to brain list"))
	}
	return b.String()
}
