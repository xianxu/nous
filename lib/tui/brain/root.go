package brain

import (
	"os"
	"os/exec"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/xianxu/nous/lib/gh"
)

// publishSubprocessDoneMsg signals "the `nous brain publish` subprocess
// returned." On success the brain is now private (origin set); we
// re-enter the detail view so its refreshed status shows the new rung
// and the `a invite` gesture.
type publishSubprocessDoneMsg struct {
	path string
	err  error
}

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
	screenLeave
)

// cancelNewBrainMsg signals "exit the new-brain flow back to the list,"
// emitted both on user cancellation AND after the new-brain flow's
// done-stage is dismissed. Distinct from newBrainDoneMsg (which
// carries the result) so the result-banner rendering happens before
// the list re-render takes over.
type cancelNewBrainMsg struct{}

type rootModel struct {
	gh gh.Client

	current      screen
	list         listModel
	detail       detailModel
	conflict     conflictPreviewModel
	recipAdd     recipientAddModel
	recipRemove  recipientRemoveModel
	newBrain     newBrainModel
	inviteCollab inviteCollabModel
	acceptInvite acceptInviteModel
	leave        leaveModel

	// listCache holds the most recent gh-fetched data (myLogin,
	// invitations, repos). Re-entering the list page (ESC from
	// detail, post-acceptInvite flow, etc.) reuses this cache so
	// the operator gets an instant render instead of waiting for
	// 2-3 gh subprocess round-trips. Cleared after operations that
	// change gh state (accept-invite success, clone success) so
	// the next list render re-fetches.
	listCache *listLoadedData

	// justAccepted holds repos the operator accepted invitations
	// for during this session. GitHub's /user/repos lags behind
	// invitation acceptance (tens of seconds, sometimes more), so
	// the just-accepted brain would briefly disappear from the
	// list — accepted → not in PendingInvitations, not yet in
	// UserRepos, not yet local. We splice these in alongside
	// UserRepos at render time. Dedup by FullName means UserRepos
	// catching up doesn't double-render; localFullNames suppresses
	// post-clone duplication.
	justAccepted []gh.UserRepo
}

