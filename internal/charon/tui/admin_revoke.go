package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xianxu/nous/internal/charon/providers"
	"github.com/xianxu/nous/internal/charon/vault"
)

// adminRevokeModel handles the revoke confirmation modal triggered by
// pressing `r` on either the admin-key row or a project row in the
// entity list.
//
// Two shapes:
//   - rowProject: revoke one minted credential. Calls Provider.RevokeKey
//     (idempotent — already-revoked is treated as success), then deletes
//     the vault entry. Single confirm, no cascade.
//   - rowAdminKey: revoke the admin key itself. Cascade-aware — also
//     lists all minted credentials under the same OrgID that will be
//     orphaned (they keep working at the provider until the user
//     manually revokes them there). Single confirm; cascade-deletes
//     in vault, then removes the admin entry. Does NOT call upstream
//     RevokeKey for the orphaned creds — the user is told they need to
//     do that at the provider's dashboard.
type adminRevokeModel struct {
	providerName string
	provider     providers.Provider
	store        *providers.AdminKeyStore
	vault        vault.Store

	target adminRevokeTarget

	// For rowProject:
	account     string
	projectID   string
	keyID       string

	// For rowAdminKey:
	orgID           string
	cascadeAccounts []string

	state adminRevokeState
	err   error
}

type adminRevokeState int

const (
	revokeStateConfirm adminRevokeState = iota
	revokeStateInProgress
	revokeStateError
)

type adminRevokeTarget int

const (
	revokeProject adminRevokeTarget = iota
	revokeAdminKey
)

// adminRevokeDoneMsg signals the revoke landed (vault is consistent).
// Parent rebuilds the entity list.
type adminRevokeDoneMsg struct{}

// adminRevokeCancelMsg signals the user cancelled the modal.
type adminRevokeCancelMsg struct{}

// adminRevokeResultMsg carries the upstream revoke outcome back to
// the model. Used for project-revoke; admin-key revoke is local-only.
type adminRevokeResultMsg struct {
	err error
}

// newProjectRevokeModel builds a revoke flow targeting a single
// minted project credential. account is the X-Charon-Account name;
// the model reads the credential from vault to find the projectID +
// keyID for the upstream call.
func newProjectRevokeModel(
	providerName string,
	provider providers.Provider,
	store *providers.AdminKeyStore,
	v vault.Store,
	account string,
) (adminRevokeModel, error) {
	cred, err := v.Get(providerName, account)
	if err != nil {
		return adminRevokeModel{}, fmt.Errorf("read credential %s/%s: %w", providerName, account, err)
	}
	if cred.AdminKey == nil {
		return adminRevokeModel{}, fmt.Errorf("credential %s/%s has no AdminKey payload", providerName, account)
	}
	return adminRevokeModel{
		providerName: providerName,
		provider:     provider,
		store:        store,
		vault:        v,
		target:       revokeProject,
		account:      account,
		projectID:    cred.AdminKey.ProjectID,
		keyID:        cred.AdminKey.KeyID,
		state:        revokeStateConfirm,
	}, nil
}

// newAdminKeyRevokeModel builds a revoke flow targeting the
// configured admin key (and its cascade of vault-side orphan
// credentials).
func newAdminKeyRevokeModel(
	providerName string,
	store *providers.AdminKeyStore,
	v vault.Store,
) (adminRevokeModel, error) {
	_, meta, err := store.Get()
	if err != nil {
		return adminRevokeModel{}, fmt.Errorf("read admin key: %w", err)
	}
	creds, err := v.List()
	if err != nil {
		return adminRevokeModel{}, fmt.Errorf("list vault: %w", err)
	}
	var cascade []string
	for _, c := range creds {
		if c.Provider == providerName && c.CredType() == vault.TypeAdminKey &&
			c.AdminKey != nil && c.AdminKey.OrgID == meta.OrgID {
			cascade = append(cascade, c.Account)
		}
	}
	return adminRevokeModel{
		providerName:    providerName,
		store:           store,
		vault:           v,
		target:          revokeAdminKey,
		orgID:           meta.OrgID,
		cascadeAccounts: cascade,
		state:           revokeStateConfirm,
	}, nil
}

func (m adminRevokeModel) Update(msg tea.Msg) (adminRevokeModel, tea.Cmd) {
	switch m.state {
	case revokeStateConfirm:
		return m.updateConfirm(msg)
	case revokeStateInProgress:
		return m.updateInProgress(msg)
	case revokeStateError:
		return m.updateError(msg)
	}
	return m, nil
}

func (m adminRevokeModel) updateConfirm(msg tea.Msg) (adminRevokeModel, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch k.String() {
	case "y", "enter":
		m.state = revokeStateInProgress
		switch m.target {
		case revokeProject:
			provider := m.provider
			store := m.store
			pid := m.projectID
			kid := m.keyID
			return m, func() tea.Msg {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				// Read admin key fresh (rotation may have happened
				// between row render and revoke confirm).
				adminKey, _, err := store.Get()
				if err != nil {
					return adminRevokeResultMsg{err: fmt.Errorf("read admin key: %w", err)}
				}
				err = provider.RevokeKey(ctx, adminKey, pid, kid)
				return adminRevokeResultMsg{err: err}
			}
		case revokeAdminKey:
			// Local-only: cascade-delete vault entries, then drop the
			// admin entry. No upstream call.
			return m, func() tea.Msg {
				return adminRevokeResultMsg{} // synchronous; processed in updateInProgress
			}
		}
	case "n", "esc":
		return m, func() tea.Msg { return adminRevokeCancelMsg{} }
	case "ctrl+c":
		return m, tea.Quit
	}
	return m, nil
}

