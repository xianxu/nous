package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/xianxu/nous/internal/charon/providers"
	"github.com/xianxu/nous/internal/charon/vault"
)

// adminKeyPasteModel drives the admin-key first-time-setup and
// replace flows for an admin-key provider (OpenAI, Anthropic).
//
// Internal state machine:
//
//	editingLabel ──enter──▶ editingKey ──enter──▶ discovering
//	                          │                       │
//	                          esc                    err ──▶ error ──any-key──▶ editingKey
//	                          ▼                       │
//	                     editingLabel            success
//	                                                  │
//	                            ┌─────────────────────┼─────────────────────┐
//	                            ▼                     ▼                     ▼
//	             not replace OR same OrgID    different OrgID         (no other branch)
//	                  → write + done           → replaceConfirm
//	                                              │      │
//	                                            y │      │ n
//	                                              ▼      ▼
//	                                          cascade   cancel
//	                                          + write
//	                                          + done
//
// On success, emits adminKeyPasteDoneMsg; the parent rebuilds the
// entity list. On cancel (esc from editingLabel), emits
// adminKeyPasteCancelMsg with no side effects.
type adminKeyPasteModel struct {
	providerName string
	provider     providers.Provider
	store        *providers.AdminKeyStore
	vault        vault.Store

	state adminKeyPasteState

	labelInput textinput.Model
	keyInput   textinput.Model

	// Replace mode: when true, an admin key already exists for this
	// provider; existingOrgID is its captured OrgID, used to pick
	// between same-org silent rotate vs different-org confirm.
	isReplace     bool
	existingOrgID string

	// Discovery results, held while showing replaceConfirm.
	pendingAdminKey string
	pendingOrgID    string
	pendingOrgName  string

	// Accounts that will be cascade-deleted on different-org
	// confirm. Surfaced in the modal so the user sees what they're
	// signing up for.
	cascadeAccounts []string

	err error

	// errOnReturn lets the test seam set a different "key" after an
	// error — defensive only; not currently exercised.
	width, height int
}

type adminKeyPasteState int

const (
	pasteStateEditingLabel adminKeyPasteState = iota
	pasteStateEditingKey
	pasteStateDiscovering
	pasteStateError
	pasteStateReplaceConfirm
)

// adminKeyPasteDoneMsg signals the paste flow stored a new admin key
// successfully. Parent should rebuild the entity list.
type adminKeyPasteDoneMsg struct{}

// adminKeyPasteCancelMsg signals the user cancelled out of the flow
// before any persistent state change.
type adminKeyPasteCancelMsg struct{}

// adminKeyDiscoveredMsg carries the result of provider.DiscoverOrg
// back to the model after the async call returns.
type adminKeyDiscoveredMsg struct {
	adminKey string // the key the user pasted, threaded through so the model doesn't have to remember
	orgID    string
	orgName  string
	err      error
}

// newAdminKeyPasteModel constructs a paste flow.
//
// First-time setup: pass isReplace=false. existingOrgID is ignored.
// Replace mode: pass isReplace=true with the OrgID from the stored
// AdminMeta. The flow's behavior diverges only after discovery —
// editing UX is identical.
func newAdminKeyPasteModel(
	providerName string,
	provider providers.Provider,
	store *providers.AdminKeyStore,
	v vault.Store,
	isReplace bool,
	existingOrgID string,
) adminKeyPasteModel {
	label := textinput.New()
	label.Placeholder = "your-email@example.com"
	label.Prompt = "  label> "
	label.CharLimit = 128
	label.Width = 60
	label.Focus()

	key := textinput.New()
	key.Placeholder = "sk-admin-…"
	key.Prompt = "  key> "
	key.CharLimit = 256
	key.Width = 60
	key.EchoMode = textinput.EchoPassword
	key.EchoCharacter = '•'

	return adminKeyPasteModel{
		providerName:  providerName,
		provider:      provider,
		store:         store,
		vault:         v,
		state:         pasteStateEditingLabel,
		labelInput:    label,
		keyInput:      key,
		isReplace:     isReplace,
		existingOrgID: existingOrgID,
	}
}

