package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// TestFrameSizeInvariant — every View() call must produce exactly m.height
// lines, regardless of state, filter, cursor position, or window scroll.
// Bubbletea's render diff doesn't reliably clear lines that disappear
// between frames (especially with CHARON_TUI_NO_ALT=1), so we pad.
func TestFrameSizeInvariant(t *testing.T) {
	v := vaultWithBase("a@gmail.com", "https://www.googleapis.com/auth/gmail.readonly")
	rows, _ := loadScopeRows(v, "a@gmail.com", nil)

	cases := []struct {
		name  string
		setup func(scopesModel) scopesModel
	}{
		{"normal", func(m scopesModel) scopesModel { return m }},
		{"normal+focus list", func(m scopesModel) scopesModel {
			m.focus = focusList
			return m
		}},
		{"empty filter", func(m scopesModel) scopesModel {
			m.search.SetValue("zzznomatch")
			m.recomputeFiltered()
			return m
		}},
		{"add custom modal", func(m scopesModel) scopesModel { m.state = stateAddCustom; return m }},
		{"applying modal", func(m scopesModel) scopesModel { m.state = stateApplying; return m }},
		{"apply error modal", func(m scopesModel) scopesModel {
			m.state = stateApplyError
			return m
		}},
		{"quit confirm", func(m scopesModel) scopesModel {
			m.state = stateQuitConfirm
			return m
		}},
		{"reduce confirm", func(m scopesModel) scopesModel {
			m.state = stateReduceConfirm
			return m
		}},
		{"revoke confirm", func(m scopesModel) scopesModel {
			m.state = stateRevokeConfirm
			return m
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newScopesModel("a@gmail.com", rows, nil)
			m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 22})
			m = tc.setup(m)
			view := m.View()
			lines := strings.Split(view, "\n")
			if len(lines) != 22 {
				t.Errorf("rendered %d lines, want 22 (height)", len(lines))
			}
		})
	}
}

// TestSmallTerminalLayout is a regression test for the rendering pipeline:
// at a fixed height, the rendered view should be exactly that many lines,
// with the header and search bar at the top. This catches the class of
// bugs where the row block grew too tall and pushed the chrome off-screen
// (#000005 image 12 ghosting), or where a trailing newline scrolled the
// top off (#000005 final off-by-one).
func TestSmallTerminalLayout(t *testing.T) {
	v := vaultWithBase("a@gmail.com")
	rows, _ := loadScopeRows(v, "a@gmail.com", nil)
	m := newScopesModel("a@gmail.com", rows, nil)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 22})

	view := m.View()
	lines := strings.Split(view, "\n")

	// Raw height: at h=22, we render exactly 22 lines (no trailing newline).
	// reservedLines=8, so visible row block = 14, total = 22.
	if got, want := len(lines), 22; got != want {
		t.Errorf("rendered %d lines for height=22, want %d", got, want)
	}
	first5 := strings.Join(lines[:5], "\n")
	if !strings.Contains(first5, "google / a@gmail.com") {
		t.Errorf("header missing from first 5 lines:\n%s", first5)
	}
	if !strings.Contains(first5, "filter (substring)") {
		t.Errorf("search placeholder missing from first 5 lines:\n%s", first5)
	}
}

