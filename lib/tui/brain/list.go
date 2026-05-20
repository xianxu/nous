package brain

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	libbrain "github.com/xianxu/nous/lib/brain"
	"github.com/xianxu/nous/lib/gh"
)

// drillInMsg signals "navigate to the detail view for brain at path."
// The root model intercepts and pushes a fresh detail model.
type drillInMsg struct{ path string }

type listItem struct {
	manifest   libbrain.Manifest
	isOperator bool // true when the auth'd github user is owner/admin/maintain
}

// labelInner is the post-marker text — basename, kind, recipient count.
// The marker prefix (`*` for operator, ` ` otherwise) is added at
// render time so cursor-row highlighting can include or exclude it
// consistently with the rest of the row.
func (it listItem) labelInner() string {
	kind := "private"
	if it.manifest.Shared() {
		kind = "shared"
	}
	// Display the directory basename rather than manifest.Name. The
	// manifest's `name:` field is operator-authored and can drift from
	// the on-disk location (e.g. brain `name: personal` sits at
	// ~/workspace/brain); for "which repo am I looking at?" the
	// basename is the unambiguous answer.
	return fmt.Sprintf("%-22s  (%s, %d recipients)",
		filepath.Base(it.manifest.Path), kind, len(it.manifest.Recipients))
}

type listModel struct {
	items   []listItem
	cursor  int
	err     error  // shown in View when non-nil; drill-in disabled
	myLogin string // auth'd github user; "" when gh outage
}

func newListModel() listModel {
	manifests, err := libbrain.DiscoverAll()
	if err != nil {
		return listModel{err: err}
	}
	// Resolve auth'd login once for all the IsOperator probes. Empty
	// on outage — marker just doesn't render, consistent with the CLI
	// list's behavior.
	myLogin, _ := gh.AuthLogin()
	items := make([]listItem, 0, len(manifests))
	for _, m := range manifests {
		items = append(items, listItem{
			manifest:   m,
			isOperator: libbrain.IsOperator(m.Path, myLogin),
		})
	}
	sort.Slice(items, func(i, j int) bool {
		return filepath.Base(items[i].manifest.Path) < filepath.Base(items[j].manifest.Path)
	})
	return listModel{items: items, myLogin: myLogin}
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
	case "n":
		// Launch the new-brain flow regardless of whether the list
		// is populated — `n` is a useful entry point even when there
		// are zero brains yet (first-run experience).
		return m, func() tea.Msg { return launchNewBrainMsg{} }
	case "q", "esc", "ctrl+c":
		return m, tea.Quit
	}
	return m, nil
}

func (m listModel) View() string {
	var b strings.Builder
	title := "Brains"
	if m.myLogin != "" {
		title = fmt.Sprintf("Brains (%s)", m.myLogin)
	}
	b.WriteString(titleStyle.Render(title))
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
		b.WriteString(helpStyle.Render("n  create one    q/esc  quit"))
		return b.String()
	}

	for i, it := range m.items {
		marker := " "
		if it.isOperator {
			marker = "*"
		}
		body := marker + " " + it.labelInner()
		row := "  " + body
		if i == m.cursor {
			row = cursorRowStyle.Render("▸ " + body)
		}
		b.WriteString(row)
		b.WriteString("\n")
	}
	b.WriteString("\n")
	if m.myLogin != "" {
		b.WriteString(mutedStyle.Render("  (* = owner)"))
		b.WriteString("\n")
	}
	b.WriteString(helpStyle.Render("↑↓/jk  navigate    enter  drill in    n  new brain    q/esc  quit"))
	return b.String()
}