func (m adminKeyPasteModel) Update(msg tea.Msg) (adminKeyPasteModel, tea.Cmd) {
	if ws, ok := msg.(tea.WindowSizeMsg); ok {
		m.width, m.height = ws.Width, ws.Height
		return m, nil
	}
	switch m.state {
	case pasteStateEditingLabel:
		return m.updateEditingLabel(msg)
	case pasteStateEditingKey:
		return m.updateEditingKey(msg)
	case pasteStateDiscovering:
		return m.updateDiscovering(msg)
	case pasteStateError:
		return m.updateError(msg)
	case pasteStateReplaceConfirm:
		return m.updateReplaceConfirm(msg)
	}
	return m, nil
}

func (m adminKeyPasteModel) updateEditingLabel(msg tea.Msg) (adminKeyPasteModel, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "enter":
			if strings.TrimSpace(m.labelInput.Value()) == "" {
				return m, nil // require a non-empty label
			}
			m.state = pasteStateEditingKey
			m.labelInput.Blur()
			m.keyInput.Focus()
			return m, nil
		case "esc":
			return m, func() tea.Msg { return adminKeyPasteCancelMsg{} }
		case "ctrl+c":
			return m, tea.Quit
		}
	}
	var cmd tea.Cmd
	m.labelInput, cmd = m.labelInput.Update(msg)
	return m, cmd
}

func (m adminKeyPasteModel) updateEditingKey(msg tea.Msg) (adminKeyPasteModel, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "enter":
			pasted := strings.TrimSpace(m.keyInput.Value())
			if pasted == "" {
				return m, nil
			}
			m.state = pasteStateDiscovering
			adminKey := pasted
			provider := m.provider
			return m, func() tea.Msg {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				orgID, orgName, err := provider.DiscoverOrg(ctx, adminKey)
				return adminKeyDiscoveredMsg{
					adminKey: adminKey,
					orgID:    orgID,
					orgName:  orgName,
					err:      err,
				}
			}
		case "esc":
			// Back up to label edit; preserve label, clear key.
			m.state = pasteStateEditingLabel
			m.keyInput.Blur()
			m.keyInput.Reset()
			m.labelInput.Focus()
			return m, nil
		case "ctrl+c":
			return m, tea.Quit
		}
	}
	var cmd tea.Cmd
	m.keyInput, cmd = m.keyInput.Update(msg)
	return m, cmd
}

func (m adminKeyPasteModel) updateDiscovering(msg tea.Msg) (adminKeyPasteModel, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok && k.String() == "ctrl+c" {
		return m, tea.Quit
	}
	dm, ok := msg.(adminKeyDiscoveredMsg)
	if !ok {
		return m, nil
	}
	if dm.err != nil {
		m.state = pasteStateError
		m.err = dm.err
		return m, nil
	}

	// Discovery succeeded.
	m.pendingAdminKey = dm.adminKey
	m.pendingOrgID = dm.orgID
	m.pendingOrgName = dm.orgName

	// Same OrgID (or first-time) → silent commit.
	if !m.isReplace || dm.orgID == m.existingOrgID {
		return m.commit()
	}

	// Different OrgID — gather cascade accounts and show confirm
	// modal. Read vault here (small) so the modal can name the
	// affected credentials.
	creds, err := m.vault.List()
	if err != nil {
		m.state = pasteStateError
		m.err = fmt.Errorf("list vault for cascade preview: %w", err)
		return m, nil
	}
	for _, c := range creds {
		if c.Provider == m.providerName && c.CredType() == vault.TypeAdminKey &&
			c.AdminKey != nil && c.AdminKey.OrgID == m.existingOrgID {
			m.cascadeAccounts = append(m.cascadeAccounts, c.Account)
		}
	}
	m.state = pasteStateReplaceConfirm
	return m, nil
}

func (m adminKeyPasteModel) updateError(msg tea.Msg) (adminKeyPasteModel, tea.Cmd) {
	if _, ok := msg.(tea.KeyMsg); ok {
		// Any key dismisses the error and returns to key edit (the
		// most common cause is wrong key bytes). Clear the previous
		// key so the user can re-paste.
		m.state = pasteStateEditingKey
		m.err = nil
		m.keyInput.Reset()
		m.keyInput.Focus()
	}
	return m, nil
}

