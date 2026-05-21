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
	screenNewBrain
	screenInviteCollab
	screenAcceptInvite
)

// cancelNewBrainMsg signals "exit the new-brain flow back to the list,"
// emitted both on user cancellation AND after the new-brain flow's
// done-stage is dismissed. Distinct from newBrainDoneMsg (which
// carries the result) so the result-banner rendering happens before
// the list re-render takes over.
type cancelNewBrainMsg struct{}

type rootModel struct {
	current       screen
	list          listModel
	detail        detailModel
	conflict      conflictPreviewModel
	recipAdd      recipientAddModel
	recipRemove   recipientRemoveModel
	newBrain      newBrainModel
	inviteCollab  inviteCollabModel
	acceptInvite  acceptInviteModel
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
	case launchInviteCollabMsg:
		m.current = screenInviteCollab
		m.inviteCollab = newInviteCollabModel(msg.brainPath)
		return m, m.inviteCollab.Init()
	case inviteCollabDoneMsg, cancelInviteCollabMsg:
		// Return to the detail view, refreshing Status so the new
		// recipient (once auto-admit runs on the operator's side)
		// shows up. The detail view's own banner shows the result.
		path := m.detail.path
		m.current = screenDetail
		m.detail = newDetailModel(path)
		if rm, ok := msg.(inviteCollabDoneMsg); ok {
			if rm.err == nil {
				m.detail.banner = "✓ invited " + rm.login + " — auto-admit on their join"
			} else {
				m.detail.banner = "✗ invite " + rm.login + ": " + rm.err.Error()
			}
		}
		return m, m.detail.Init()
	case launchAcceptInviteMsg:
		m.current = screenAcceptInvite
		m.acceptInvite = newAcceptInviteModel(msg.invitation)
		return m, m.acceptInvite.Init()
	case acceptInviteDoneMsg:
		// Whether success or failure/cancel, return to the list and
		// re-render. On success, the invitation moves from pending
		// to (collaborator — press enter to clone). On failure,
		// stays pending; the operator can retry.
		m.current = screenList
		m.list = newListModel()
		return m, m.list.Init()
	case cloneSubprocessDoneMsg:
		// Same shape as joinSubprocessDoneMsg. On success the
		// brain shows up as a local row (refresh picks it up); on
		// failure exit so the operator can see the clone-side
		// error in scrollback (most often "missing operator
		// pubkey on keys branch" — see brain_clone.go's hint).
		if msg.err != nil {
			return m, tea.Quit
		}
		m.current = screenList
		m.list = newListModel()
		return m, m.list.Init()
	case launchNewBrainMsg:
		m.current = screenNewBrain
		m.newBrain = newNewBrainModel()
		return m, m.newBrain.Init()
	case newBrainDoneMsg:
		// The new-brain subprocess returned. The done-stage view in
		// newBrainModel renders the result; we just stay on that
		// screen until the operator presses any key (which then emits
		// cancelNewBrainMsg).
		_ = msg
		return m, nil
	case cancelNewBrainMsg:
		m.current = screenList
		m.list = newListModel()
		return m, m.list.Init()
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
	case screenNewBrain:
		m.newBrain, cmd = m.newBrain.Update(msg)
	case screenInviteCollab:
		m.inviteCollab, cmd = m.inviteCollab.Update(msg)
	case screenAcceptInvite:
		m.acceptInvite, cmd = m.acceptInvite.Update(msg)
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
	case screenNewBrain:
		return m.newBrain.View()
	case screenInviteCollab:
		return m.inviteCollab.View()
	case screenAcceptInvite:
		return m.acceptInvite.View()
	default:
		return m.list.View()
	}
}