// NewRoot returns the top-level bubbletea model for `nous brain`. The
// injected gh.Client is threaded into every sub-model that touches the
// GitHub control plane (list operator-markers, detail status, the
// invite/accept/remove/leave flows).
func NewRoot(c gh.Client) tea.Model {
	return rootModel{
		gh:      c,
		current: screenList,
		list:    newListModel(c, nil, nil),
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
		m.detail = newDetailModel(m.gh, msg.path)
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
		m.recipRemove = newRecipientRemoveModel(m.gh, msg.brainPath, msg.recipients)
		return m, m.recipRemove.Init()
	case launchInviteCollabMsg:
		m.current = screenInviteCollab
		m.inviteCollab = newInviteCollabModel(m.gh, msg.brainPath)
		return m, m.inviteCollab.Init()
	case inviteCollabDoneMsg, cancelInviteCollabMsg:
		// Return to the detail view, refreshing Status so the new
		// recipient (once auto-admit runs on the operator's side)
		// shows up. The detail view's own banner shows the result.
		path := m.detail.path
		m.current = screenDetail
		m.detail = newDetailModel(m.gh, path)
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
		m.acceptInvite = newAcceptInviteModel(m.gh, msg.invitation)
		return m, m.acceptInvite.Init()
	case launchPublishMsg:
		// Publish runs as a foreground subprocess (suspends the TUI):
		// gh repo create + gcrypt push need a real terminal for gpg/ssh
		// pinentry. Same posture as the clone subprocess. Reuses the
		// `nous brain publish` CLI rather than re-implementing the flow.
		bin, err := os.Executable()
		if err != nil {
			bin = "nous"
		}
		path := msg.brainPath
		cmd := exec.Command(bin, "brain", "publish", "--brain", path)
		cmd.Env = os.Environ()
		return m, tea.ExecProcess(cmd, func(err error) tea.Msg {
			return publishSubprocessDoneMsg{path: path, err: err}
		})
	case publishSubprocessDoneMsg:
		// Origin may now be set; the list's per-brain rung is recomputed
		// on next render, so drop the cache. Return to the detail view
		// for the same brain — its Init re-loads Status, surfacing the
		// new rung (private) and the `a invite` action.
		m.listCache = nil
		m.current = screenDetail
		m.detail = newDetailModel(m.gh, msg.path)
		if msg.err == nil {
			m.detail.banner = "✓ published — now private (GitHub-backed)"
		} else {
			m.detail.banner = "✗ publish: " + msg.err.Error()
		}
		return m, m.detail.Init()
	case launchLeaveMsg:
		m.current = screenLeave
		m.leave = newLeaveModel(m.gh, msg.brainPath)
		return m, m.leave.Init()
	case leaveDoneMsg:
		// Manifest changed (we're no longer a collaborator) + GitHub
		// accessible-list may have changed; invalidate cache so the
		// next list render shows ground truth. On success, this brain
		// won't appear as a local row anymore (manifest's still on
		// disk until rm -rf, but our key isn't on it). On
		// cancel/error, the list re-renders unchanged.
		m.listCache = nil
		m.current = screenList
		m.list = newListModel(m.gh, nil, m.justAccepted)
		return m, m.list.Init()
	case acceptInviteDoneMsg:
		// Whether success or failure/cancel, return to the list and
		// re-render. On success, the invitation moves from pending
		// to (collaborator — press enter to clone). On failure,
		// stays pending; the operator can retry. Invalidate the
		// cache either way — the invitation set may have changed
		// (success consumes it) and we want the next render to
		// reflect ground truth.
		//
		// On success, also capture the just-accepted invitation's
		// repo into justAccepted so the brain renders as accessible-
		// but-not-cloned during the /user/repos consistency-lag
		// window. Without this splice, brain disappears entirely
		// between accept and UserRepos catching up.
		if msg.err == nil {
			repo := m.acceptInvite.invitation.AsUserRepo()
			if repo.FullName != "" {
				m.justAccepted = append(m.justAccepted, repo)
			}
		}
		m.listCache = nil
		m.current = screenList
		m.list = newListModel(m.gh, nil, m.justAccepted)
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
		// Local manifests changed; remote uncloned-set may also no
		// longer include this repo. Invalidate the cache.
		// justAccepted is left alone — buildAllItems' localFullNames
		// suppression handles the dedup automatically once the brain
		// has a local manifest.
		m.listCache = nil
		m.current = screenList
		m.list = newListModel(m.gh, nil, m.justAccepted)
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
		// New brain may have been created; local manifests changed.
		// Invalidate cache so the next render re-fetches gh data
		// (and recomputes the localFullNames exclusion against the
		// new brain's origin URL).
		m.listCache = nil
		m.current = screenList
		m.list = newListModel(m.gh, nil, m.justAccepted)
		return m, m.list.Init()
	case recipientAddedMsg, recipientRemovedMsg, cancelRecipientFlowMsg:
		// Recipient flow ended (success/failure/cancel). Return to the
		// detail view; refresh Status so post-action state shows.
		path := m.detail.path
		m.current = screenDetail
		m.detail = newDetailModel(m.gh, path)
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
		// Plain back-navigation (ESC from detail / conflict preview).
		// Use the cached gh data when present — local manifests
		// always re-discovered, so a newly-created brain still
		// shows. This is the win that makes ESC instant.
		m.current = screenList
		m.list = newListModel(m.gh, m.listCache, m.justAccepted)
		return m, m.list.Init()
	case listLoadedMsg:
		// Splice justAccepted into the freshly-loaded repos before
		// caching + forwarding. Dedup in mergeUserReposDedup keeps
		// it harmless once /user/repos catches up; localFullNames
		// suppression in buildAllItems handles the post-clone case.
		// We don't trim justAccepted explicitly — entries become
		// no-ops once the brain is local or once /user/repos
		// surfaces the repo on its own.
		data := msg.data
		data.repos = mergeUserReposDedup(data.repos, m.justAccepted)
		m.listCache = &data
		var cmd tea.Cmd
		m.list, cmd = m.list.Update(listLoadedMsg{data: data})
		return m, cmd
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
	case screenLeave:
		m.leave, cmd = m.leave.Update(msg)
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
	case screenLeave:
		return m.leave.View()
	default:
		return m.list.View()
	}
}