func (m adminKeyPasteModel) updateReplaceConfirm(msg tea.Msg) (adminKeyPasteModel, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch k.String() {
	case "y", "enter":
		// Cascade-delete then commit. Continue-and-aggregate: a
		// per-account Delete failure shouldn't abandon the rest of
		// the cascade — that leaves the user with inconsistent
		// partial cleanup. Collect all failures, surface them after
		// the admin write so the user sees the full picture.
		var cascadeErrs []string
		for _, account := range m.cascadeAccounts {
			if err := m.vault.Delete(m.providerName, account); err != nil {
				cascadeErrs = append(cascadeErrs, fmt.Sprintf("%s: %v", account, err))
			}
		}
		if len(cascadeErrs) > 0 {
			// Don't proceed to admin write — the vault is now in an
			// inconsistent state (some old creds gone, some present).
			// Surface the error; user retries with the same paste flow.
			m.state = pasteStateError
			m.err = fmt.Errorf("cascade delete partially failed (%d of %d): %s",
				len(cascadeErrs), len(m.cascadeAccounts), strings.Join(cascadeErrs, "; "))
			return m, nil
		}
		// Old admin entry is overwritten by Set below; if MVP layout
		// ever changes to per-OrgID keying we'd also delete the old
		// entry here. Today: Set replaces in place.
		return m.commit()
	case "n", "esc":
		// Cancel — drop pending discovery results.
		m.pendingAdminKey = ""
		m.pendingOrgID = ""
		m.pendingOrgName = ""
		m.cascadeAccounts = nil
		return m, func() tea.Msg { return adminKeyPasteCancelMsg{} }
	case "ctrl+c":
		return m, tea.Quit
	}
	return m, nil
}

// commit writes the discovered admin key + meta to the store and
// emits the done message. Shared between same-org rotate and
// different-org-confirmed paths.
func (m adminKeyPasteModel) commit() (adminKeyPasteModel, tea.Cmd) {
	meta := providers.AdminMeta{
		OrgID:    m.pendingOrgID,
		OrgLabel: strings.TrimSpace(m.labelInput.Value()),
		OrgName:  m.pendingOrgName,
	}
	if err := m.store.Set(m.pendingAdminKey, meta); err != nil {
		m.state = pasteStateError
		m.err = fmt.Errorf("store admin key: %w", err)
		return m, nil
	}
	return m, func() tea.Msg { return adminKeyPasteDoneMsg{} }
}

func (m adminKeyPasteModel) View() string {
	switch m.state {
	case pasteStateEditingLabel:
		return m.viewEditingLabel()
	case pasteStateEditingKey:
		return m.viewEditingKey()
	case pasteStateDiscovering:
		return m.viewDiscovering()
	case pasteStateError:
		return m.viewError()
	case pasteStateReplaceConfirm:
		return m.viewReplaceConfirm()
	}
	return ""
}

func (m adminKeyPasteModel) viewEditingLabel() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render(fmt.Sprintf("%s › %s › admin key", appName(), providerLabel(m.providerName))))
	b.WriteString("\n")
	if m.isReplace {
		b.WriteString(mutedStyle.Render("Replace admin key (1/2 — informational label)"))
	} else {
		b.WriteString(mutedStyle.Render("Configure admin key (1/2 — informational label)"))
	}
	b.WriteString("\n")
	b.WriteString(strings.Repeat("─", 60))
	b.WriteString("\n\n")
	b.WriteString("  Admin key URL:\n")
	b.WriteString(mutedStyle.Render("    " + adminKeyURL(m.providerName)))
	b.WriteString("\n\n")
	b.WriteString("  Email/label for this admin key (informational):\n")
	b.WriteString(m.labelInput.View())
	b.WriteString("\n\n")
	b.WriteString(helpStyle.Render("enter: continue   esc: cancel"))
	return b.String()
}