func (m adminRevokeModel) updateInProgress(msg tea.Msg) (adminRevokeModel, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok && k.String() == "ctrl+c" {
		return m, tea.Quit
	}
	r, ok := msg.(adminRevokeResultMsg)
	if !ok {
		return m, nil
	}

	switch m.target {
	case revokeProject:
		// Upstream errors that aren't "already revoked" surface as
		// errors; ErrAlreadyRevoked is treated as success since the
		// goal is "key gone, vault cleaned up."
		if r.err != nil && !errors.Is(r.err, providers.ErrAlreadyRevoked) {
			m.state = revokeStateError
			m.err = fmt.Errorf("upstream revoke: %w", r.err)
			return m, nil
		}
		if err := m.vault.Delete(m.providerName, m.account); err != nil {
			m.state = revokeStateError
			m.err = fmt.Errorf("delete vault entry: %w", err)
			return m, nil
		}
		return m, func() tea.Msg { return adminRevokeDoneMsg{} }

	case revokeAdminKey:
		// Cascade-delete vault, then drop admin entry. No upstream
		// call — the user is warned in the modal that orphaned API
		// keys keep working at the provider.
		//
		// Continue-and-aggregate: a per-account Delete failure
		// shouldn't abandon the rest of the cascade and leave the
		// vault in a partially-revoked state. Loop through all,
		// collect failures, surface them after.
		var cascadeErrs []string
		for _, account := range m.cascadeAccounts {
			if err := m.vault.Delete(m.providerName, account); err != nil {
				cascadeErrs = append(cascadeErrs, fmt.Sprintf("%s: %v", account, err))
			}
		}
		if len(cascadeErrs) > 0 {
			m.state = revokeStateError
			m.err = fmt.Errorf("cascade-delete partially failed (%d of %d): %s",
				len(cascadeErrs), len(m.cascadeAccounts), strings.Join(cascadeErrs, "; "))
			return m, nil
		}
		if err := m.store.Delete(); err != nil {
			m.state = revokeStateError
			m.err = fmt.Errorf("delete admin entry: %w", err)
			return m, nil
		}
		return m, func() tea.Msg { return adminRevokeDoneMsg{} }
	}
	return m, nil
}

func (m adminRevokeModel) updateError(msg tea.Msg) (adminRevokeModel, tea.Cmd) {
	if _, ok := msg.(tea.KeyMsg); ok {
		// Any key dismisses error → cancel the flow (no further
		// state change). User can retry by pressing `r` again.
		return m, func() tea.Msg { return adminRevokeCancelMsg{} }
	}
	return m, nil
}

func (m adminRevokeModel) View() string {
	switch m.state {
	case revokeStateConfirm:
		return m.viewConfirm()
	case revokeStateInProgress:
		return m.viewInProgress()
	case revokeStateError:
		return m.viewError()
	}
	return ""
}

func (m adminRevokeModel) header() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render(fmt.Sprintf("%s › %s › revoke", appName(), providerLabel(m.providerName))))
	b.WriteString("\n")
	b.WriteString(strings.Repeat("─", 60))
	b.WriteString("\n\n")
	return b.String()
}

func (m adminRevokeModel) viewConfirm() string {
	var b strings.Builder
	b.WriteString(m.header())
	switch m.target {
	case revokeProject:
		b.WriteString(rowDelStyle.Render(fmt.Sprintf("  Revoke %s/%s", m.providerName, m.account)))
		b.WriteString("\n\n")
		b.WriteString(fmt.Sprintf("    %s ID:  %s\n", titleCase(entityTerm(m.providerName)), m.projectID))
		b.WriteString(fmt.Sprintf("    Key ID:      %s\n", m.keyID))
		b.WriteString("\n")
		b.WriteString("  Charon will revoke the API key upstream and remove\n")
		b.WriteString("  the credential from its vault. Agents using\n")
		b.WriteString(fmt.Sprintf("  X-Charon-Account: %s will fail until you re-mint.\n", m.account))
	case revokeAdminKey:
		b.WriteString(rowDelStyle.Render("  Revoke admin key"))
		b.WriteString("\n\n")
		b.WriteString(fmt.Sprintf("    OrgID: %s\n\n", m.orgID))
		if len(m.cascadeAccounts) == 0 {
			b.WriteString("  No minted credentials under this OrgID — clean removal.\n")
		} else {
			b.WriteString(fmt.Sprintf("  This will also remove %d minted %s from charon's vault:\n",
				len(m.cascadeAccounts), entityTermPlural(m.providerName)))
			for _, a := range m.cascadeAccounts {
				b.WriteString(rowDelStyle.Render("    - " + a))
				b.WriteString("\n")
			}
			b.WriteString("\n")
			b.WriteString(rowReqStyle.Render("  The underlying API keys keep working at the provider until\n"))
			b.WriteString(rowReqStyle.Render("  you revoke them at the provider's dashboard. Charon can no\n"))
			b.WriteString(rowReqStyle.Render("  longer revoke them through this admin key after this step."))
		}
	}
	b.WriteString("\n\n")
	b.WriteString(helpStyle.Render("[y/enter] proceed    [n/esc] cancel"))
	return b.String()
}

func (m adminRevokeModel) viewInProgress() string {
	var b strings.Builder
	b.WriteString(m.header())
	b.WriteString("  Revoking...\n\n")
	b.WriteString(helpStyle.Render("(ctrl+c to abort)"))
	return b.String()
}

func (m adminRevokeModel) viewError() string {
	var b strings.Builder
	b.WriteString(m.header())
	b.WriteString(rowDelStyle.Render("  Revoke failed"))
	b.WriteString("\n\n")
	if m.err != nil {
		b.WriteString("  " + m.err.Error())
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(helpStyle.Render("press any key to dismiss"))
	return b.String()
}
