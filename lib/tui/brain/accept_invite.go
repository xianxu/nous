package brain

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	libbrain "github.com/xianxu/nous/lib/brain"
	"github.com/xianxu/nous/lib/gh"
	"github.com/xianxu/nous/lib/identity"
)

// accept_invite.go is the joiner-side TUI flow run from the brain
// list when the operator presses Enter on a `(invited — press enter
// to join)` row. Previously delegated to `tea.ExecProcess nous brain
// join`; refactored inline because the flow has no terminal-using
// subprocess (no pinentry, no gcrypt decryption — just one gh.api
// PATCH and a plain-git push of the pubkey to the keys branch).
//
// Inline means: stay in alt-screen, no flicker, immediate state
// updates as each step completes. The SSH push inside
// PublishOwnPubkeyToRemote uses ssh-agent + Keychain (per the
// nous-bootstrap config), so no interactive prompt fires during a
// healthy flow.
//
// Flow:
//   1. Identity stage — pick which GPG key signs this brain (skipped
//      if there's only one secret key).
//   2. Confirm stage — "Accept invitation to <repo> as <login> using
//      <key>? [y/N]".
//   3. Working stage — gh.AcceptInvitation then
//      brain.PublishOwnPubkeyToRemote, both synchronous.
//   4. Done stage — show result + what's next.

type acceptStage int

const (
	acceptStageIdentity acceptStage = iota
	acceptStageConfirm
	acceptStageWorking
	acceptStageDone
)

// launchAcceptInviteMsg signals "open the accept-invite flow."
// Emitted by the list's Enter handler on a pending row.
type launchAcceptInviteMsg struct {
	invitation gh.Invitation
}

// acceptInviteDoneMsg is the outer signal back to root after the
// flow exits (success or failure). Root re-renders the list.
type acceptInviteDoneMsg struct{ err error }

// acceptWorkMsg is the internal tea.Msg returned from the
// background goroutine that does the accept + publish. Carries the
// err (nil on success).
type acceptWorkMsg struct{ err error }

type acceptInviteModel struct {
	gh gh.Client

	invitation gh.Invitation

	stage acceptStage

	keys     []identity.Key
	cursor   int   // identity-stage cursor
	pickedFP string // picked key's fingerprint
	myLogin  string // auth'd github user (filename stem for <login>.asc)

	loadErr error // populated on Init failures (gh.AuthLogin, identity.List)
	err     error // populated in done stage on accept/publish failure
}

func newAcceptInviteModel(c gh.Client, inv gh.Invitation) acceptInviteModel {
	m := acceptInviteModel{gh: c, invitation: inv}

	// Resolve auth login (filename stem for the published <login>.asc).
	login, err := c.AuthLogin()
	if err != nil {
		m.loadErr = fmt.Errorf("resolve github login: %w", err)
		return m
	}
	m.myLogin = login

	// Load GPG identities for the picker stage.
	keys, err := identity.List()
	if err != nil {
		m.loadErr = fmt.Errorf("list GPG identities: %w", err)
		return m
	}
	if len(keys) == 0 {
		m.loadErr = fmt.Errorf("no GPG secret key found; run `nous identity init` first")
		return m
	}
	m.keys = keys
	if len(keys) == 1 {
		// Single key — auto-pick + jump to confirm.
		m.pickedFP = keys[0].Fingerprint
		m.stage = acceptStageConfirm
	} else {
		m.stage = acceptStageIdentity
	}
	return m
}

func (m acceptInviteModel) Init() tea.Cmd { return nil }

func (m acceptInviteModel) Update(msg tea.Msg) (acceptInviteModel, tea.Cmd) {
	if m.loadErr != nil {
		// Init failure — any key returns to list.
		if _, ok := msg.(tea.KeyMsg); ok {
			return m, func() tea.Msg { return acceptInviteDoneMsg{err: m.loadErr} }
		}
		return m, nil
	}

	if wm, ok := msg.(acceptWorkMsg); ok {
		m.stage = acceptStageDone
		m.err = wm.err
		return m, nil
	}

	switch m.stage {
	case acceptStageIdentity:
		return m.updateIdentity(msg)
	case acceptStageConfirm:
		return m.updateConfirm(msg)
	case acceptStageWorking:
		// Subprocess in flight — ignore key input.
		return m, nil
	case acceptStageDone:
		if _, ok := msg.(tea.KeyMsg); ok {
			return m, func() tea.Msg { return acceptInviteDoneMsg{err: m.err} }
		}
	}
	return m, nil
}

