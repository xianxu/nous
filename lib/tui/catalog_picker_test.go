package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xianxu/nous/lib/provider/providers/catalog"
)

func newTestCatalogPicker(t *testing.T, ids ...string) catalogPickerModel {
	t.Helper()
	entries := make([]catalog.Entry, 0, len(ids))
	for _, id := range ids {
		entries = append(entries, catalog.Entry{
			ID:               id,
			Name:             strings.ToTitle(id[:1]) + id[1:],
			HostnamePatterns: []string{"api." + id + ".test"},
		})
	}
	return newCatalogPickerModel(&catalog.Catalog{Entries: entries})
}

func TestCatalogPicker_EnterEmitsSelected(t *testing.T) {
	m := newTestCatalogPicker(t, "anthropic", "groq")
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected cmd from enter")
	}
	msg := cmd()
	sel, ok := msg.(catalogSelectedMsg)
	if !ok {
		t.Fatalf("expected catalogSelectedMsg, got %T", msg)
	}
	if sel.entry.ID != "anthropic" {
		t.Errorf("entry.ID = %q, want anthropic", sel.entry.ID)
	}
	if updated.cursor != 0 {
		t.Errorf("cursor moved unexpectedly: %d", updated.cursor)
	}
}

func TestCatalogPicker_DownThenEnter(t *testing.T) {
	m := newTestCatalogPicker(t, "anthropic", "groq")
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if m.cursor != 1 {
		t.Fatalf("cursor = %d after down, want 1", m.cursor)
	}
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	sel := cmd().(catalogSelectedMsg)
	if sel.entry.ID != "groq" {
		t.Errorf("selected = %q, want groq", sel.entry.ID)
	}
}

func TestCatalogPicker_DownClampsAtBottom(t *testing.T) {
	m := newTestCatalogPicker(t, "a", "b")
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown}) // tries to go past
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if m.cursor != 1 {
		t.Errorf("cursor = %d after 3 downs in 2-row list, want 1", m.cursor)
	}
}

func TestCatalogPicker_UpClampsAtTop(t *testing.T) {
	m := newTestCatalogPicker(t, "a", "b")
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if m.cursor != 0 {
		t.Errorf("cursor = %d after up at top, want 0", m.cursor)
	}
}

func TestCatalogPicker_EscEmitsBack(t *testing.T) {
	m := newTestCatalogPicker(t, "anthropic")
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd == nil {
		t.Fatal("expected cmd from esc")
	}
	if _, ok := cmd().(catalogBackMsg); !ok {
		t.Errorf("expected catalogBackMsg, got %T", cmd())
	}
}

func TestCatalogPicker_EmptyShowsHint(t *testing.T) {
	m := newCatalogPickerModel(&catalog.Catalog{})
	out := m.View()
	if !strings.Contains(out, "catalog is empty") {
		t.Errorf("empty-catalog view missing hint, got:\n%s", out)
	}
}

func TestCatalogPicker_ViewIncludesEntryAndHostname(t *testing.T) {
	m := newTestCatalogPicker(t, "anthropic")
	out := m.View()
	if !strings.Contains(out, "Anthropic") {
		t.Errorf("view missing entry name, got:\n%s", out)
	}
	if !strings.Contains(out, "api.anthropic.test") {
		t.Errorf("view missing hostname, got:\n%s", out)
	}
}

// TestProviderPicker_AddProviderTransitionsToCatalog confirms that
// pressing Enter on the "+ add provider" row emits an addProviderMsg
// the top-level model uses to swap to screenCatalogPicker.
func TestProviderPicker_AddProviderEmitsAddProviderMsg(t *testing.T) {
	// Build a minimal picker with just the "+ add provider" row at
	// cursor=1 (after Google).
	m := providerPickerModel{
		items: []providerPickerItem{
			{name: "google", label: "Google"},
			{isAddProvider: true},
		},
		cursor: 1,
	}
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected cmd from enter on + add provider")
	}
	if _, ok := cmd().(addProviderMsg); !ok {
		t.Errorf("expected addProviderMsg, got %T", cmd())
	}
}
