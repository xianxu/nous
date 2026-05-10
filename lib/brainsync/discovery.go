// Package brainsync implements the shared-brain sync daemon.
//
// Identification: a directory is a "brain" iff it contains .brain/config.md.
// Whether the daemon watches it is the *Shared* test (more than one
// recipient) — the legacy `mode: shared` field is no longer authoritative.
package brainsync

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/xianxu/nous/lib/brain"
	"github.com/xianxu/nous/lib/workspace"
)

// FindSharedBrains walks each root, returning the absolute path of every
// directory whose .brain/config.md declares more than one recipient
// (the derived shared signal — see lib/brain.Manifest.Shared).
//
// Walks one level deep — brains live as immediate children of the workspace
// root (parent of nous; see lib/workspace.Root). A nested .brain/ inside a
// brain isn't a distinct brain.
func FindSharedBrains(roots []string) ([]string, error) {
	var found []string
	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", root, err)
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			p := filepath.Join(root, e.Name())
			m, err := brain.Read(p)
			if err != nil {
				continue // not a brain, fine
			}
			if !m.Shared() {
				continue
			}
			abs, err := filepath.Abs(p)
			if err != nil {
				return nil, err
			}
			found = append(found, abs)
		}
	}
	return found, nil
}

// FindAllSharedBrainsInWorkspace looks under the workspace root
// (lib/workspace.Root — $WORKSPACE_ROOT, $NOUS_DIR's parent, the running
// binary's grandparent, or $HOME/workspace) for shared brains. Used as
// the auto-discovery default when the operator doesn't pass --brain
// flags.
func FindAllSharedBrainsInWorkspace() ([]string, error) {
	root, err := workspace.Root()
	if err != nil {
		return nil, err
	}
	return FindSharedBrains([]string{root})
}

