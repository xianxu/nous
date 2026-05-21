package brain

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
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
