// Package brainsync implements the shared-brain sync daemon.
//
// Identification: a directory is a "brain" iff it contains .brain/config.md.
// Whether the daemon watches it is the *Shared* test (more than one
// recipient) — the legacy `mode: shared` field is no longer authoritative.
package brainsync

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/xianxu/nous/lib/brain"
	"github.com/xianxu/nous/lib/workspace"
)

// FindSharedBrains walks each root, returning the absolute path of
// every brain that should be watched by the brain-sync daemon. A
// brain is "watchable" when it's either:
//
//   - currently shared (≥2 recipients — the derived signal, see
//     lib/brain.Manifest.Shared); OR
//   - provisioned for github-mediated sharing (has a gcrypt::
//     remote.origin.url configured) even when currently single-
//     recipient. That's the "operator just provisioned, invited a
//     peer, but auto-admit hasn't run yet" state — and brain-sync
//     IS the loop that runs auto-admit, so excluding it creates
//     a chicken-and-egg (auto-admit never fires → recipient count
//     stays at 1 → brain never gets included in the watch list).
//     See nous#26's first manual-test bug.
//
// Truly private brains (single recipient, no gcrypt remote) are
// excluded — there's nothing to sync.
//
// Walks one level deep — brains live as immediate children of the
// workspace root (parent of nous; see lib/workspace.Root). A nested
// .brain/ inside a brain isn't a distinct brain.
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
			if !isWatchable(p, m) {
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

// isWatchable encodes the "should brain-sync watch this?" predicate.
// Split out so the rule is easy to test and easy to evolve when we
// add more sync substrates (e.g., a future "shared brain over an
// HTTPS remote without gcrypt" mode).
func isWatchable(brainRoot string, m brain.Manifest) bool {
	if m.Shared() {
		return true
	}
	return hasGcryptRemote(brainRoot)
}

// hasGcryptRemote checks whether the brain's git remote.origin.url
// starts with "gcrypt::" — the marker for github-mediated shared
// brains (vs. a plain github mirror or no remote at all).
//
// Best-effort: a git outage or missing .git/ directory returns
// false, which lands the brain in the "private" bucket. False
// positives (a brain that was once shared, has the remote
// configured, but the operator now wants it private) are rare and
// recoverable by removing the remote.
func hasGcryptRemote(brainRoot string) bool {
	out, err := exec.Command("git", "-C", brainRoot, "config", "--get", "remote.origin.url").Output()
	if err != nil {
		return false
	}
	return strings.HasPrefix(strings.TrimSpace(string(out)), "gcrypt::")
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

