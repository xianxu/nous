package brain

import (
	"errors"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	libbrain "github.com/xianxu/nous/lib/brain"
	"github.com/xianxu/nous/lib/gh"
)

// invite_collab.go is the operator-side TUI flow for `nous brain
// invite <gh-login>` from the brain detail screen (key: `a` —
// formerly "add recipient," now "add collaborator," reflecting the
// nous#26 shift from GPG-pubkey admission to GitHub-mediated
// onboarding).
//
// Flow:
//   1. Operator types the invitee's GitHub login.
//   2. Tool best-effort-validates via gh.UserExists. If GitHub
//      returns 404 (brand-new account, shadow-flagged, etc.), the
//      operator can still proceed — same posture as the CLI's
//      `--force` flag. Validation result is shown inline.
//   3. Confirm: "Send invitation to <login> for <owner/repo>? [Y/n]"
//   4. Send via gh.AddCollaborator. Result rendered, then return to
//      detail view (which auto-refreshes recipient state).
//
// The legacy GPG-pubkey path (recipient_add.go) stays in the
// codebase for now — useful for offline / sneakernet admissions
// that don't go through GitHub. Just not the default-keybinding
// surface anymore.

type inviteStage int

const (
	inviteStageLogin   inviteStage = iota // textinput for github login
	inviteStageConfirm                    // shown after Enter on login; validate result inline
	inviteStageDone                       // success or failure rendered
)

// launchInviteCollabMsg signals "open the invite-collaborator
// flow." Emitted by the detail view's `a` keybinding.
type launchInviteCollabMsg struct {
	brainPath string
}

// inviteCollabDoneMsg signals success/failure back to the detail
// view so it can render a banner.
type inviteCollabDoneMsg struct {
	login string
	err   error
}

// cancelInviteCollabMsg signals "back to detail without acting."
type cancelInviteCollabMsg struct{}

type inviteCollabModel struct {
	brainPath string
	stage     inviteStage

	loginInput textinput.Model

	picked    string // github login captured at advance time
	owner     string // resolved from brain remote
	repo      string
	urlErr    error // resolution failure shown inline

	validateErr error // nil = exists; ErrUserNotVisible = brand-new lag; other = real error
	sendErr     error // populated in inviteStageDone on failure
}

func newInviteCollabModel(brainPath string) inviteCollabModel {
	t := textinput.New()
	t.Placeholder = "yingtest42"
	t.Prompt = "  github login> "
	t.CharLimit = 64
	t.Width = 32
	t.Focus()

	m := inviteCollabModel{
		brainPath:  brainPath,
		stage:      inviteStageLogin,
		loginInput: t,
	}

	// Resolve owner/repo up front so the confirm stage can render
	// the target. urlErr is non-nil for non-github brains (or no
	// remote configured); we surface it on the login screen so the
	// operator sees the gating issue immediately.
	url := libbrain.ReadOriginURL(brainPath)
	if url == "" {
		m.urlErr = errors.New("this brain has no remote.origin.url configured — can't invite a github collaborator")
		return m
	}
	owner, repo, err := libbrain.GitHubOwnerRepo(url)
	if err != nil {
		m.urlErr = fmt.Errorf("brain remote isn't a github.com URL (%s)", url)
		return m
	}
	m.owner = owner
	m.repo = repo
	return m
}

func (m inviteCollabModel) Init() tea.Cmd { return textinput.Blink }

func (m inviteCollabModel) Update(msg tea.Msg) (inviteCollabModel, tea.Cmd) {
	switch m.stage {
	case inviteStageLogin:
		return m.updateLogin(msg)
	case inviteStageConfirm:
		return m.updateConfirm(msg)
	case inviteStageDone:
		if _, ok := msg.(tea.KeyMsg); ok {
			return m, func() tea.Msg {
				return inviteCollabDoneMsg{login: m.picked, err: m.sendErr}
			}
		}
	}
	return m, nil
}