func (m adminKeyPasteModel) viewEditingKey() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render(fmt.Sprintf("%s › %s › admin key", appName(), providerLabel(m.providerName))))
	b.WriteString("\n")
	if m.isReplace {
		b.WriteString(mutedStyle.Render("Replace admin key (2/2 — paste key)"))
	} else {
		b.WriteString(mutedStyle.Render("Configure admin key (2/2 — paste key)"))
	}
	b.WriteString("\n")
	b.WriteString(strings.Repeat("─", 60))
	b.WriteString("\n\n")
	b.WriteString(fmt.Sprintf("  Label: %s\n\n", strings.TrimSpace(m.labelInput.Value())))
	b.WriteString("  Paste admin key (input is hidden):\n")
	b.WriteString(m.keyInput.View())
	b.WriteString("\n\n")
	b.WriteString(helpStyle.Render("enter: discover org & store   esc: back   ctrl+c: quit"))
	return b.String()
}

func (m adminKeyPasteModel) viewDiscovering() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render(fmt.Sprintf("%s › %s › admin key", appName(), providerLabel(m.providerName))))
	b.WriteString("\n\n")
	b.WriteString("  Discovering organization...\n")
	b.WriteString("\n")
	b.WriteString(helpStyle.Render("(ctrl+c to abort)"))
	return b.String()
}

func (m adminKeyPasteModel) viewError() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render(fmt.Sprintf("%s › %s › admin key — failed", appName(), providerLabel(m.providerName))))
	b.WriteString("\n\n")
	if m.err != nil {
		// Surface a friendlier framing for the most common failure
		// (rejected key) without losing the upstream message.
		if errors.Is(m.err, providers.ErrInvalidAdminKey) {
			b.WriteString(rowDelStyle.Render("  Admin key rejected by upstream — likely incorrect."))
			b.WriteString("\n")
			b.WriteString(mutedStyle.Render("  (charon called " + adminKeyDiscoveryEndpoint(m.providerName) + " and got 401)"))
		} else {
			b.WriteString(rowDelStyle.Render("  " + m.err.Error()))
		}
	}
	b.WriteString("\n\n")
	b.WriteString(helpStyle.Render("press any key to retry"))
	return b.String()
}

func (m adminKeyPasteModel) viewReplaceConfirm() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Replace admin key"))
	b.WriteString("\n\n")
	b.WriteString("  The new admin key is for a ")
	b.WriteString(rowDelStyle.Render("different organization"))
	b.WriteString(".\n\n")
	b.WriteString(fmt.Sprintf("    Current OrgID:  %s\n", m.existingOrgID))
	b.WriteString(fmt.Sprintf("    New OrgID:      %s (%s)\n", m.pendingOrgID, m.pendingOrgName))
	b.WriteString("\n")

	if len(m.cascadeAccounts) == 0 {
		b.WriteString("  No minted credentials to remove from charon.\n")
	} else {
		b.WriteString(fmt.Sprintf("  Charon will remove %d minted %s from its vault:\n",
			len(m.cascadeAccounts), entityTermPlural(m.providerName)))
		for _, a := range m.cascadeAccounts {
			b.WriteString(rowDelStyle.Render("    - " + a))
			b.WriteString("\n")
		}
		b.WriteString("\n")
		b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("178")).Render(
			"  The underlying API keys keep working at the provider until\n" +
				"  you revoke them at the provider's dashboard. Charon will\n" +
				"  no longer be able to revoke them through this admin key."))
		b.WriteString("\n\n")
	}
	b.WriteString(helpStyle.Render("[y/enter] proceed    [n/esc] cancel"))
	return b.String()
}

// adminKeyURL returns the URL the user should open to grab a new
// admin key. Surfaced in the paste-label step so the user knows
// where to go without leaving charon.
func adminKeyURL(provider string) string {
	switch provider {
	case "openai":
		return "https://platform.openai.com/settings/organization/admin-keys"
	case "anthropic":
		return "https://console.anthropic.com/settings/admin-keys"
	}
	return ""
}

// adminKeyDiscoveryEndpoint surfaces the per-provider endpoint name
// in the error message so the user can correlate with the provider's
// own audit logs if the key is rejected.
func adminKeyDiscoveryEndpoint(provider string) string {
	switch provider {
	case "openai":
		return "GET /v1/organization/projects?limit=1"
	case "anthropic":
		return "GET /v1/organizations/me"
	}
	return "discovery endpoint"
}
