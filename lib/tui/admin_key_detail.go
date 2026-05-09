package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xianxu/nous/lib/provider/vault"
)

// adminKeyDetailModel is the per-key drill-in view (Screen 3b in
// the design doc). Shows full credential metadata: name, project
// (human + opaque id), upstream key id, redacted key prefix,
// creation timestamp, owning admin-key org context. Action keys:
//   r → opens the revoke confirm modal (same flow as the entity
//        list's `r`)
//   esc → back to the entity list
//   q / ctrl+c → quit
//
// No mutation happens in this model — it's read-only display.
// Revoke routes through the existing adminRevokeRequestMsg path so
// there's a single revoke implementation across entry points.
type adminKeyDetailModel struct {
	provider string
	account  string

	projectName string
	projectID   string
	keyID       string
	keyHint     string
	createdAt   time.Time
	orgID       string
	orgLabel    string
	orgName     string
}

// adminKeyDetailBackMsg signals "back to the entity list."
type adminKeyDetailBackMsg struct{}

// newAdminKeyDetailModel reads the credential from the vault and
// builds the view-model. Returns an error if the credential is
// missing or the AdminKey payload isn't populated — the entity list
// only emits the open-detail request for visible admin-key rows, so
// either error is a real bug.
func newAdminKeyDetailModel(provider string, v vault.Store, account string) (adminKeyDetailModel, error) {
	cred, err := v.Get(provider, account)
	if err != nil {
		return adminKeyDetailModel{}, fmt.Errorf("read credential %s/%s: %w", provider, account, err)
	}
	if cred.AdminKey == nil {
		return adminKeyDetailModel{}, fmt.Errorf("credential %s/%s has no AdminKey payload", provider, account)
	}
	return adminKeyDetailModel{
		provider:    provider,
		account:     account,
		projectName: cred.AdminKey.ProjectName,
		projectID:   cred.AdminKey.ProjectID,
		keyID:       cred.AdminKey.KeyID,
		keyHint:     hintFromKeyMaterial(cred.AdminKey.KeyMaterial),
		createdAt:   cred.AdminKey.CreatedAt,
		orgID:       cred.AdminKey.OrgID,
		orgLabel:    cred.AdminKey.OrgLabel,
		orgName:     cred.AdminKey.OrgName,
	}, nil
}

func (m adminKeyDetailModel) Update(msg tea.Msg) (adminKeyDetailModel, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch k.String() {
	case "r":
		// Reuse the existing revoke flow — same confirm modal +
		// upstream call + vault delete + entity-list refresh as
		// pressing `r` from the entity list.
		provider := m.provider
		account := m.account
		return m, func() tea.Msg {
			return adminRevokeRequestMsg{
				provider: provider,
				target:   revokeProject,
				account:  account,
			}
		}
	case "esc":
		return m, func() tea.Msg { return adminKeyDetailBackMsg{} }
	case "q", "ctrl+c":
		return m, tea.Quit
	}
	return m, nil
}

func (m adminKeyDetailModel) View() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render(fmt.Sprintf("%s › %s › %s",
		appName(), providerLabel(m.provider), m.account)))
	b.WriteString("\n")
	b.WriteString(mutedStyle.Render("Key info"))
	b.WriteString("\n")
	b.WriteString(strings.Repeat("─", 60))
	b.WriteString("\n\n")

	// Two-column layout. Left column fixed width so values align
	// regardless of how many rows are present.
	row := func(label, value string) {
		b.WriteString("  ")
		b.WriteString(padOrTrunc(label, 14))
		b.WriteString(value)
		b.WriteString("\n")
	}

	row("Name:", m.account)
	row("Project:", m.projectDisplay())
	if m.keyID != "" {
		row("Key ID:", m.keyID)
	}
	row("Key prefix:", m.keyHint)
	if !m.createdAt.IsZero() {
		row("Created:", m.createdAt.Format("2006-01-02 15:04"))
	}
	b.WriteString("\n")
	row("Org:", m.orgDisplay())
	if m.orgID != "" {
		row("Org ID:", m.orgID)
	}

	b.WriteString("\n")
	b.WriteString(mutedStyle.Render(
		"  Key material is in keychain and never shown after mint.\n" +
			"  To rotate, revoke and re-mint."))
	b.WriteString("\n\n")
	b.WriteString(helpStyle.Render("r revoke   esc back   q quit"))
	return b.String()
}

// projectDisplay shows "name (id)" when both are present, falling
// back to whichever is non-empty. Mirrors the row formatter in the
// entity list so the same credential reads consistently across
// screens.
func (m adminKeyDetailModel) projectDisplay() string {
	switch {
	case m.projectName != "" && m.projectID != "":
		return fmt.Sprintf("%s (%s)", m.projectName, m.projectID)
	case m.projectName != "":
		return m.projectName
	case m.projectID != "":
		return m.projectID
	}
	return "(unknown)"
}

// orgDisplay reuses the same format-fallback chain as the admin-key
// row in the entity list, so the org label looks identical from both
// vantage points.
func (m adminKeyDetailModel) orgDisplay() string {
	switch {
	case m.orgLabel != "" && m.orgName != "":
		return m.orgLabel + " / " + m.orgName
	case m.orgLabel != "":
		return m.orgLabel
	case m.orgName != "":
		return m.orgName
	case m.orgID != "":
		return m.orgID
	}
	return "(unlabeled)"
}
