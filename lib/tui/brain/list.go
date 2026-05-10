package brain

import (
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	libbrain "github.com/xianxu/nous/lib/brain"
)

// drillInMsg signals "navigate to the detail view for brain at path."
// The root model intercepts and pushes a fresh detail model.
type drillInMsg struct{ path string }

type listItem struct {
	manifest libbrain.Manifest
}

func (it listItem) label() string {
	kind := "private"
	if it.manifest.Shared() {
		kind = "shared"
	}
	return fmt.Sprintf("%-22s  (%s, %d recipients)",
		it.manifest.Name, kind, len(it.manifest.Recipients))
}

type listModel struct {
	items  []listItem
	cursor int
	err    error // shown in View when non-nil; drill-in disabled
}

func newListModel() listModel {
	manifests, err := libbrain.DiscoverAll()
	if err != nil {
		return listModel{err: err}
	}
	items := make([]listItem, 0, len(manifests))
	for _, m := range manifests {
		items = append(items, listItem{manifest: m})
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].manifest.Name < items[j].manifest.Name
	})
	return listModel{items: items}
}

func (m listModel) Init() tea.Cmd { return nil }

func (m listModel) Update(msg tea.Msg) (listModel, tea.Cmd) {
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
		if m.cursor < len(m.items)-1 {
			m.cursor++
		}
	case "enter":
		if m.err != nil || len(m.items) == 0 {
			return m, nil
		}
		path := m.items[m.cursor].manifest.Path
		return m, func() tea.Msg { return drillInMsg{path: path} }
	case "q", "esc", "ctrl+c":
		return m, tea.Quit
	}
	return m, nil
}

func (m listModel) View() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Brains under workspace"))
	b.WriteString("\n\n")

	if m.err != nil {
		b.WriteString(warnStyle.Render("error: " + m.err.Error()))
		b.WriteString("\n\n")
		b.WriteString(helpStyle.Render("q/esc quit"))
		return b.String()
	}
	if len(m.items) == 0 {
		b.WriteString(mutedStyle.Render("(no brains found under workspace root)"))
		b.WriteString("\n\n")
		b.WriteString(helpStyle.Render("q/esc quit"))
		return b.String()
	}

	for i, it := range m.items {
		row := "  " + it.label()
		if i == m.cursor {
			row = cursorRowStyle.Render("▸ " + it.label())
		}
		b.WriteString(row)
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(helpStyle.Render("↑↓/jk  navigate    enter  drill in    q/esc  quit"))
	return b.String()
}
