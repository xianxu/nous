// Package brainsync implements the brain sync daemon.
//
// Identification: a directory is a "brain" iff it contains .brain/config.md.
// Whether the daemon watches it — and what it does for it (commit / push /
// pull / keys-admit) — is the per-brain BrainPolicy (see policy.go). nous#47
// decoupled the commit and push cadences, so every brain gets a local
// autosave commit while auto-push stays opt-in for plain remotes; a brain is
// watched iff its policy is Active().
package brainsync

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/xianxu/nous/lib/brain"
	"github.com/xianxu/nous/lib/workspace"
)

// FindBrains walks each root, returning the absolute path of every brain
// the daemon should watch — i.e. every brain whose BrainPolicy is
// Active() (at least one of commit / push / pull applies). This now
// includes purely-local brains (autosave commit safety net) and private
// plain-remote brains; only a fully opted-out brain (autosave off and
// not a sync participant) is excluded. See policy.go for the derivation.
//
// Walks one level deep — brains live as immediate children of the
// workspace root (parent of nous; see lib/workspace.Root). A nested
// .brain/ inside a brain isn't a distinct brain.
func FindBrains(roots []string) ([]string, error) {
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
			if !ComputePolicy(m, remoteKind(p)).Active() {
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

// FindAllBrainsInWorkspace looks under the workspace root
// (lib/workspace.Root — $WORKSPACE_ROOT, $NOUS_DIR's parent, the running
// binary's grandparent, or $HOME/workspace) for watchable brains. Used as
// the auto-discovery default when the operator doesn't pass --brain
// flags.
func FindAllBrainsInWorkspace() ([]string, error) {
	root, err := workspace.Root()
	if err != nil {
		return nil, err
	}
	return FindBrains([]string{root})
}

