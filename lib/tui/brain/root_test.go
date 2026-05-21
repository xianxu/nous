package brain

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/xianxu/nous/lib/gh"
)

// TestRoot_DetailEscPopsToList pins the navigation contract: pressing
// ESC on the brain-detail page must return the operator to the brain
// list page. The wiring goes:
//
//	detail.Update(esc) → tea.Cmd that emits popToListMsg
//	root.Update(popToListMsg) → m.current = screenList
//
// Both halves are exercised here so a future refactor of either side
// fails loudly instead of silently making ESC a no-op.
func TestRoot_DetailEscPopsToList(t *testing.T) {
	root := NewRoot().(rootModel)
	if root.current != screenList {
		t.Fatalf("NewRoot should start on screenList, got %d", root.current)
	}

	// Drill into detail (path doesn't need to exist; newDetailModel
	// is a pure constructor — Init() does the async LoadStatus, which
	// we ignore in this test).
	r2, _ := root.Update(drillInMsg{path: "/nonexistent-test-brain"})
	root = r2.(rootModel)
	if root.current != screenDetail {
		t.Fatalf("after drillInMsg expected screenDetail (%d), got %d", screenDetail, root.current)
	}

	// Press ESC. detail.Update routes it through root's default
	// dispatch (KeyMsg has no top-level handler), then returns a Cmd
	// that produces popToListMsg.
	r3, cmd := root.Update(tea.KeyMsg{Type: tea.KeyEsc})
	root = r3.(rootModel)
	if cmd == nil {
		t.Fatal("ESC on detail returned no Cmd; expected one producing popToListMsg")
	}
	msg := cmd()
	if _, ok := msg.(popToListMsg); !ok {
		t.Fatalf("ESC Cmd produced %T, want popToListMsg", msg)
	}

	// Now deliver the popToListMsg to root, simulating bubbletea's
	// follow-up dispatch of the Cmd's result.
	r4, _ := root.Update(msg)
	root = r4.(rootModel)
	if root.current != screenList {
		t.Errorf("after popToListMsg expected screenList (%d), got %d", screenList, root.current)
	}
}

// TestList_NewListModel_DoesNotBlockOnGh pins the load-async
// contract: building the list model with no cache must NOT make
// any gh subprocess calls. If a future change reintroduces the
// synchronous load, this test fails because newListModel takes
// noticeably longer than a filesystem walk on this small fixture.
//
// We don't intercept gh directly (no DI hook in this package);
// instead, we assert on wall-clock time. 200ms is a generous bound
// — a real gh.AuthLogin alone is typically 300ms+, so any sync
// call would blow past it. The TempDir + no manifests path keeps
// the local filesystem-walk side fast.
func TestList_NewListModel_DoesNotBlockOnGh(t *testing.T) {
	t.Setenv("WORKSPACE_ROOT", t.TempDir()) // empty workspace
	start := time.Now()
	m := newListModel(nil, nil)
	elapsed := time.Since(start)
	if elapsed > 200*time.Millisecond {
		t.Errorf("newListModel(nil, nil) took %v — likely doing synchronous gh calls; should be filesystem-only", elapsed)
	}
	if !m.loadingRemote {
		t.Error("expected loadingRemote=true when cache is nil")
	}
}

// TestList_LoadedMsg_FoldsRemoteDataIn pins the async-fold path:
// after newListModel returns a local-only model with
// loadingRemote=true, dispatching listLoadedMsg through Update
// must populate myLogin + flip loadingRemote off.
func TestList_LoadedMsg_FoldsRemoteDataIn(t *testing.T) {
	t.Setenv("WORKSPACE_ROOT", t.TempDir())
	m := newListModel(nil, nil)
	if !m.loadingRemote {
		t.Fatal("precondition: loadingRemote should be true")
	}

	msg := listLoadedMsg{data: listLoadedData{
		myLogin:     "operator-login",
		invitations: nil,
		repos:       nil,
	}}
	m2, _ := m.Update(msg)
	if m2.loadingRemote {
		t.Error("listLoadedMsg should flip loadingRemote off")
	}
	if m2.myLogin != "operator-login" {
		t.Errorf("myLogin = %q, want %q", m2.myLogin, "operator-login")
	}
}

