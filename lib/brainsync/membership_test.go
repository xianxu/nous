package brainsync

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xianxu/nous/lib/brain"
)

// TestPushMembershipChange_RetriesOnConcurrentPush pins nous#41 #6: when a
// concurrent operator pushes a membership/content change first, our push is
// rejected (non-fast-forward). pushMembershipChange must reset to the remote
// and re-apply the (idempotent set-op) mutation on top — converging, not
// clobbering. Plain git (no gpg/gcrypt) — the retry logic is git-level.
func TestPushMembershipChange_RetriesOnConcurrentPush(t *testing.T) {
	work, bare := initRepoWithBareRemote(t)

	const fpA = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	const fpB = "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB"

	// Seed a manifest + push so origin/main exists.
	if err := brain.WriteManifest(work, brain.Manifest{Name: "t", Recipients: []string{fpA}}); err != nil {
		t.Fatalf("seed manifest: %v", err)
	}
	if err := AddCommitPush(work, "seed"); err != nil {
		t.Fatalf("seed push: %v", err)
	}

	// A concurrent operator clones + pushes a change FIRST — origin/main
	// advances past `work`, which never pulled it.
	clone2 := t.TempDir()
	mustGit(t, clone2, "clone", "-q", bare, ".")
	mustGit(t, clone2, "config", "user.email", "c2@nous.local")
	mustGit(t, clone2, "config", "user.name", "C2")
	if err := os.WriteFile(filepath.Join(clone2, "concurrent.txt"), []byte("from clone2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, clone2, "add", "-A")
	mustGit(t, clone2, "commit", "-q", "-m", "concurrent operator change")
	mustGit(t, clone2, "push", "-q", "origin", "main")

	// `work` (stale) runs a membership change: append fpB. The first push is
	// rejected; the wrapper resets to the remote and re-applies.
	applyCalls := 0
	err := pushMembershipChange(work, func() (string, error) {
		applyCalls++
		m, err := brain.Read(work)
		if err != nil {
			return "", err
		}
		m.Recipients = append(m.Recipients, fpB)
		if err := brain.RewriteFrontmatter(work, m); err != nil {
			return "", err
		}
		return "add recipient " + fpB[:8], nil
	})
	if err != nil {
		t.Fatalf("pushMembershipChange: %v", err)
	}
	if applyCalls < 2 {
		t.Errorf("expected a re-apply after the rejected push (>=2 apply calls), got %d", applyCalls)
	}

	// The remote now has BOTH the concurrent operator's file AND the membership
	// change — convergence, not clobber.
	final := t.TempDir()
	mustGit(t, final, "clone", "-q", bare, ".")
	if _, err := os.Stat(filepath.Join(final, "concurrent.txt")); err != nil {
		t.Errorf("concurrent operator's change was clobbered: %v", err)
	}
	fm, err := brain.Read(final)
	if err != nil {
		t.Fatalf("read final manifest: %v", err)
	}
	hasA, hasB := false, false
	for _, r := range fm.Recipients {
		if strings.EqualFold(r, fpA) {
			hasA = true
		}
		if strings.EqualFold(r, fpB) {
			hasB = true
		}
	}
	if !hasA || !hasB {
		t.Errorf("expected both recipients on remote after retry, got %v (hasA=%v hasB=%v)", fm.Recipients, hasA, hasB)
	}
}
