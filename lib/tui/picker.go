package tui

import (
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xianxu/nous/lib/provider/vault"
)

type pickerItem struct {
	email  string
	isNew  bool
	health AccountHealth // empty when checks aren't wired (default UX)
}

// pickerState captures the OAuth picker's modal state. Normal is the
// default account-list view. RevokeConfirm overlays a confirm modal
// for the cursored account (parity with the admin-key entity list's
// `r` flow, see admin_key_list.go).
type pickerState int

const (
	pickerStateNormal pickerState = iota
	pickerStateRevokeConfirm
)

type pickerModel struct {
	items     []pickerItem
	cursor    int
	state     pickerState
	statusMsg string // transient hint (e.g. revoke result); cleared on nav
}

func newPickerModel(v vault.Store) (pickerModel, error) {
	creds, err := v.List()
	if err != nil {
		return pickerModel{}, fmt.Errorf("list accounts: %w", err)
	}
	var items []pickerItem
	for _, c := range creds {
		if c.Provider != "google" {
			continue
		}
		items = append(items, pickerItem{email: c.Account})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].email < items[j].email })
	items = append(items, pickerItem{isNew: true})
	return pickerModel{items: items}, nil
}

// AnnotateHealth runs the health checker against each Google account
// in the picker and stamps results onto items. No-op when check is nil.
//
// Synchronous — does len(items) refresh-token network roundtrips.
// Cheap for personal-proxy use (typically 1-3 accounts, ~600ms total
// worst case). If this becomes a TUI-startup bottleneck, future work
// can move it to a background goroutine that updates via tea.Msg.
func (m *pickerModel) AnnotateHealth(v vault.Store, check AccountHealthChecker) {
	if check == nil {
		return
	}
	for i := range m.items {
		if m.items[i].isNew {
			continue
		}
		cred, err := v.Get("google", m.items[i].email)
		if err != nil {
			m.items[i].health = AccountHealthUnknown
			continue
		}
		m.items[i].health = check(cred)
	}
}

// Messages emitted by the picker.
type accountSelectedMsg struct{ email string }
type newAccountMsg struct{}

// pickerBackMsg signals "navigate back to the provider picker" — the
// new top-level. Distinct from tea.Quit which terminates the program.
type pickerBackMsg struct{}

func (m pickerModel) Update(msg tea.Msg) (pickerModel, tea.Cmd) {
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	if m.state == pickerStateRevokeConfirm {
		return m.updateRevokeConfirm(keyMsg)
	}
	switch keyMsg.String() {
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
			m.statusMsg = ""
		}
	case "down", "j":
		if m.cursor < len(m.items)-1 {
			m.cursor++
			m.statusMsg = ""
		}
	case "enter":
		if m.cursor < 0 || m.cursor >= len(m.items) {
			return m, nil
		}
		item := m.items[m.cursor]
		if item.isNew {
			return m, func() tea.Msg { return newAccountMsg{} }
		}
		return m, func() tea.Msg { return accountSelectedMsg{email: item.email} }
	case "r":
		// Revoke from list level — parity with admin-key entity list.
		// Only meaningful for actual account rows (not "+ new account").
		if m.cursor < 0 || m.cursor >= len(m.items) {
			return m, nil
		}
		if m.items[m.cursor].isNew {
			return m, nil
		}
		m.state = pickerStateRevokeConfirm
		m.statusMsg = ""
		return m, nil
	case "esc":
		return m, func() tea.Msg { return pickerBackMsg{} }
	case "q", "ctrl+c":
		return m, tea.Quit
	}
	return m, nil
}

// updateRevokeConfirm handles the y/n modal that gates revocation.
// Confirming emits revokeAccountMsg — the same message the inner
// scope-view Ctrl+R uses, so the model's revokeAccountMsg handler
// is the single revoke implementation across both entry points.
func (m pickerModel) updateRevokeConfirm(k tea.KeyMsg) (pickerModel, tea.Cmd) {
	switch k.String() {
	case "y", "enter":
		account := m.items[m.cursor].email
		m.state = pickerStateNormal
		return m, func() tea.Msg { return revokeAccountMsg{account: account} }
	case "n", "esc":
		m.state = pickerStateNormal
		return m, nil
	case "ctrl+c":
		return m, tea.Quit
	}
	return m, nil
}

func (m pickerModel) View() string {
	if m.state == pickerStateRevokeConfirm {
		return m.viewRevokeConfirm()
	}
	var b strings.Builder
	b.WriteString(titleStyle.Render("Google accounts"))
	b.WriteString("\n")
	b.WriteString(strings.Repeat("─", 40))
	b.WriteString("\n")
	for i, item := range m.items {
		cursor := "  "
		if i == m.cursor {
			cursor = "> "
		}
		var line string
		if item.isNew {
			line = "+ new account"
		} else {
			line = item.email
			// Append health badge inline so the operator can see at
			// a glance which accounts need attention. Empty health
			// (unchecked) renders without a badge — same as before.
			switch item.health {
			case AccountHealthNeedsReauth:
				line += "  (needs reauth)"
			case AccountHealthUnknown:
				line += "  (?)"
			}
		}
		if i == m.cursor {
			line = selectedStyle.Render(line)
		} else if item.isNew {
			line = mutedStyle.Render(line)
		} else if item.health == AccountHealthNeedsReauth {
			// Visually de-emphasize accounts that need reauth so the
			// healthy ones stand out as the default-action targets.
			// Cursor styling overrides this when hovered.
			line = mutedStyle.Render(line)
		}
		b.WriteString(cursor)
		b.WriteString(line)
		b.WriteString("\n")
	}
	b.WriteString("\n")
	if m.statusMsg != "" {
		b.WriteString(helpStyle.Render(m.statusMsg))
		b.WriteString("\n")
	}
	b.WriteString(helpStyle.Render("↑↓ nav   enter open   r revoke   esc back   q quit"))
	b.WriteString("\n")
	return b.String()
}

func (m pickerModel) viewRevokeConfirm() string {
	var b strings.Builder
	account := m.items[m.cursor].email
	b.WriteString(titleStyle.Render(fmt.Sprintf("Revoke %s?", account)))
	b.WriteString("\n\n")
	b.WriteString(rowDelStyle.Render("  This will revoke ALL Google scopes for this account"))
	b.WriteString("\n")
	b.WriteString(rowDelStyle.Render("  and remove the credential from charon's keychain."))
	b.WriteString("\n\n")
	b.WriteString("  Agents using this account will lose access immediately.\n")
	b.WriteString("  You'll need to re-auth via charon auth to use it again.\n")
	b.WriteString("\n")
	b.WriteString(helpStyle.Render("[y/enter] revoke    [n/esc] cancel"))
	b.WriteString("\n")
	return b.String()
}
