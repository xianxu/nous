package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xianxu/nous/lib/provider/providers"
	"github.com/xianxu/nous/lib/provider/vault/memory"
)

// TestWindowSizeFanout — issue #20. The top-level model.Update used to
// only forward tea.WindowSizeMsg to the scopes screen, leaving every other
// screen unable to react to SIGWINCH. After the fix the resize should
// reach whichever sub-model is current.
func TestWindowSizeFanout(t *testing.T) {
	v := vaultWithBase("a@gmail.com", "https://www.googleapis.com/auth/gmail.readonly")
	rows, _ := loadScopeRows(v, "a@gmail.com", nil)
	scopes := newScopesModel("a@gmail.com", rows, nil)
	// scopesModel auto-seeds height from ioctl in newScopesModel; force
	// to a known sentinel so the post-resize assertion below has a
	// definite "before".
	scopes.height = 0
	scopes.heightOverride = 0

	m := model{current: screenScopes, scopes: scopes}

	out, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 42})
	mm := out.(model)

	if mm.width != 120 || mm.height != 42 {
		t.Errorf("parent dims: got w=%d h=%d, want 120/42", mm.width, mm.height)
	}
	if mm.scopes.height != 42 {
		t.Errorf("scopes height not updated: got %d, want 42", mm.scopes.height)
	}
}

// TestWindowSizeFanoutToPaste — non-scopes screens must also receive the
// resize. adminKeyPasteModel stores width/height on WindowSizeMsg; before
// the fix the parent dropped the message on the floor for everything but
// scopes, so the paste model's fields never updated.
func TestWindowSizeFanoutToPaste(t *testing.T) {
	v := memory.New()
	store := fakeAdminStore(t, "openai", false, "")
	fake := providers.NewFake().WithName("openai")
	paste := newAdminKeyPasteModel("openai", fake, store, v, false, "")

	m := model{current: screenAdminKeyPaste, adminPaste: paste}
	out, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	mm := out.(model)

	if mm.adminPaste.width != 100 || mm.adminPaste.height != 30 {
		t.Errorf("paste dims: got w=%d h=%d, want 100/30",
			mm.adminPaste.width, mm.adminPaste.height)
	}
}

// TestSeedSizeOnScreenTransition — when m.current changes, the parent
// must emit a synthetic WindowSizeMsg so the new screen sees real
// dimensions on its first frame instead of zero. Without this, paste/list
// modals open against zero-valued dimensions and only refresh if the user
// happens to resize the terminal afterwards.
func TestSeedSizeOnScreenTransition(t *testing.T) {
	v := memory.New()
	store := fakeAdminStore(t, "openai", false, "")
	fake := providers.NewFake().WithName("openai")

	m := model{
		vault:          v,
		current:        screenProvider, // anything != target
		adminProviders: map[string]providers.Provider{"openai": fake},
		adminStores:    map[string]*providers.AdminKeyStore{"openai": store},
		width:          88,
		height:         24,
	}

	// Force a screen transition via openAdminKeyPaste, the same path the
	// real adminKeyPasteRequestMsg case takes.
	out, cmd := m.Update(adminKeyPasteRequestMsg{provider: "openai"})
	mm := out.(model)
	if mm.current != screenAdminKeyPaste {
		t.Fatalf("expected transition to paste, got %v", mm.current)
	}
	if cmd == nil {
		t.Fatal("expected a Cmd carrying the seed WindowSizeMsg, got nil")
	}

	// The Cmd should be a tea.Batch — first the original cmd (likely
	// nil for this path), then the seed. Drain it and look for a
	// WindowSizeMsg carrying the cached dimensions.
	got := drainForWindowSize(cmd)
	if got == nil {
		t.Fatal("no WindowSizeMsg found in returned Cmd batch")
	}
	if got.Width != 88 || got.Height != 24 {
		t.Errorf("seeded dims: got w=%d h=%d, want 88/24", got.Width, got.Height)
	}
}

// drainForWindowSize executes a Cmd (or the BatchMsg it produces) and
// returns the first WindowSizeMsg encountered, or nil. bubbletea's
// tea.Batch returns a Cmd whose result is a tea.BatchMsg containing the
// child Cmds; we recurse one level since seedSizeCmd is itself a leaf.
func drainForWindowSize(cmd tea.Cmd) *tea.WindowSizeMsg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if ws, ok := msg.(tea.WindowSizeMsg); ok {
		return &ws
	}
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			if ws := drainForWindowSize(c); ws != nil {
				return ws
			}
		}
	}
	return nil
}
