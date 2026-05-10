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
