package tui

import (
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/xianxu/nous/internal/charon/providers"
	"github.com/xianxu/nous/internal/charon/providers/catalog"
	"github.com/xianxu/nous/internal/charon/vault"
)

// providerPickerItem is one row in the top-level provider picker.
// Either an existing provider (name + label + summary) or the
// "+ add provider" affordance (#15 stub for catalog).
type providerPickerItem struct {
	// Static identity.
	name      string // "google" / "openai" / "anthropic"
	label     string // "Google" / "OpenAI" / "Anthropic"
	typeLabel string // "OAuth" / "Admin key" / "API key"

	// Dynamic summary state computed at picker creation.
	glyph        string // "●" green configured / "○" red not-set / "" oauth-no-glyph
	summary      string // "3 accounts" / "2 projects" / "admin key not set"
	provType     string // vault.TypeOAuth / vault.TypeAdminKey / vault.TypeCatalog
	adminKeySet  bool   // for admin-key providers — drives the glyph color

	// Affordance row (terminal "+ add provider"). Mutually exclusive
	// with the identity fields.
	isAddProvider bool
}

// providerPickerModel is the new top-level screen. Lists Google
// (always), plus any admin-key providers registered via
// WithAdminKeyProvider, plus a "+ add provider" stub for the catalog
// (#15) flow.
type providerPickerModel struct {
	items     []providerPickerItem
	cursor    int
	statusMsg string // transient hint shown on hover/action; clears on next nav
}

// providerSelectedMsg is emitted when the user picks a provider.
// Forwarded to the top-level model which sets up the per-type entity
// list and routes screens.
type providerSelectedMsg struct {
	name     string
	provType string
}

// addProviderMsg signals the user wanted to add a Tier-3 catalog
// provider. The top-level model handles this by transitioning to
// the catalog picker (#15 M2).
type addProviderMsg struct{}

// newProviderPickerModel builds the picker by combining what's in
// the vault (existing accounts → counts) with what's registered as
// admin-key providers (stores → glyph state) and the catalog (entries
// with stored credentials → API-key rows).
//
// adminStores is keyed by provider name; passing an empty/nil map is
// fine — the picker just shows Google (and the catalog "+" row). cat
// may be nil; nil means no catalog rows render even if vault has
// stranded TypeCatalog credentials (test convenience).
func newProviderPickerModel(
	v vault.Store,
	adminStores map[string]*providers.AdminKeyStore,
	cat *catalog.Catalog,
) (providerPickerModel, error) {
	creds, err := v.List()
	if err != nil {
		return providerPickerModel{}, fmt.Errorf("list accounts: %w", err)
	}

	// Per-provider counters. List returns full credentials with
	// payload across all backends (see vault.Store interface contract);
	// CredType() is the canonical discriminator.
	googleAccounts := 0
	adminCounts := map[string]int{}   // provider name → minted-key count
	catalogCounts := map[string]int{} // catalog entry id → pasted-key count
	for _, c := range creds {
		switch c.CredType() {
		case vault.TypeOAuth:
			if c.Provider == "google" {
				googleAccounts++
			}
		case vault.TypeAdminKey:
			adminCounts[c.Provider]++
		case vault.TypeCatalog:
			catalogCounts[c.Provider]++
		}
	}

	items := []providerPickerItem{
		{
			name:      "google",
			label:     "Google",
			typeLabel: "OAuth",
			provType:  vault.TypeOAuth,
			summary:   pluralize(googleAccounts, "account", "accounts"),
		},
	}

	// Sort admin-key providers alphabetically so the picker is stable
	// across runs.
	adminNames := make([]string, 0, len(adminStores))
	for name := range adminStores {
		adminNames = append(adminNames, name)
	}
	sort.Strings(adminNames)
	for _, name := range adminNames {
		store := adminStores[name]
		set := store.IsSet()
		item := providerPickerItem{
			name:        name,
			label:       providerLabel(name),
			typeLabel:   "Admin key",
			provType:    vault.TypeAdminKey,
			adminKeySet: set,
		}
		if set {
			item.glyph = "●"
			// Universal "key/keys" wording — matches the entity-list
			// title ("Keys"), the mint flow ("+ new key"), and the
			// detail screen breadcrumb. The upstream-container term
			// (project/workspace) only shows up in the mint flow's
			// step-2 picker where it's actually disambiguating.
			item.summary = pluralize(adminCounts[name], "key", "keys")
		} else {
			item.glyph = "○"
			item.summary = "admin key not set"
		}
		items = append(items, item)
	}

	// Catalog rows: one per catalog entry with stored credentials.
	// Sorted alphabetically by id for stable rendering. Entries with
	// zero stored creds are omitted — the "+ add provider" row is
	// the path to add the first credential.
	if cat != nil {
		var catIDs []string
		for _, e := range cat.Entries {
			if catalogCounts[e.ID] > 0 {
				catIDs = append(catIDs, e.ID)
			}
		}
		sort.Strings(catIDs)
		entryByID := map[string]catalog.Entry{}
		for _, e := range cat.Entries {
			entryByID[e.ID] = e
		}
		for _, id := range catIDs {
			e := entryByID[id]
			items = append(items, providerPickerItem{
				name:      e.ID,
				label:     providerLabel(e.ID),
				typeLabel: "API key",
				provType:  vault.TypeCatalog,
				glyph:     "●",
				summary:   pluralize(catalogCounts[id], "account", "accounts"),
			})
		}
	}

	items = append(items, providerPickerItem{isAddProvider: true})

	m := providerPickerModel{items: items}
	// Onboarding polish (#15 M7): when the vault has no credentials
	// at all, default the cursor to the "+ add provider" row so a
	// first-run user lands on the most useful action rather than
	// pressing enter on an empty Google row. Cursor preservation
	// across drill-out → drill-in (refreshProviderPickerWithStatus)
	// will override this on subsequent rebuilds, so the polish only
	// affects the first paint of a fresh model — exactly the right
	// scope.
	if len(creds) == 0 {
		m.cursor = len(items) - 1
	}

	return m, nil
}

