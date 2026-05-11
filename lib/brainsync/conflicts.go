package brainsync

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Conflict file discovery — the read-side of the convention defined by
// `conflictPath` + `Resolve`. The substrate (brainsync's Resolve) renames
// the losing side of a push-rejection to `<stem>.conflict-<peer>-<utc>.<ext>`
// and checks out the canonical side; the `/nous-resolve` skill walks
// the tree for those files, loads both versions, AI-merges, snapshots,
// commits.
//
// This file exposes the walk-and-parse step as a public Go API so
// `nous brain resolve <path>` can emit structured output instead of
// the skill grepping `find`. The semantic merge itself stays in the
// skill — this surface is mechanical only.

// Conflict describes one unresolved conflict file in a brain. Canonical
// is the on-disk path the conflict file is shadowing (i.e., the
// canonical version git checked out); ConflictFile is the loser
// version preserved alongside it. Peer is the brainsync peer-ID
// embedded in the filename (typically a hostname slug like
// `xianxu-mbp`). At is the UTC timestamp the conflict was created.
type Conflict struct {
	// Canonical is the path (relative to brainRoot) of the
	// canonical version — what was on disk before the conflict file
	// was written next to it.
	Canonical string

	// ConflictFile is the path (relative to brainRoot) of the
	// loser version preserved by Resolve.
	ConflictFile string

	// Peer is the brainsync peer-ID embedded in the conflict
	// filename. For local resolution this is informational; the
	// skill uses it in commit messages.
	Peer string

	// At is the UTC timestamp parsed from the filename.
	At time.Time
}

// conflictFileRE captures peer + timestamp + optional extension from
// the brainsync convention `<stem>.conflict-<peer>-<utc>.<ext>`.
//
// Note: lib/brain/status.go has a sibling regex doing the same
// match-only walk for the TUI's read-only conflict count. The two
// regexes are kept in sync but not shared, since lib/brain → lib/brainsync
// would close the existing brainsync → brain dep cycle. Cost of
// duplication is ~5 lines; cost of breaking the cycle is a new
// shared package. Live with the duplication.
var conflictFileRE = regexp.MustCompile(
	`^(.+)\.conflict-([^/]+)-([0-9]{8}T[0-9]{6}Z)(\.[^/.]+)?$`)

// ConflictFiles walks brainRoot for conflict files matching the
// brainsync convention and returns them in (canonical-relpath, peer)
// sort order. Skips .git/ and .brain/ (substrate, not content).
//
// Returns an empty slice + nil on a clean brain. Errors only on a
// failed root walk (permissions, etc.); per-file parse failures
// (timestamp doesn't parse, etc.) are silently skipped — the walk is
// best-effort.
func ConflictFiles(brainRoot string) ([]Conflict, error) {
	abs, err := filepath.Abs(brainRoot)
	if err != nil {
		return nil, err
	}
	var out []Conflict
	walkErr := filepath.WalkDir(abs, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			if name == ".git" || name == ".brain" {
				return fs.SkipDir
			}
			return nil
		}
		c, ok := parseConflictName(path, abs)
		if !ok {
			return nil
		}
		out = append(out, c)
		return nil
	})
	if walkErr != nil {
		return nil, fmt.Errorf("walk %s: %w", abs, walkErr)
	}
	return out, nil
}

// parseConflictName parses a single absolute path into a Conflict if
// the basename matches the convention. Returns (Conflict{}, false)
// on mismatch.
func parseConflictName(absPath, brainRoot string) (Conflict, bool) {
	rel, err := filepath.Rel(brainRoot, absPath)
	if err != nil {
		return Conflict{}, false
	}
	dir, base := filepath.Split(rel)
	m := conflictFileRE.FindStringSubmatch(base)
	if m == nil {
		return Conflict{}, false
	}
	stem := m[1]
	peer := m[2]
	ts := m[3]
	ext := m[4] // may be empty
	at, err := time.Parse("20060102T150405Z", ts)
	if err != nil {
		return Conflict{}, false
	}
	canonicalBase := stem + ext
	canonical := canonicalBase
	if dir != "" {
		canonical = filepath.Join(dir, canonicalBase)
	}
	conflictRel := rel
	// Normalize separators on windows-style joins (no-op on posix).
	conflictRel = filepath.ToSlash(conflictRel)
	canonical = filepath.ToSlash(canonical)
	return Conflict{
		Canonical:    canonical,
		ConflictFile: conflictRel,
		Peer:         peer,
		At:           at,
	}, true
}

// String returns a stable tabular line for the conflict: the format
// `nous brain resolve` defaults to. JSON output is emitted separately
// by the caller.
func (c Conflict) String() string {
	return strings.Join([]string{
		c.Canonical,
		c.ConflictFile,
		c.Peer,
		c.At.UTC().Format(time.RFC3339),
	}, "\t")
}
