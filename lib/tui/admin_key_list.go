package tui

import (
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/xianxu/nous/lib/provider/providers"
	"github.com/xianxu/nous/lib/provider/vault"
)

// adminKeyListRow is one row in the admin-key entity list. Three
// kinds: the admin-key state row at top, project rows in the middle,
// and the trailing "+ new project" / "+ new workspace" affordance.
type adminKeyListRow struct {
	kind adminKeyRowKind

	// adminKeyRow fields:
	adminKeySet bool
	adminLabel  string // "xianxu@gmail.com / acme-inc" or "not set — press enter to configure"

	// keyRow fields:
	account     string // the X-Charon-Account header value (key name)
	projectName string // human-readable upstream project / workspace name
	projectID   string // proj_… / ws_… — kept for revoke; not displayed primary
	keyHint     string // "sk-…xyz" — derived from KeyMaterial prefix+suffix
}

type adminKeyRowKind int

const (
	rowAdminKey adminKeyRowKind = iota
	rowProject
	rowAddNew
)

// adminKeyListModel renders the keys list for admin-key providers
// (OpenAI / Anthropic). Each row is a charon-managed key (name,
// upstream container, key hint). The list is key-centric — projects
// (OpenAI) / workspaces (Anthropic) are just attributes on each row,
// not their own navigation level. Users add a project via the mint
// flow when picking "+ create new project" in step 2.
type adminKeyListModel struct {
	provider     string // "openai" / "anthropic"
	entityPlural string // "Keys" — universal across admin-key providers
	entityAdd    string // "+ new key" — universal

	rows        []adminKeyListRow
	cursor      int
	adminKeySet bool

	// adminOrgID is the configured admin key's OrgID (empty when
	// adminKeySet is false). Threaded into the paste flow's replace
	// path so the same-org-vs-different-org compare doesn't need a
	// second store.Get round-trip.
	adminOrgID string

	// adminLabelStr is the human label shown on the admin-key row
	// (`<OrgLabel> / <OrgName>` when set, fallback otherwise).
	adminLabelStr string

	// transient status (shown below the help line)
	statusMsg string
}

// adminKeyListBackMsg signals "navigate back to provider picker."
type adminKeyListBackMsg struct{}

// adminKeyPasteRequestMsg signals "open the admin-key paste flow."
// Emitted on enter against the admin-key row. Replace mode is true
// iff the row's adminKeySet flag is true.
type adminKeyPasteRequestMsg struct {
	provider      string
	isReplace     bool
	existingOrgID string
}

// adminMintRequestMsg signals "open the mint flow." Emitted on enter
// against the `+ new project` / `+ new workspace` row when the
// admin key is configured.
type adminMintRequestMsg struct {
	provider string
}

// adminRevokeRequestMsg signals "open the revoke confirm modal."
// Emitted on `r` against either an admin-key row or a project row.
// Target picks the model variant.
type adminRevokeRequestMsg struct {
	provider string
	target   adminRevokeTarget // revokeProject or revokeAdminKey
	account  string            // populated when target == revokeProject
}

// adminKeyDetailRequestMsg signals "open the per-key detail screen."
// Emitted on enter against a project (key) row in the entity list.
type adminKeyDetailRequestMsg struct {
	provider string
	account  string
}

