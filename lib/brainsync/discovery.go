// Package brainsync implements the shared-brain sync daemon.
//
// Identification: a directory is a "brain" iff it contains .brain/config.md.
// Mode is read from the YAML frontmatter; "shared" brains are the ones this
// daemon watches.
package brainsync

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/xianxu/nous/lib/workspace"
)

// FindSharedBrains walks each root, returning the absolute path of every
// directory that contains a .brain/config.md with `mode: shared`.
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
			cfg := filepath.Join(p, ".brain", "config.md")
			data, err := os.ReadFile(cfg)
			if err != nil {
				continue // not a brain, fine
			}
			if isSharedBrain(string(data)) {
				abs, err := filepath.Abs(p)
				if err != nil {
					return nil, err
				}
				found = append(found, abs)
			}
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

// isSharedBrain returns true if the manifest body declares mode: shared.
// Tolerates the YAML frontmatter wrapper (--- ... ---).
func isSharedBrain(manifest string) bool {
	for _, line := range strings.Split(manifest, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "mode:") {
			val := strings.TrimSpace(strings.TrimPrefix(line, "mode:"))
			return val == "shared"
		}
	}
	return false
}
