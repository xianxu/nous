package brain

import (
	tea "github.com/charmbracelet/bubbletea"
)

// rootModel owns the screen stack for the brain TUI: list ⇄ detail ⇄
// conflict preview, plus the recipient add/remove flows launched from
// detail. Sub-models emit nav messages; the root handles them by
// switching `current` and instantiating fresh sub-models.
//
// Recipient action flows have a one-deep return: the operator exits
// the flow (success, error, or cancel) and lands back on the detail
// view for the same brain. The detail view re-loads Status so any
// state change reflects without going through the list.
type screen int

const (
	screenList screen = iota
	screenDetail
	screenConflict
	screenRecipientAdd
	screenRecipientRemove
)

type rootModel struct {
	current     screen
	list        listModel
	detail      detailModel
	conflict    conflictPreviewModel
	recipAdd    recipientAddModel
	recipRemove recipientRemoveModel
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
	case launchRecipientAddMsg:
		m.current = screenRecipientAdd
		m.recipAdd = newRecipientAddModel(msg.brainPath)
		return m, m.recipAdd.Init()
	case launchRecipientRemoveMsg:
		m.current = screenRecipientRemove
		m.recipRemove = newRecipientRemoveModel(msg.brainPath, msg.recipients)
		return m, m.recipRemove.Init()
	case recipientAddedMsg, recipientRemovedMsg, cancelRecipientFlowMsg:
		// Recipient flow ended (success/failure/cancel). Return to the
		// detail view; refresh Status so post-action state shows.
		path := m.detail.path
		m.current = screenDetail
		m.detail = newDetailModel(path)
		if rm, ok := msg.(recipientAddedMsg); ok && rm.err == nil {
			m.detail.banner = "✓ admitted " + rm.last8
		}
		if rm, ok := msg.(recipientAddedMsg); ok && rm.err != nil {
			m.detail.banner = "✗ " + rm.err.Error()
		}
		if rm, ok := msg.(recipientRemovedMsg); ok && rm.err == nil {
			m.detail.banner = "✓ revoked " + rm.last8
		}
		if rm, ok := msg.(recipientRemovedMsg); ok && rm.err != nil {
			m.detail.banner = "✗ " + rm.err.Error()
		}
		return m, m.detail.Init()
	case popToListMsg:
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
	case screenRecipientAdd:
		m.recipAdd, cmd = m.recipAdd.Update(msg)
	case screenRecipientRemove:
		m.recipRemove, cmd = m.recipRemove.Update(msg)
	}
	return m, cmd
}

func (m rootModel) View() string {
	switch m.current {
	case screenDetail:
		return m.detail.View()
	case screenConflict:
		return m.conflict.View()
	case screenRecipientAdd:
		return m.recipAdd.View()
	case screenRecipientRemove:
		return m.recipRemove.View()
	default:
		return m.list.View()
	}
}
