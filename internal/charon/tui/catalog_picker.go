package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xianxu/nous/internal/charon/providers/catalog"
)

// catalogPickerModel lists Tier-3 catalog providers (#15). Reached
// from the top-level provider picker via "+ add provider". Cursor-
// based list mirroring providerPickerModel; bubbles/list with filter
// will be worth pulling in when the catalog grows past ~5 entries.
type catalogPickerModel struct {
	entries []catalog.Entry
	cursor  int
}

// catalogSelectedMsg is emitted when the user picks a catalog entry.
// M4 will route this to the paste-and-store flow; today the top-
// level model surfaces a short status note pointing at the CLI
// shortcut.
type catalogSelectedMsg struct {
	entry catalog.Entry
}

// catalogBackMsg returns to the top-level provider picker.
type catalogBackMsg struct{}

func newCatalogPickerModel(c *catalog.Catalog) catalogPickerModel {
	if c == nil {
		return catalogPickerModel{}
	}
	return catalogPickerModel{entries: c.Entries}
}

func (m catalogPickerModel) Update(msg tea.Msg) (catalogPickerModel, tea.Cmd) {
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch keyMsg.String() {
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.entries)-1 {
			m.cursor++
		}
	case "enter":
		if m.cursor < 0 || m.cursor >= len(m.entries) {
			return m, nil
		}
		entry := m.entries[m.cursor]
		return m, func() tea.Msg { return catalogSelectedMsg{entry: entry} }
	case "esc", "q":
		return m, func() tea.Msg { return catalogBackMsg{} }
	case "ctrl+c":
		return m, tea.Quit
	}
	return m, nil
}

func (m catalogPickerModel) View() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render(appName() + " › Add provider"))
	b.WriteString("\n")
	b.WriteString(mutedStyle.Render("Catalog (paste-and-revoke)"))
	b.WriteString("\n")
	b.WriteString(strings.Repeat("─", 60))
	b.WriteString("\n")

	if len(m.entries) == 0 {
		b.WriteString(mutedStyle.Render("  (catalog is empty)"))
		b.WriteString("\n\n")
		b.WriteString(helpStyle.Render("esc back"))
		return b.String()
	}

	for i, e := range m.entries {
		cursor := "  "
		if i == m.cursor {
			cursor = "> "
		}
		host := ""
		if len(e.HostnamePatterns) > 0 {
			host = e.HostnamePatterns[0]
		}
		name := padOrTrunc(e.Name, 16)
		line := fmt.Sprintf("%s %s", name, mutedStyle.Render(host))
		if i == m.cursor {
			line = selectedStyle.Render(fmt.Sprintf("%s %s", name, host))
		}
		b.WriteString(cursor)
		b.WriteString(line)
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(helpStyle.Render("↑↓ nav   enter select   esc back   ctrl+c quit"))
	return b.String()
}
