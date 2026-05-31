package brain

import (
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// TestNewBrain_PathSkipsIdentityStraightToConfirm pins the nous#33
// behavior: creating a brain is local-only, so the path stage advances
// directly to confirm — there is no identity picker (a local brain
// needs no GPG key).
func TestNewBrain_PathSkipsIdentityStraightToConfirm(t *testing.T) {
	m := newNewBrainModel()
	// A fresh, non-existent path (new.go refuses to advance on an
	// existing target).
	m.path.SetValue(filepath.Join(t.TempDir(), "my-notes"))

	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m2.stage != newStageConfirm {
		t.Fatalf("after path enter, stage = %d, want newStageConfirm (%d) — no identity stage for a local brain",
			m2.stage, newStageConfirm)
	}
}

// TestNewBrain_ConfirmCopyIsLocal pins that the confirm screen describes
// local creation (no GPG/gh ceremony) — the bug this fix closed.
func TestNewBrain_ConfirmCopyIsLocal(t *testing.T) {
	m := newNewBrainModel()
	m.path.SetValue(filepath.Join(t.TempDir(), "my-notes"))
	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	v := m2.View()
	if !strings.Contains(v, "local brain") || !strings.Contains(v, "no GPG key needed") {
		t.Errorf("confirm view should describe local creation; got:\n%s", v)
	}
	if strings.Contains(v, "GPG passphrase") || strings.Contains(v, "--as") {
		t.Errorf("confirm view should not mention GPG passphrase or --as (stale ceremony); got:\n%s", v)
	}
}
