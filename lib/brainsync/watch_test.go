package brainsync

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPushBrain_NoUnpushed_NoOp(t *testing.T) {
	_, peerA, _ := twoPeerRepo(t)
	// peerA is at origin/main; nothing to push.
	if err := PushBrain(peerA, "peerA", time.Now); err != nil {
		t.Errorf("expected nil, got %v", err)
	}
}

func TestPushBrain_HappyPath(t *testing.T) {
	_, peerA, _ := twoPeerRepo(t)
	must(t, os.WriteFile(filepath.Join(peerA, "tokyo.md"), []byte("plan\n"), 0o644))
	mustGit(t, peerA, "add", "tokyo.md")
	mustGit(t, peerA, "commit", "-q", "-m", "tokyo")

	if err := PushBrain(peerA, "peerA", time.Now); err != nil {
		t.Errorf("PushBrain: %v", err)
	}
}

func TestPushBrain_RejectedThenResolves(t *testing.T) {
	_, peerA, peerB := twoPeerRepo(t)

	must(t, os.WriteFile(filepath.Join(peerA, "paris.md"), []byte("A\n"), 0o644))
	mustGit(t, peerA, "add", "paris.md")
	mustGit(t, peerA, "commit", "-q", "-m", "A: paris")
	mustGit(t, peerA, "push", "-q", "origin", "main")

	must(t, os.WriteFile(filepath.Join(peerB, "paris.md"), []byte("B\n"), 0o644))
	mustGit(t, peerB, "add", "paris.md")
	mustGit(t, peerB, "commit", "-q", "-m", "B: paris")

	at := time.Date(2026, 5, 7, 22, 16, 4, 0, time.UTC)
	now := func() time.Time { return at }

	if err := PushBrain(peerB, "peerB", now); err != nil {
		t.Fatalf("PushBrain: %v", err)
	}
	// Verify B's working tree has both canonical (A's content) + conflict file.
	got, err := os.ReadFile(filepath.Join(peerB, "paris.md"))
	must(t, err)
	if string(got) != "A\n" {
		t.Errorf("paris.md should be A's content, got %q", got)
	}
	expectConflict := filepath.Join(peerB, "paris.conflict-peerB-20260507T221604Z.md")
	got, err = os.ReadFile(expectConflict)
	must(t, err)
	if string(got) != "B\n" {
		t.Errorf("conflict file should be B's content, got %q", got)
	}
}

func TestPullBrain_FastForward(t *testing.T) {
	_, peerA, peerB := twoPeerRepo(t)

	// A pushes a new file.
	must(t, os.WriteFile(filepath.Join(peerA, "tokyo.md"), []byte("plan\n"), 0o644))
	mustGit(t, peerA, "add", "tokyo.md")
	mustGit(t, peerA, "commit", "-q", "-m", "A: tokyo")
	mustGit(t, peerA, "push", "-q", "origin", "main")

	// B fetches + ff-pulls; should bring tokyo.md.
	if err := PullBrain(peerB); err != nil {
		t.Fatalf("PullBrain: %v", err)
	}
	if _, err := os.Stat(filepath.Join(peerB, "tokyo.md")); err != nil {
		t.Errorf("tokyo.md should be present after PullBrain: %v", err)
	}
}

func TestPullBrain_DirtyWorkTree_Skips(t *testing.T) {
	_, peerA, peerB := twoPeerRepo(t)

	// A pushes a new file.
	must(t, os.WriteFile(filepath.Join(peerA, "tokyo.md"), []byte("plan\n"), 0o644))
	mustGit(t, peerA, "add", "tokyo.md")
	mustGit(t, peerA, "commit", "-q", "-m", "A: tokyo")
	mustGit(t, peerA, "push", "-q", "origin", "main")

	// B has uncommitted edit; PullBrain should skip the ff-pull.
	must(t, os.WriteFile(filepath.Join(peerB, "paris.md"), []byte("draft B\n"), 0o644))

	if err := PullBrain(peerB); err != nil {
		t.Fatalf("PullBrain: %v", err)
	}
	// tokyo.md should NOT have been pulled in (skipped due to dirty WT).
	if _, err := os.Stat(filepath.Join(peerB, "tokyo.md")); err == nil {
		t.Error("tokyo.md should NOT be present (PullBrain should skip on dirty work tree)")
	}
	// B's edit to paris.md should still be there.
	got, err := os.ReadFile(filepath.Join(peerB, "paris.md"))
	must(t, err)
	if string(got) != "draft B\n" {
		t.Errorf("B's edit should be preserved, got %q", got)
	}
}

func TestPeerIDFor(t *testing.T) {
	repo := initRepoForTest(t)
	got := PeerIDFor(repo)
	if got != "test" {
		t.Errorf("got %q, want %q", got, "test")
	}
}