// newAdminKeyListModel builds the model from the vault + admin-key
// store state. Errors propagate from vault.List; missing admin key is
// not an error (the row just renders red).
func newAdminKeyListModel(
	provider string,
	v vault.Store,
	store *providers.AdminKeyStore,
) (adminKeyListModel, error) {
	m := adminKeyListModel{
		provider:     provider,
		entityPlural: "Keys",
		entityAdd:    "+ new key",
	}

	// Admin-key state row
	if store != nil && store.IsSet() {
		_, meta, err := store.Get()
		if err == nil {
			m.adminKeySet = true
			m.adminLabelStr = formatAdminLabel(meta)
			m.adminOrgID = meta.OrgID
		} else {
			// Corrupt-meta path: don't claim the admin key is set; the
			// user needs to re-paste.
			m.adminKeySet = false
			m.adminLabelStr = "(corrupt meta — re-paste admin key)"
		}
	}
	m.rows = append(m.rows, adminKeyListRow{
		kind:        rowAdminKey,
		adminKeySet: m.adminKeySet,
		adminLabel:  m.adminLabelStr,
	})

	// Key rows: vault entries with Type==admin-key for this provider.
	// vault.Store.List returns full credentials minus AccessToken
	// across all backends (see vault.Store interface contract), so
	// we can filter on the payload directly.
	creds, err := v.List()
	if err != nil {
		return adminKeyListModel{}, fmt.Errorf("list credentials: %w", err)
	}
	type keyItem struct {
		account, projectName, projectID, hint string
	}
	var keys []keyItem
	for _, c := range creds {
		if c.Provider != provider || c.CredType() != vault.TypeAdminKey || c.AdminKey == nil {
			continue
		}
		keys = append(keys, keyItem{
			account:     c.Account,
			projectName: c.AdminKey.ProjectName,
			projectID:   c.AdminKey.ProjectID,
			hint:        hintFromKeyMaterial(c.AdminKey.KeyMaterial),
		})
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].account < keys[j].account })
	for _, k := range keys {
		m.rows = append(m.rows, adminKeyListRow{
			kind:        rowProject,
			account:     k.account,
			projectName: k.projectName,
			projectID:   k.projectID,
			keyHint:     k.hint,
		})
	}

	// Trailing add-new affordance.
	m.rows = append(m.rows, adminKeyListRow{kind: rowAddNew})

	return m, nil
}

// formatAdminLabel produces "<label> / <name>" with safe fallbacks.
func formatAdminLabel(meta providers.AdminMeta) string {
	switch {
	case meta.OrgLabel != "" && meta.OrgName != "":
		return meta.OrgLabel + " / " + meta.OrgName
	case meta.OrgLabel != "":
		return meta.OrgLabel
	case meta.OrgName != "":
		return meta.OrgName
	case meta.OrgID != "":
		// Truncate the opaque OrgID for display — full id isn't useful
		// to the eye and may overflow the row width.
		if len(meta.OrgID) > 20 {
			return meta.OrgID[:20] + "…"
		}
		return meta.OrgID
	}
	return "(unlabeled)"
}

// hintFromKeyMaterial builds a "sk-…xyz" pattern from the captured key
// material so the list shows a recognizable suffix without exposing
// the secret. Mirrors the partial-key-hint convention from the
// upstream admin APIs.
func hintFromKeyMaterial(material string) string {
	if material == "" {
		return ""
	}
	// First few chars (the prefix) are not secret; last 3 are
	// recognizable but don't compromise the secret.
	const prefixLen = 3
	const suffixLen = 3
	if len(material) <= prefixLen+suffixLen+1 {
		return material // too short to redact meaningfully — surface as-is
	}
	return material[:prefixLen] + "…" + material[len(material)-suffixLen:]
}

func (m adminKeyListModel) Update(msg tea.Msg) (adminKeyListModel, tea.Cmd) {
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
		switch {
		case row.kind == rowAdminKey:
			// First-time setup or replace — same flow, isReplace
			// branches the post-discovery path.
			provider := m.provider
			isReplace := m.adminKeySet
			existing := m.adminOrgID
			return m, func() tea.Msg {
				return adminKeyPasteRequestMsg{
					provider:      provider,
					isReplace:     isReplace,
					existingOrgID: existing,
				}
			}
		case row.kind == rowAddNew && !m.adminKeySet:
			m.statusMsg = "set the admin key first — see the row above"
			return m, nil
		case row.kind == rowAddNew:
			provider := m.provider
			return m, func() tea.Msg {
				return adminMintRequestMsg{provider: provider}
			}
		case row.kind == rowProject:
			provider := m.provider
			account := row.account
			return m, func() tea.Msg {
				return adminKeyDetailRequestMsg{
					provider: provider,
					account:  account,
				}
			}
		}
		return m, nil
	case "r":
		row := m.rows[m.cursor]
		switch {
		case row.kind == rowAddNew:
			return m, nil
		case row.kind == rowAdminKey && !m.adminKeySet:
			// Nothing to revoke when no admin key is configured.
			return m, nil
		case row.kind == rowAdminKey:
			provider := m.provider
			return m, func() tea.Msg {
				return adminRevokeRequestMsg{provider: provider, target: revokeAdminKey}
			}
		case row.kind == rowProject:
			provider := m.provider
			account := row.account
			return m, func() tea.Msg {
				return adminRevokeRequestMsg{
					provider: provider,
					target:   revokeProject,
					account:  account,
				}
			}
		}
		return m, nil
	case "esc":
		return m, func() tea.Msg { return adminKeyListBackMsg{} }
	case "q":
		return m, tea.Quit
	case "ctrl+c":
		return m, tea.Quit
	}
	return m, nil
}

