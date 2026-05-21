package brain

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestEnclosingBrain_StartIsBrainRoot(t *testing.T) {
	root := writeBrain(t, t.TempDir(), `---
name: brain1
recipients: [0ECF6AC06E9BB6C5B928F10B5D6885D83872C2F0]
---
`)
	m, err := EnclosingBrain(root)
	if err != nil {
		t.Fatalf("EnclosingBrain: %v", err)
	}
	abs, _ := filepath.Abs(root)
	if m.Path != abs {
		t.Errorf("Path = %q, want %q", m.Path, abs)
	}
	if m.Name != "brain1" {
		t.Errorf("Name = %q, want brain1", m.Name)
	}
}

func TestEnclosingBrain_StartIsDeepSubdir(t *testing.T) {
	root := writeBrain(t, t.TempDir(), `---
name: brain1
recipients: [0ECF6AC06E9BB6C5B928F10B5D6885D83872C2F0]
---
`)
	deep := filepath.Join(root, "notes", "tokyo", "drafts")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	m, err := EnclosingBrain(deep)
	if err != nil {
		t.Fatalf("EnclosingBrain from deep subdir: %v", err)
	}
	if m.Name != "brain1" {
		t.Errorf("Name = %q, want brain1", m.Name)
	}
}

func TestEnclosingBrain_NotInBrain(t *testing.T) {
	tmp := t.TempDir() // No .brain/config.md anywhere up the tree.
	_, err := EnclosingBrain(tmp)
	if !errors.Is(err, ErrNotInBrain) {
		t.Errorf("EnclosingBrain in non-brain dir: err = %v, want ErrNotInBrain", err)
	}
}

func TestEnclosingBrain_NestedBrainsReturnNearest(t *testing.T) {
	// Two brains, one nested inside the other. The walk should
	// stop at the *nearest* — even though a real layout wouldn't
	// typically nest brains, the contract should be unambiguous.
	tmp := t.TempDir()
	outer := writeBrain(t, tmp, `---
name: outer
recipients: [0ECF6AC06E9BB6C5B928F10B5D6885D83872C2F0]
---
`)
	innerRoot := filepath.Join(outer, "sub")
	writeBrain(t, innerRoot, `---
name: inner
recipients: [0ECF6AC06E9BB6C5B928F10B5D6885D83872C2F0]
---
`)
	m, err := EnclosingBrain(filepath.Join(innerRoot, "x"))
	if err != nil {
		t.Fatalf("EnclosingBrain: %v", err)
	}
	if m.Name != "inner" {
		t.Errorf("Name = %q, want inner (nearest)", m.Name)
	}
}

func TestManifest_AutosaveEnabled_DefaultsToOn(t *testing.T) {
	root := writeBrain(t, t.TempDir(), `---
name: brain1
recipients: [0ECF6AC06E9BB6C5B928F10B5D6885D83872C2F0]
---
`)
	m, err := Read(root)
	if err != nil {
		t.Fatal(err)
	}
	if !m.AutosaveEnabled() {
		t.Error("missing autosave key should default to enabled")
	}
}

func TestManifest_AutosaveEnabled_RespectsOff(t *testing.T) {
	root := writeBrain(t, t.TempDir(), `---
name: brain1
recipients: [0ECF6AC06E9BB6C5B928F10B5D6885D83872C2F0]
autosave: off
---
`)
	m, err := Read(root)
	if err != nil {
		t.Fatal(err)
	}
	if m.AutosaveEnabled() {
		t.Error("autosave: off should disable")
	}
}

func TestManifest_AutosaveEnabled_OnExplicit(t *testing.T) {
	root := writeBrain(t, t.TempDir(), `---
name: brain1
recipients: [0ECF6AC06E9BB6C5B928F10B5D6885D83872C2F0]
autosave: on
---
`)
	m, err := Read(root)
	if err != nil {
		t.Fatal(err)
	}
	if !m.AutosaveEnabled() {
		t.Error("autosave: on should enable")
	}
}
