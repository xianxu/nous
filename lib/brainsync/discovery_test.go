package brainsync

import (
	"os"
	"path/filepath"
	"testing"
)

// mustWriteBrain creates dir/.brain/config.md with the given body.
func mustWriteBrain(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".brain"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".brain", "config.md"), []byte("---\n"+body+"---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFindSharedBrains(t *testing.T) {
	root := t.TempDir()
	mustWriteBrain(t, filepath.Join(root, "shared-family"), "mode: shared\nname: family\nrecipients: [FP1, FP2]\n")
	mustWriteBrain(t, filepath.Join(root, "private-brain"), "mode: private\nname: personal\nrecipients: [FP1]\n")
	if err := os.MkdirAll(filepath.Join(root, "code-repo"), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := FindSharedBrains([]string{root})
	if err != nil {
		t.Fatalf("FindSharedBrains: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 shared brain, got %d: %v", len(got), got)
	}
	if filepath.Base(got[0]) != "shared-family" {
		t.Errorf("want shared-family, got %s", got[0])
	}
}

func TestFindSharedBrains_EmptyRoot(t *testing.T) {
	got, err := FindSharedBrains([]string{t.TempDir()})
	if err != nil {
		t.Fatalf("FindSharedBrains: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("want 0 brains in empty root, got %v", got)
	}
}

func TestFindSharedBrains_BadRoot(t *testing.T) {
	_, err := FindSharedBrains([]string{"/no/such/path"})
	if err == nil {
		t.Error("expected error for nonexistent root")
	}
}

func TestIsSharedBrain(t *testing.T) {
	tests := []struct {
		name, body string
		want       bool
	}{
		{"shared", "---\nmode: shared\nname: family\n---\n", true},
		{"private", "---\nmode: private\nname: personal\n---\n", false},
		{"missing mode", "---\nname: thing\n---\n", false},
		{"shared with extra space", "---\nmode:   shared   \n---\n", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isSharedBrain(tc.body); got != tc.want {
				t.Errorf("isSharedBrain(%q) = %v, want %v", tc.body, got, tc.want)
			}
		})
	}
}

func TestFindAllSharedBrainsInWorkspace(t *testing.T) {
	root := t.TempDir()
	mustWriteBrain(t, filepath.Join(root, "shared-x"), "mode: shared\nname: x\nrecipients: [FP1]\n")
	t.Setenv("WORKSPACE_ROOT", root)

	got, err := FindAllSharedBrainsInWorkspace()
	if err != nil {
		t.Fatalf("FindAllSharedBrainsInWorkspace: %v", err)
	}
	if len(got) != 1 || filepath.Base(got[0]) != "shared-x" {
		t.Errorf("got %v, want [shared-x]", got)
	}
}