func (m inviteCollabModel) updateLogin(msg tea.Msg) (inviteCollabModel, tea.Cmd) {
	if m.urlErr != nil {
		// No usable remote. The only useful action is to back out.
		if km, ok := msg.(tea.KeyMsg); ok {
			switch km.String() {
			case "esc", "q", "ctrl+c", "enter":
				return m, func() tea.Msg { return cancelInviteCollabMsg{} }
			}
		}
		return m, nil
	}
	if km, ok := msg.(tea.KeyMsg); ok {
		switch km.String() {
		case "esc", "ctrl+c":
			return m, func() tea.Msg { return cancelInviteCollabMsg{} }
		case "enter":
			login := strings.TrimPrefix(strings.TrimSpace(m.loginInput.Value()), "@")
			if login == "" {
				return m, nil
			}
			m.picked = login
			// Best-effort validate. The result is informational — even
			// if UserExists returns ErrUserNotVisible (brand-new
			// account; documented case from nous#25), advance to the
			// confirm stage. The operator can still proceed.
			m.validateErr = gh.UserExists(login)
			m.stage = inviteStageConfirm
			return m, nil
		}
	}
	var cmd tea.Cmd
	m.loginInput, cmd = m.loginInput.Update(msg)
	return m, cmd
}

func (m inviteCollabModel) updateConfirm(msg tea.Msg) (inviteCollabModel, tea.Cmd) {
	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch strings.ToLower(km.String()) {
	case "esc", "n", "ctrl+c":
		return m, func() tea.Msg { return cancelInviteCollabMsg{} }
	case "y", "enter":
		// Synchronous gh.AddCollaborator. The op is a single
		// authenticated REST call, so blocking the TUI briefly is
		// acceptable — much simpler than tea.ExecProcess.
		_, err := gh.InviteCollaborator(m.owner, m.repo, m.picked, "push") // clears stale/expired invite first (nous#39)
		m.sendErr = err
		m.stage = inviteStageDone
		return m, nil
	}
	return m, nil
}

func (m inviteCollabModel) View() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Invite a GitHub collaborator"))
	b.WriteString("\n\n")

	if m.urlErr != nil {
		b.WriteString(warnStyle.Render("✗ " + m.urlErr.Error()))
		b.WriteString("\n\n")
		b.WriteString(helpStyle.Render("any key  back"))
		return b.String()
	}

	switch m.stage {
	case inviteStageLogin:
		b.WriteString("Inviting to ")
		b.WriteString(cursorRowStyle.Render(fmt.Sprintf("%s/%s", m.owner, m.repo)))
		b.WriteString("\n\n")
		b.WriteString(m.loginInput.View())
		b.WriteString("\n\n")
		b.WriteString(helpStyle.Render("enter  validate + confirm    esc  cancel"))

	case inviteStageConfirm:
		b.WriteString("About to invite\n\n")
		b.WriteString("  ")
		b.WriteString(cursorRowStyle.Render(m.picked))
		b.WriteString("\n  to ")
		b.WriteString(cursorRowStyle.Render(fmt.Sprintf("%s/%s", m.owner, m.repo)))
		b.WriteString("\n\n")
		// Best-effort validation result — informational, not gating.
		switch {
		case m.validateErr == nil:
			b.WriteString("  ")
			b.WriteString("✓ ")
			b.WriteString(mutedStyle.Render("github reports the user exists."))
			b.WriteString("\n")
		case errors.Is(m.validateErr, gh.ErrUserNotVisible):
			b.WriteString("  ")
			b.WriteString("⚠ ")
			b.WriteString(mutedStyle.Render(
				"github reports the user isn't visible right now. That's common for\n  brand-new accounts whose public profile hasn't propagated yet.\n  You can still send the invite — the collaborator-add endpoint\n  resolves usernames via the internal user table."))
			b.WriteString("\n")
		default:
			b.WriteString("  ")
			b.WriteString("⚠ ")
			b.WriteString(mutedStyle.Render("couldn't validate via github: " + m.validateErr.Error()))
			b.WriteString("\n")
		}
		b.WriteString("\n")
		b.WriteString(helpStyle.Render("y/enter  send invitation    n/esc  cancel"))

	case inviteStageDone:
		if m.sendErr != nil {
			b.WriteString(warnStyle.Render("✗ Invitation failed: " + m.sendErr.Error()))
		} else {
			b.WriteString("✓ Invitation sent to ")
			b.WriteString(cursorRowStyle.Render(m.picked))
			b.WriteString("\n\n")
			b.WriteString(mutedStyle.Render(
				"  Once they open `nous brain` on their machine and accept the\n" +
					"  invitation from the list, their pubkey is published and\n" +
					"  brain-sync's auto-admit appends them to the manifest on the\n" +
					"  next pull cycle."))
		}
		b.WriteString("\n\n")
		b.WriteString(helpStyle.Render("any key  return to brain detail"))
	}
	return b.String()
}