func (m adminKeyListModel) View() string {
	var b strings.Builder

	b.WriteString(titleStyle.Render(fmt.Sprintf("%s › %s", appName(), providerLabel(m.provider))))
	b.WriteString("\n")
	b.WriteString(mutedStyle.Render(m.entityPlural))
	b.WriteString("\n")
	b.WriteString(strings.Repeat("─", 60))
	b.WriteString("\n")

	for i, row := range m.rows {
		cursor := "  "
		if i == m.cursor {
			cursor = "> "
		}
		var line string
		switch row.kind {
		case rowAdminKey:
			line = renderAdminKeyRow(row)
		case rowProject:
			line = renderProjectRow(row)
		case rowAddNew:
			line = m.entityAdd
			if !m.adminKeySet {
				line = mutedStyle.Render(line) + mutedStyle.Render("   (admin key required — see above)")
			}
		}
		if i == m.cursor && row.kind != rowAdminKey {
			line = selectedStyle.Render(line)
		} else if i == m.cursor && row.kind == rowAdminKey {
			// Don't selectedStyle-overlay the admin row (the colored
			// glyph would be obscured). Bold + prefix arrow is enough.
			line = lipgloss.NewStyle().Bold(true).Render(line)
		}
		b.WriteString(cursor)
		b.WriteString(line)
		b.WriteString("\n")
		// Visual separator between admin-key row and the rest.
		if row.kind == rowAdminKey {
			b.WriteString("\n")
		}
	}

	b.WriteString("\n")
	if m.statusMsg != "" {
		b.WriteString(helpStyle.Render(m.statusMsg))
		b.WriteString("\n")
	}
	b.WriteString(helpStyle.Render("↑↓ nav   enter open   r revoke   esc back   q quit"))
	return b.String()
}

func renderAdminKeyRow(row adminKeyListRow) string {
	if row.adminKeySet {
		glyph := lipgloss.NewStyle().Foreground(lipgloss.Color("70")).Render("●")
		return fmt.Sprintf("%s Admin key   %s", glyph, row.adminLabel)
	}
	glyph := lipgloss.NewStyle().Foreground(lipgloss.Color("167")).Render("○")
	return fmt.Sprintf("%s Admin key   not set — press enter to configure", glyph)
}

func renderProjectRow(row adminKeyListRow) string {
	// Columns: name (24) project (24) keyHint. Project column shows
	// the human-readable name; the upstream ID is available via the
	// detail screen for revoke etc. If ProjectName is empty (older
	// vault entries pre-#13 mint flow), fall back to a truncated ID.
	acct := padOrTrunc(row.account, 24)
	proj := row.projectName
	if proj == "" {
		proj = row.projectID
	}
	proj = padOrTrunc(proj, 24)
	hint := row.keyHint
	if hint == "" {
		hint = "(no key)"
	}
	return fmt.Sprintf("%s %s %s", acct, proj, hint)
}

// titleCase capitalizes the first letter of a string. Used for screen
// titles where the entity term is plural-lowercase ("projects" →
// "Projects").
func titleCase(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