func (m acceptInviteModel) updateIdentity(msg tea.Msg) (acceptInviteModel, tea.Cmd) {
	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch km.String() {
	case "esc", "ctrl+c":
		return m, func() tea.Msg { return acceptInviteDoneMsg{err: nil} } // cancel
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.keys)-1 {
			m.cursor++
		}
	case "enter":
		m.pickedFP = m.keys[m.cursor].Fingerprint
		m.stage = acceptStageConfirm
	}
	return m, nil
}

func (m acceptInviteModel) updateConfirm(msg tea.Msg) (acceptInviteModel, tea.Cmd) {
	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch strings.ToLower(km.String()) {
	case "esc", "n", "ctrl+c":
		return m, func() tea.Msg { return acceptInviteDoneMsg{err: nil} } // cancel
	case "y", "enter":
		m.stage = acceptStageWorking
		// Background: accept + publish, then post the result via
		// acceptWorkMsg so the View can render a status frame in
		// the meantime.
		invID := m.invitation.ID
		c := m.gh
		cloneURL := c.CloneURL(m.invitation.Repository.FullName, m.invitation.Repository.SSHURL)
		login := m.myLogin
		fp := m.pickedFP
		return m, func() tea.Msg {
			ctx := context.Background()
			if err := c.AcceptInvitation(invID); err != nil {
				return acceptWorkMsg{err: fmt.Errorf("accept invitation: %w", err)}
			}
			armor, err := identity.Export(fp)
			if err != nil {
				return acceptWorkMsg{err: fmt.Errorf("export pubkey: %w", err)}
			}
			if err := libbrain.PublishOwnPubkeyToRemote(ctx, cloneURL, login, armor); err != nil {
				return acceptWorkMsg{err: fmt.Errorf("publish pubkey: %w", err)}
			}
			return acceptWorkMsg{err: nil}
		}
	}
	return m, nil
}

func (m acceptInviteModel) View() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render(fmt.Sprintf("Join %s", m.invitation.Repository.FullName)))
	b.WriteString("\n\n")

	if m.loadErr != nil {
		b.WriteString(warnStyle.Render("✗ " + m.loadErr.Error()))
		b.WriteString("\n\n")
		b.WriteString(helpStyle.Render("any key  back"))
		return b.String()
	}

	switch m.stage {
	case acceptStageIdentity:
		b.WriteString("Multiple secret keys in your keyring. Which key should\n")
		b.WriteString("sign this brain? (You'll publish this key's pubkey to\n")
		b.WriteString("the brain's keys branch.)\n\n")
		for i, k := range m.keys {
			row := fmt.Sprintf("  %s  %s", shortFingerprint(k.Fingerprint), k.UID)
			if i == m.cursor {
				row = cursorRowStyle.Render("▸ " + fmt.Sprintf("%s  %s", shortFingerprint(k.Fingerprint), k.UID))
			}
			b.WriteString(row)
			b.WriteString("\n")
		}
		b.WriteString("\n")
		b.WriteString(helpStyle.Render("↑↓/jk  navigate    enter  pick    esc  cancel"))

	case acceptStageConfirm:
		b.WriteString("About to accept invitation and publish your pubkey:\n\n")
		b.WriteString(fmt.Sprintf("  brain:     %s\n", cursorRowStyle.Render(m.invitation.Repository.FullName)))
		b.WriteString(fmt.Sprintf("  as login:  %s\n", cursorRowStyle.Render(m.myLogin)))
		b.WriteString(fmt.Sprintf("  using key: %s\n", cursorRowStyle.Render(shortFingerprint(m.pickedFP))))
		b.WriteString("\n")
		b.WriteString(mutedStyle.Render(
			"  Two operations run synchronously: gh API accept (one call),\n" +
				"  then plain-git push of <login>.asc to the keys branch. No\n" +
				"  pinentry prompts; ssh-agent handles the push silently."))
		b.WriteString("\n\n")
		b.WriteString(helpStyle.Render("y/enter  join    n/esc  cancel"))

	case acceptStageWorking:
		b.WriteString(mutedStyle.Render("  Accepting invitation and publishing pubkey..."))
		b.WriteString("\n")

	case acceptStageDone:
		if m.err != nil {
			b.WriteString(warnStyle.Render("✗ " + m.err.Error()))
		} else {
			b.WriteString("✓ Joined.\n\n")
			b.WriteString(mutedStyle.Render(
				"  Operator's brain-sync will auto-admit you on its next pull\n" +
					"  cycle. The brain will then show up as `(collaborator —\n" +
					"  press enter to clone)` in the list; Enter there to clone."))
		}
		b.WriteString("\n\n")
		b.WriteString(helpStyle.Render("any key  back to list"))
	}
	return b.String()
}
