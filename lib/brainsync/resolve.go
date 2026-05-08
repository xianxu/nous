package brainsync

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// conflictPath returns the path to use for the loser of a conflict.
// Format: <basename>.conflict-<peer>-<utc-iso8601-compact>.<ext>
//
// Example:
//
//	conflictPath("data/travel/paris.md", "xianxu-mbp", t) =
//	    "data/travel/paris.conflict-xianxu-mbp-20260507T221604Z.md"
func conflictPath(orig, peer string, at time.Time) string {
	dir := filepath.Dir(orig)
	base := filepath.Base(orig)
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	ts := at.UTC().Format("20060102T150405Z")
	name := fmt.Sprintf("%s.conflict-%s-%s%s", stem, peer, ts, ext)
	if dir == "." {
		return name
	}
	return filepath.Join(dir, name)
}

// Resolve handles a push rejection by file-level resolution.
//
// Steps:
//  1. fetch origin
//  2. for each file changed locally that origin/main also changed:
//     if content differs: rename local to .conflict-<peer>-<ts>.<ext>
//     and check out origin/main's version
//  3. for each file origin/main changed that we didn't: check it out
//  4. soft-reset HEAD to origin/main; commit "conflict-resolve: ..."
//  5. caller pushes; if still rejected, caller calls Resolve again
//
// Cap retries at the call site.
func Resolve(repo, peer string, now time.Time) error {
	if err := Fetch(repo); err != nil {
		return err
	}

	// Files we changed since merge-base. Three-dot syntax `A...B` shows
	// changes on B relative to merge-base(A, B) — exactly what we want.
	ourChanged, err := changedFiles(repo, "origin/main...HEAD")
	if err != nil {
		return err
	}
	// Files origin/main changed since merge-base.
	remoteChanged, err := changedFiles(repo, "HEAD...origin/main")
	if err != nil {
		return err
	}

	conflicts := intersect(ourChanged, remoteChanged)
	for _, f := range conflicts {
		ours, err1 := readFromGit(repo, "HEAD", f)
		theirs, err2 := readFromGit(repo, "origin/main", f)
		if err1 != nil || err2 != nil {
			continue
		}
		if string(ours) == string(theirs) {
			continue // both made the same change — not actually a conflict
		}
		dst := filepath.Join(repo, conflictPath(f, peer, now))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(dst, ours, 0o644); err != nil {
			return err
		}
		// Stage origin's version as canonical.
		if _, err := RunGit(repo, "checkout", "origin/main", "--", f); err != nil {
			return err
		}
	}

	// Pure remote-only changes: take them.
	for _, f := range remoteChanged {
		if containsString(conflicts, f) {
			continue
		}
		if _, err := RunGit(repo, "checkout", "origin/main", "--", f); err != nil {
			return err
		}
	}

	// Soft-reset HEAD to origin/main: keeps working tree (which has our
	// conflict files + remote's canonical content), rebases HEAD pointer.
	if _, err := RunGit(repo, "reset", "--soft", "origin/main"); err != nil {
		return err
	}

	// Stage everything: at this point conflict files are new untracked
	// files; canonical files match origin/main and won't appear in diff.
	if _, err := RunGit(repo, "add", "-A"); err != nil {
		return err
	}

	// If nothing's staged (no conflict files written, no remote-only
	// changes to take), we're done — origin/main IS our state.
	if _, err := RunGit(repo, "diff", "--cached", "--quiet"); err == nil {
		return nil
	}

	msg := buildConflictMsg(conflicts, peer)
	if _, err := RunGit(repo, "commit", "-m", msg); err != nil {
		return err
	}
	return nil
}

func buildConflictMsg(conflicts []string, peer string) string {
	if len(conflicts) == 0 {
		return "conflict-resolve: incorporate remote changes (no conflicts)"
	}
	return fmt.Sprintf("conflict-resolve: %d conflict file(s) by %s", len(conflicts), peer)
}

// changedFiles returns the list of files in the given diff range.
func changedFiles(repo, refRange string) ([]string, error) {
	out, err := RunGit(repo, "diff", "--name-only", refRange)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		files = append(files, line)
	}
	return files, nil
}

func readFromGit(repo, ref, path string) ([]byte, error) {
	return RunGit(repo, "show", ref+":"+path)
}

func intersect(a, b []string) []string {
	m := make(map[string]struct{}, len(b))
	for _, x := range b {
		m[x] = struct{}{}
	}
	var out []string
	for _, x := range a {
		if _, ok := m[x]; ok {
			out = append(out, x)
		}
	}
	return out
}

func containsString(haystack []string, needle string) bool {
	for _, x := range haystack {
		if x == needle {
			return true
		}
	}
	return false
}
