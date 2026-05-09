package tui

import (
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xianxu/nous/internal/charon/providers/catalog"
	"github.com/xianxu/nous/internal/charon/vault"
)

// catalogAccountListModel renders the per-catalog-provider account
// list (#15 M4b). One row per stored credential plus a trailing
// `+ add account` affordance. Cursor + nav mirror adminKeyListModel
// for consistency.
type catalogAccountListModel struct {
	entry catalog.Entry
	rows  []catalogAccountListRow

	cursor    int
	statusMsg string
}

type catalogAccountListRow struct {
	isAddNew bool

	account string
	hint    string // partial-key suffix, "x-…XYZ" style
}

// catalogAccountListBackMsg signals navigate back to provider picker.
type catalogAccountListBackMsg struct{}

// catalogAccountAddMsg signals the user wants to add another account
// under this catalog provider — re-enter the paste flow with the
// entry pre-selected.
type catalogAccountAddMsg struct {
	entry catalog.Entry
}

// catalogRevokeRequestMsg signals "open the revoke confirm modal" for
// a TypeCatalog credential. account is the X-Charon-Account name.
type catalogRevokeRequestMsg struct {
	entry   catalog.Entry
	account string
}

// newCatalogAccountListModel builds the row list from vault state for
// the given catalog entry. Errors propagate from vault.List; missing
// rows is fine (model just shows the trailing + add row).
//
// Cost note: each call invokes vault.List, which on the prod
// keychain backend issues N keychain reads (one per entry). Typical
// users have <20 entries and reads from charon's own ACL'd entries
// are silent (see internal/vault/keychain/keychain_darwin.go:97);
// per-back-nav refresh is intentional so deletions/additions made in
// other surfaces (CLI, another TUI session) are reflected. If
// keychain churn ever shows up in practice, cache vault.List output
// at the model level and invalidate on vault writes.
func newCatalogAccountListModel(entry catalog.Entry, v vault.Store) (catalogAccountListModel, error) {
	creds, err := v.List()
	if err != nil {
		return catalogAccountListModel{}, fmt.Errorf("list credentials: %w", err)
	}
	m := catalogAccountListModel{entry: entry}
	type acct struct{ account, hint string }
	var accts []acct
	for _, c := range creds {
		if c.Provider != entry.ID || c.CredType() != vault.TypeCatalog || c.Catalog == nil {
			continue
		}
		accts = append(accts, acct{
			account: c.Account,
			hint:    hintFromKeyMaterial(c.Catalog.KeyMaterial),
		})
	}
	sort.Slice(accts, func(i, j int) bool { return accts[i].account < accts[j].account })
	for _, a := range accts {
		m.rows = append(m.rows, catalogAccountListRow{account: a.account, hint: a.hint})
	}
	m.rows = append(m.rows, catalogAccountListRow{isAddNew: true})
	return m, nil
}

func (m catalogAccountListModel) Update(msg tea.Msg) (catalogAccountListModel, tea.Cmd) {
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch keyMsg.String() {
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
			m.statusMsg = ""
		}
	case "down", "j":
		if m.cursor < len(m.rows)-1 {
			m.cursor++
			m.statusMsg = ""
		}
	case "enter":
		row := m.rows[m.cursor]
		// Enter is only meaningful on the "+ add account" row.
		// Catalog credentials have no detail screen — there's
		// nothing to "open" for an existing account row, so enter
		// is intentionally a no-op there. Revoke is reachable
		// only via `r` so the destructive path doesn't share a
		// keybinding with a benign "open" verb the way the admin-
		// key list does.
		if row.isAddNew {
			entry := m.entry
			return m, func() tea.Msg { return catalogAccountAddMsg{entry: entry} }
		}
		return m, nil
	case "r":
		row := m.rows[m.cursor]
		if row.isAddNew {
			return m, nil
		}
		entry := m.entry
		account := row.account
		return m, func() tea.Msg {
			return catalogRevokeRequestMsg{entry: entry, account: account}
		}
	case "esc":
		return m, func() tea.Msg { return catalogAccountListBackMsg{} }
	case "q", "ctrl+c":
		return m, tea.Quit
	}
	return m, nil
}

func (m catalogAccountListModel) View() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render(fmt.Sprintf("%s › %s", appName(), providerLabel(m.entry.ID))))
	b.WriteString("\n")
	b.WriteString(mutedStyle.Render("Accounts"))
	b.WriteString("\n")
	b.WriteString(strings.Repeat("─", 60))
	b.WriteString("\n")

	for i, row := range m.rows {
		cursor := "  "
		if i == m.cursor {
			cursor = "> "
		}
		var line string
		if row.isAddNew {
			line = "+ add account"
		} else {
			acct := padOrTrunc(row.account, 24)
			hint := row.hint
			if hint == "" {
				hint = "(no key)"
			}
			line = fmt.Sprintf("%s %s", acct, hint)
		}
		if i == m.cursor {
			line = selectedStyle.Render(line)
		} else if row.isAddNew {
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
	b.WriteString(helpStyle.Render("↑↓ nav   r revoke   esc back   q quit"))
	return b.String()
}
