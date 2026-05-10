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
//  3. The running binary's grandparent, if its path matches
//     <root>/<repo>/bin/<exe>. Symlinks resolved first, so an installed
//     symlink at /usr/local/bin/nous → ~/workspace/nous/bin/nous still
//     yields ~/workspace.
//  4. $HOME/workspace as the last sane default.
//
// Errors only when none of those resolve (no $HOME, no executable path).
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
		// <root>/<repo>/bin/<exe> → strip 3 levels.
		binDir := filepath.Dir(exe)
		if filepath.Base(binDir) == "bin" {
			repoDir := filepath.Dir(binDir)
			return filepath.Dir(repoDir), nil
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("workspace root: no $WORKSPACE_ROOT, no $NOUS_DIR, can't infer from binary path, no $HOME: %w", err)
	}
	return filepath.Join(home, "workspace"), nil
}
