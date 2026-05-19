// Package workspace resolves the *workspace root* — the directory under
// which nous and brains live as siblings (e.g. ~/workspace, with
// ~/workspace/nous, ~/workspace/brain, ~/workspace/brain-shared-family
// as immediate children).
//
// Centralized so brain discovery, brainsync, and any future cross-repo
// tooling agree on what "workspace" means without each hardcoding
// $HOME/workspace.
package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Root returns the workspace root. Resolution order:
//
//  1. $WORKSPACE_ROOT — explicit override; tests + nonstandard layouts.
//  2. $NOUS_DIR's parent — set by the Makefile during `make nous-*`.
//  3. The running binary's path, walked upward until a repo marker
//     (go.mod or .git/) is found; workspace root is then the parent
//     of that directory. Symlinks resolved first, so `bin/nous` →
//     `cmd/nous/bin/nous` still resolves correctly (walks up past
//     cmd/ to find nous/go.mod, returns its parent).
//  4. $HOME/workspace as the last sane default.
//
// Errors only when none of those resolve (no $HOME, no executable path).
//
// Step 3 used to strip a fixed three levels off the binary path,
// assuming a `<root>/<repo>/bin/<exe>` layout. That broke for two
// install layouts:
//   - The bin/<name> → cmd/<name>/bin/<name> symlink layout, which
//     adds a level after EvalSymlinks (resolved real path is
//     deeper than the symlink suggests).
//   - Installed binaries at ~/.local/bin/nous, where ~/.local IS
//     basename "bin"'s grandparent — heuristic returned ~/, not
//     ~/workspace, and brain discovery walked one level under ~/.
// The repo-marker walk handles both: cmd/nous/bin/nous walks up to
// find nous/go.mod; ~/.local/bin/nous walks up without finding any
// marker, falls through to $HOME/workspace.
func Root() (string, error) {
	if r := os.Getenv("WORKSPACE_ROOT"); r != "" {
		return r, nil
	}
	if d := os.Getenv("NOUS_DIR"); d != "" {
		return filepath.Dir(strings.TrimRight(d, string(os.PathSeparator))), nil
	}
	if exe, err := os.Executable(); err == nil {
		if real, err := filepath.EvalSymlinks(exe); err == nil {
			exe = real
		}
		if root := findWorkspaceViaRepoMarker(filepath.Dir(exe)); root != "" {
			return root, nil
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("workspace root: no $WORKSPACE_ROOT, no $NOUS_DIR, can't infer from binary path, no $HOME: %w", err)
	}
	return filepath.Join(home, "workspace"), nil
}

// findWorkspaceViaRepoMarker walks up from `start` looking for a
// directory that has a `go.mod` or `.git/` marker (indicating a repo
// root). Returns the parent of that directory (the workspace root),
// or "" if no marker found before reaching the filesystem root.
//
// Symbolic safety: bounded by filesystem-root detection (filepath.Dir
// of "/" returns "/", so the loop terminates). No symlink loops
// possible because we only walk parent directories (which are not
// followed as symlinks).
func findWorkspaceViaRepoMarker(start string) string {
	dir := start
	for {
		// Repo marker = go.mod (Go repo) or .git/ (any git repo).
		// Either suffices; we don't require both.
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return filepath.Dir(dir)
		}
		if fi, err := os.Stat(filepath.Join(dir, ".git")); err == nil && fi.IsDir() {
			return filepath.Dir(dir)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached filesystem root without finding a marker.
			return ""
		}
		dir = parent
	}
}
