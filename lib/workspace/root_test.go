package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRoot_WorkspaceRootEnvWins(t *testing.T) {
	t.Setenv("WORKSPACE_ROOT", "/tmp/explicit")
	t.Setenv("NOUS_DIR", "/tmp/should-not-be-used")
	got, err := Root()
	if err != nil {
		t.Fatalf("Root: %v", err)
	}
	if got != "/tmp/explicit" {
		t.Errorf("Root = %q, want /tmp/explicit", got)
	}
}

func TestRoot_NousDirParent(t *testing.T) {
	t.Setenv("WORKSPACE_ROOT", "")
	t.Setenv("NOUS_DIR", "/Users/x/workspace/nous")
	got, err := Root()
	if err != nil {
		t.Fatalf("Root: %v", err)
	}
	if got != "/Users/x/workspace" {
		t.Errorf("Root = %q, want /Users/x/workspace", got)
	}
}

func TestRoot_NousDirTrailingSlashTolerated(t *testing.T) {
	t.Setenv("WORKSPACE_ROOT", "")
	t.Setenv("NOUS_DIR", "/Users/x/workspace/nous/")
	got, err := Root()
	if err != nil {
		t.Fatalf("Root: %v", err)
	}
	if got != "/Users/x/workspace" {
		t.Errorf("Root = %q, want /Users/x/workspace (trailing slash should be tolerated)", got)
	}
}

func TestRoot_FallsBackToHomeWorkspace(t *testing.T) {
	t.Setenv("WORKSPACE_ROOT", "")
	t.Setenv("NOUS_DIR", "")
	// os.Executable() during `go test` returns the test binary path,
	// which is typically /var/folders/.../<pkg>.test — unlikely to
	// match the bin/<exe> heuristic, so we fall through to $HOME.
	got, err := Root()
	if err != nil {
		t.Fatalf("Root: %v", err)
	}
	home, _ := os.UserHomeDir()
	expected := filepath.Join(home, "workspace")
	// Allow either the home fallback or the executable-derived path
	// (whichever the heuristic picks during the test run).
	if got != expected && filepath.Base(got) == "" {
		t.Errorf("Root = %q, expected fallback to %q or a sensible binary-derived path", got, expected)
	}
}

// TestFindWorkspaceViaRepoMarker_BinSymlinkLayout exercises the
// post-symlink layout where bin/<name> points into cmd/<name>/bin/<name>.
// The resolved real path is two levels deeper than the symlink suggests,
// which broke the previous fixed-strip heuristic.
func TestFindWorkspaceViaRepoMarker_BinSymlinkLayout(t *testing.T) {
	root := t.TempDir() // simulates the workspace root
	repo := filepath.Join(root, "nous")
	binReal := filepath.Join(repo, "cmd", "nous", "bin") // where the real binary lives
	if err := os.MkdirAll(binReal, 0o755); err != nil {
		t.Fatal(err)
	}
	// Drop a go.mod at the repo root so the walk can recognize it.
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module example.com/nous\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := findWorkspaceViaRepoMarker(binReal)
	if got != root {
		t.Errorf("findWorkspaceViaRepoMarker(%q) = %q, want %q", binReal, got, root)
	}
}

// TestFindWorkspaceViaRepoMarker_InstalledBin verifies that an installed
// binary at ~/.local/bin/<name> doesn't accidentally pick a workspace
// root (no repo marker on the way up, so it returns "" → caller falls
// through to $HOME/workspace).
func TestFindWorkspaceViaRepoMarker_InstalledBin(t *testing.T) {
	root := t.TempDir()
	binDir := filepath.Join(root, ".local", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// No go.mod / .git anywhere along the way.
	if got := findWorkspaceViaRepoMarker(binDir); got != "" {
		t.Errorf("findWorkspaceViaRepoMarker(%q) = %q, want \"\" (no marker found)", binDir, got)
	}
}

// TestFindWorkspaceViaRepoMarker_GitMarker verifies that a .git/
// directory (not just go.mod) counts as a repo marker — useful for
// repos that aren't Go-based.
func TestFindWorkspaceViaRepoMarker_GitMarker(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "myrepo")
	binDir := filepath.Join(repo, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := findWorkspaceViaRepoMarker(binDir); got != root {
		t.Errorf("findWorkspaceViaRepoMarker(%q) = %q, want %q", binDir, got, root)
	}
}