// TestRoot_PopToListReusesCache pins the cache-on-back-navigation
// behavior: after a listLoadedMsg has populated rootModel.listCache,
// pressing ESC from detail must reconstruct the list WITHOUT
// requiring another async load. We assert that the freshly-built
// list model has loadingRemote=false (cache served it).
func TestRoot_PopToListReusesCache(t *testing.T) {
	t.Setenv("WORKSPACE_ROOT", t.TempDir())
	root := NewRoot().(rootModel)

	// Simulate an initial async load completing.
	r2, _ := root.Update(listLoadedMsg{data: listLoadedData{
		myLogin: "operator", invitations: []gh.Invitation{}, repos: []gh.UserRepo{},
	}})
	root = r2.(rootModel)
	if root.listCache == nil {
		t.Fatal("listCache should be populated after listLoadedMsg")
	}

	// Drill into detail, then ESC back.
	r3, _ := root.Update(drillInMsg{path: "/nonexistent"})
	root = r3.(rootModel)
	r4, _ := root.Update(popToListMsg{})
	root = r4.(rootModel)

	if root.current != screenList {
		t.Fatalf("after popToListMsg expected screenList, got %d", root.current)
	}
	if root.list.loadingRemote {
		t.Error("expected list.loadingRemote=false after pop-with-cache; got true (means another fetch is in flight)")
	}
	if root.list.myLogin != "operator" {
		t.Errorf("expected list.myLogin populated from cache, got %q", root.list.myLogin)
	}
}

// TestRoot_AcceptInviteDoneInvalidatesCache: after a successful
// accept-invite flow, the cache must be cleared so the next list
// render re-fetches the (now-changed) pending-invitations set.
func TestRoot_AcceptInviteDoneInvalidatesCache(t *testing.T) {
	t.Setenv("WORKSPACE_ROOT", t.TempDir())
	root := NewRoot().(rootModel)
	r2, _ := root.Update(listLoadedMsg{data: listLoadedData{myLogin: "operator"}})
	root = r2.(rootModel)
	if root.listCache == nil {
		t.Fatal("precondition: listCache should be populated")
	}

	r3, _ := root.Update(acceptInviteDoneMsg{})
	root = r3.(rootModel)
	if root.listCache != nil {
		t.Error("expected listCache to be cleared after acceptInviteDoneMsg; cache still present")
	}
	if !root.list.loadingRemote {
		t.Error("expected new list to be loadingRemote=true after cache invalidation")
	}
}

// TestRoot_AcceptInviteSplicesJustAccepted pins the post-accept
// visibility fix: after Emma accepts an invitation, the brain
// should appear as accessible-but-not-cloned even if /user/repos
// hasn't caught up yet. We assert that the invitation's repo
// lands in rootModel.justAccepted, and that a subsequent
// listLoadedMsg with empty UserRepos still produces an item for
// the just-accepted repo on the rebuilt list.
//
// Bypasses launchAcceptInviteMsg (which would invoke
// newAcceptInviteModel → real gh subprocess) by directly seeding
// rootModel.acceptInvite with the invitation. The
// acceptInviteDoneMsg handler reads m.acceptInvite.invitation, so
// this gets us the same effect without spawning gh.
func TestRoot_AcceptInviteSplicesJustAccepted(t *testing.T) {
	t.Setenv("WORKSPACE_ROOT", t.TempDir())
	root := NewRoot().(rootModel)

	// Construct an invitation that meets the brain-marker filter
	// (description prefix `nous-brain:`).
	inv := gh.Invitation{ID: 42}
	inv.Repository.FullName = "owner/brain1"
	inv.Repository.Name = "brain1"
	inv.Repository.Owner.Login = "owner"
	inv.Repository.Description = "nous-brain: test"
	inv.Repository.SSHURL = "git@github.com:owner/brain1.git"

	// Seed acceptInvite directly so we don't spawn gh subprocesses.
	root.acceptInvite = acceptInviteModel{invitation: inv}

	r3, _ := root.Update(acceptInviteDoneMsg{err: nil})
	root = r3.(rootModel)

	if len(root.justAccepted) != 1 || root.justAccepted[0].FullName != "owner/brain1" {
		t.Fatalf("justAccepted should hold the invitation repo after a successful accept; got %+v",
			root.justAccepted)
	}

	// Simulate /user/repos lag: load returns no repos and no
	// pending invitations. The rebuilt list should still include
	// brain1 as accessible-but-not-cloned via the splice.
	r4, _ := root.Update(listLoadedMsg{data: listLoadedData{
		myLogin: "emma", invitations: nil, repos: nil,
	}})
	root = r4.(rootModel)

	found := false
	for _, it := range root.list.items {
		if it.isUncloned && it.uncloned.FullName == "owner/brain1" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("post-accept list should contain owner/brain1 as accessible-but-not-cloned via splice; got items %+v",
			root.list.items)
	}
}

// TestMergeUserReposDedup_CaseInsensitive pins the dedup contract
// — case-insensitive FullName, preserves order of `a`, appends
// only non-duplicate `b`.
func TestMergeUserReposDedup_CaseInsensitive(t *testing.T) {
	a := []gh.UserRepo{{FullName: "Owner/One"}, {FullName: "owner/two"}}
	b := []gh.UserRepo{{FullName: "OWNER/ONE"}, {FullName: "owner/three"}}
	out := mergeUserReposDedup(a, b)
	if len(out) != 3 {
		t.Fatalf("expected 3 items after dedup, got %d: %+v", len(out), out)
	}
	got := []string{out[0].FullName, out[1].FullName, out[2].FullName}
	want := []string{"Owner/One", "owner/two", "owner/three"}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("merge[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
