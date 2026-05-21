package brain

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ErrNotInBrain is returned by EnclosingBrain when no .brain/config.md
// is found walking up from the start directory.
var ErrNotInBrain = errors.New("not inside a brain (no .brain/config.md found)")

// EnclosingBrain walks up from `start` looking for a directory that
// contains `.brain/config.md`, returning the parsed Manifest of the
// nearest such ancestor. Returns ErrNotInBrain (wrapped) when the
// walk reaches the filesystem root without finding one.
//
// Used by `nous push` to identify the brain the operator is in,
// matching the single-threaded-human assumption: the operator
// acts on one brain at a time, the one whose dir they're cd'd into.
//
// The walk is purely directory-based — no symlink resolution — so
// behavior matches what the operator typed (e.g. ~/repo/.../brain1
// in a tart-VM symlink layout).
func EnclosingBrain(start string) (Manifest, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return Manifest{}, fmt.Errorf("resolve %s: %w", start, err)
	}
	for {
		cfg := filepath.Join(dir, ".brain", "config.md")
		if _, err := os.Stat(cfg); err == nil {
			return Read(dir)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return Manifest{}, fmt.Errorf("%w (searched up from %s)", ErrNotInBrain, start)
		}
		dir = parent
	}
}
