package brain

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	libbrain "github.com/xianxu/nous/lib/brain"
	"github.com/xianxu/nous/lib/gh"
)

// readyDetail builds a non-loading detail model with the given status —
// the shape View() and the action handlers branch on.
func readyDetail(s libbrain.Status) detailModel {
	return detailModel{path: s.Manifest.Path, loading: false, status: s}
}

func localStatus() libbrain.Status {
	return libbrain.Status{
		Manifest:  libbrain.Manifest{Path: "/x/scratch", Recipients: []string{"A"}},
		OriginURL: "", // local: no remote
	}
}

func privateStatus() libbrain.Status {
	return libbrain.Status{
		Manifest:    libbrain.Manifest{Path: "/x/work", Recipients: []string{"A"}},
		OriginURL:   "gcrypt::ssh://git@github.com/me/work.git",
		HasUpstream: true,
	}
}

func sharedStatus() libbrain.Status {
	return libbrain.Status{
		Manifest:    libbrain.Manifest{Path: "/x/family", Recipients: []string{"A", "B"}},
		OriginURL:   "gcrypt::ssh://git@github.com/me/family.git",
		HasUpstream: true,
	}
}

func TestDetailView_LocalRung(t *testing.T) {
	v := readyDetail(localStatus()).View()
	wantSubstrings := []string{
		"local — on this device only, no remote", // header
		"local only — lives on this device",      // sync section
		"p  publish to GitHub",                   // footer: next-rung gesture
	}
	for _, w := range wantSubstrings {
		if !strings.Contains(v, w) {
			t.Errorf("local detail view missing %q\n---\n%s", w, v)
		}
	}
	// Must NOT offer invite/remove/leave on a local brain.
	for _, no := range []string{"invite", "remove collaborator", "leave brain"} {
		if strings.Contains(v, no) {
			t.Errorf("local detail view should not offer %q\n---\n%s", no, v)
		}
	}
}

func TestDetailView_PrivateRung(t *testing.T) {
	v := readyDetail(privateStatus()).View()
	if !strings.Contains(v, "private · github") {
		t.Errorf("private header missing\n---\n%s", v)
	}
	if !strings.Contains(v, "a  invite a collaborator") {
		t.Errorf("private footer should offer invite\n---\n%s", v)
	}
	// No publish (already published), no remove/leave (solo).
	for _, no := range []string{"publish to GitHub", "remove collaborator", "leave brain"} {
		if strings.Contains(v, no) {
			t.Errorf("private detail view should not offer %q\n---\n%s", no, v)
		}
	}
}

func TestDetailView_SharedRung(t *testing.T) {
	v := readyDetail(sharedStatus()).View()
	if !strings.Contains(v, "shared · 2 collaborators · github") {
		t.Errorf("shared header missing\n---\n%s", v)
	}
	for _, w := range []string{"a  invite", "r  remove collaborator", "l  leave brain"} {
		if !strings.Contains(v, w) {
			t.Errorf("shared footer missing %q\n---\n%s", w, v)
		}
	}
	if strings.Contains(v, "publish to GitHub") {
		t.Errorf("shared detail view should not offer publish\n---\n%s", v)
	}
}

// TestDetail_PublishKeyOnlyOnLocal: `p` emits launchPublishMsg on a
// local brain, and is a no-op on a published one.
func TestDetail_PublishKeyOnlyOnLocal(t *testing.T) {
	// Local → emits launchPublishMsg.
	m := readyDetail(localStatus())
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
	if cmd == nil {
		t.Fatal("p on local brain returned no Cmd; expected launchPublishMsg")
	}
	if lp, ok := cmd().(launchPublishMsg); !ok {
		t.Fatalf("p on local produced %T, want launchPublishMsg", cmd())
	} else if lp.brainPath != "/x/scratch" {
		t.Errorf("launchPublishMsg.brainPath = %q, want /x/scratch", lp.brainPath)
	}

	// Private → p is a no-op.
	m2 := readyDetail(privateStatus())
	if _, cmd := m2.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}}); cmd != nil {
		t.Errorf("p on a published brain should be a no-op, got a Cmd producing %T", cmd())
	}
}

// TestDetail_InviteOnLocalShowsHint: pressing `a` on a local brain
// doesn't launch the invite flow — it nudges the operator to publish.
func TestDetail_InviteOnLocalShowsHint(t *testing.T) {
	m := readyDetail(localStatus())
	m2, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	if cmd != nil {
		t.Errorf("a on local brain should not launch invite, got Cmd producing %T", cmd())
	}
	if !strings.Contains(m2.banner, "publish first") {
		t.Errorf("a on local should set a publish-first banner, got %q", m2.banner)
	}
}

// TestRoot_PublishDoneReturnsToDetail: after the publish subprocess
// completes, the root re-enters detail (refreshed) and invalidates the
// list cache so the new rung renders.
func TestRoot_PublishDoneReturnsToDetail(t *testing.T) {
	t.Setenv("WORKSPACE_ROOT", t.TempDir())
	root := NewRoot(gh.NewFake(gh.Conf{})).(rootModel)
	// Seed a cache so we can assert it's cleared.
	r1, _ := root.Update(listLoadedMsg{data: listLoadedData{myLogin: "me"}})
	root = r1.(rootModel)
	if root.listCache == nil {
		t.Fatal("precondition: cache populated")
	}

	r2, _ := root.Update(publishSubprocessDoneMsg{path: "/x/scratch", err: nil})
	root = r2.(rootModel)
	if root.current != screenDetail {
		t.Errorf("after publish done expected screenDetail, got %d", root.current)
	}
	if root.listCache != nil {
		t.Error("publish should invalidate the list cache (origin changed)")
	}
	if !strings.Contains(root.detail.banner, "now private") {
		t.Errorf("detail banner should confirm publish, got %q", root.detail.banner)
	}
}