// providerLabel maps a provider name to its display label. Falls
// back to a Title-cased version of name for unknown providers.
func providerLabel(name string) string {
	switch name {
	case "google":
		return "Google"
	case "openai":
		return "OpenAI"
	case "anthropic":
		return "Anthropic"
	}
	if name == "" {
		return ""
	}
	return strings.ToUpper(name[:1]) + name[1:]
}

// entityTerm returns the singular per-provider term for a credential
// (project for OpenAI, workspace for Anthropic, account otherwise).
// Used by the picker summary and by adminKeyListModel's screen title.
func entityTerm(provider string) string {
	switch provider {
	case "openai":
		return "project"
	case "anthropic":
		return "workspace"
	}
	return "account"
}

func entityTermPlural(provider string) string {
	switch provider {
	case "openai":
		return "projects"
	case "anthropic":
		return "workspaces"
	}
	return "accounts"
}

// upstreamContainerLabel returns the per-provider phrase for the
// upstream container that holds API keys. Used in mint flow Step 2
// where naming the provider explicitly clarifies that the user is
// picking a real OpenAI/Anthropic container, not a charon concept.
// Falls back to "container" for unknown providers.
func upstreamContainerLabel(provider string) string {
	switch provider {
	case "openai":
		return "OpenAI project"
	case "anthropic":
		return "Anthropic workspace"
	}
	return "container"
}

func pluralize(n int, singular, plural string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, singular)
	}
	return fmt.Sprintf("%d %s", n, plural)
}

func (m providerPickerModel) Update(msg tea.Msg) (providerPickerModel, tea.Cmd) {
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
		if m.cursor < len(m.items)-1 {
			m.cursor++
			m.statusMsg = ""
		}
	case "enter":
		if m.cursor < 0 || m.cursor >= len(m.items) {
			return m, nil
		}
		item := m.items[m.cursor]
		if item.isAddProvider {
			// Transitions to the catalog picker (#15 M2). The selected
			// catalog entry's paste flow ships in M4; until then the
			// catalog-picked handler routes back here with a CLI hint.
			return m, func() tea.Msg { return addProviderMsg{} }
		}
		return m, func() tea.Msg {
			return providerSelectedMsg{name: item.name, provType: item.provType}
		}
	case "q", "esc", "ctrl+c":
		return m, tea.Quit
	}
	return m, nil
}

func (m providerPickerModel) View() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render(appName()))
	b.WriteString("\n")
	b.WriteString(mutedStyle.Render("Provider"))
	b.WriteString("\n")
	b.WriteString(strings.Repeat("─", 60))
	b.WriteString("\n")

	for i, item := range m.items {
		cursor := "  "
		if i == m.cursor {
			cursor = "> "
		}

		var line string
		if item.isAddProvider {
			line = "+ add provider"
			if i == m.cursor {
				line = selectedStyle.Render(line)
			} else {
				line = mutedStyle.Render(line)
			}
		} else {
			line = renderProviderItem(item)
			if i == m.cursor {
				line = selectedStyle.Render(line)
			}
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
	b.WriteString(helpStyle.Render("↑↓ nav   enter select   q quit"))
	return b.String()
}

// renderProviderItem composes the provider row: label, type label,
// glyph (admin-key only), summary. Width-stable so columns align
// across rows.
func renderProviderItem(item providerPickerItem) string {
	// Columns: label (16 chars) typeLabel (12) glyph+summary
	label := padOrTrunc(item.label, 16)
	typeLab := padOrTrunc(item.typeLabel, 12)

	var glyph string
	if item.glyph != "" {
		// Color the glyph by configured-state. The admin-key red/green
		// is a primary affordance; using lipgloss directly here so it
		// renders even when this row isn't cursor-highlighted.
		switch item.glyph {
		case "●":
			glyph = lipgloss.NewStyle().Foreground(lipgloss.Color("70")).Render(item.glyph) + " "
		case "○":
			glyph = lipgloss.NewStyle().Foreground(lipgloss.Color("167")).Render(item.glyph) + " "
		default:
			glyph = item.glyph + " "
		}
	}
	return fmt.Sprintf("%s %s %s%s", label, typeLab, glyph, item.summary)
}

func padOrTrunc(s string, width int) string {
	if len(s) >= width {
		if len(s) > width {
			return s[:width]
		}
		return s
	}
	return s + strings.Repeat(" ", width-len(s))
}
