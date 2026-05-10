package brain

import (
	tea "github.com/charmbracelet/bubbletea"
)

// rootModel owns the screen stack for the brain TUI: list ⇄ detail ⇄
// conflict preview. Sub-models emit drillInMsg / popToListMsg /
// openConflictPreviewMsg to navigate; the root handles them by
// switching `current` and instantiating fresh sub-models.
//
// The stack is shallow on purpose. M5b will add a recipient-add sub-
// screen launched from the detail view; that's still flat under the
// detail (esc returns to detail, not the list).
type screen int

const (
	screenList screen = iota
	screenDetail
	screenConflict
)

type rootModel struct {
	current  screen
	list     listModel
	detail   detailModel
	conflict conflictPreviewModel
}

// NewRoot returns the top-level bubbletea model for `nous brain`.
func NewRoot() tea.Model {
	return rootModel{
		current: screenList,
		list:    newListModel(),
	}
}

func (m rootModel) Init() tea.Cmd {
	return m.list.Init()
}

func (m rootModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Nav messages flow up; everything else is dispatched to the active
	// sub-model. tea.Quit is forwarded by sub-models on q/ctrl+c, so the
	// root doesn't need to special-case it.
	switch msg := msg.(type) {
	case drillInMsg:
		m.current = screenDetail
		m.detail = newDetailModel(msg.path)
		return m, m.detail.Init()
	case openConflictPreviewMsg:
		m.current = screenConflict
		m.conflict = newConflictPreviewModel(msg.root, msg.rels)
		return m, m.conflict.Init()
	case popToListMsg:
		// From detail or conflict, pop to list. Refresh the list so any
		// state changes (M5b recipient ops) reflect.
		m.current = screenList
		m.list = newListModel()
		return m, m.list.Init()
	}

	var cmd tea.Cmd
	switch m.current {
	case screenList:
		m.list, cmd = m.list.Update(msg)
	case screenDetail:
		m.detail, cmd = m.detail.Update(msg)
	case screenConflict:
		m.conflict, cmd = m.conflict.Update(msg)
	}
	return m, cmd
}

func (m rootModel) View() string {
	switch m.current {
	case screenDetail:
		return m.detail.View()
	case screenConflict:
		return m.conflict.View()
	default:
		return m.list.View()
	}
}
