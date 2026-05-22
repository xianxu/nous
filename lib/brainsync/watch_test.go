package brainsync

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPushBrain_NoUnpushed_NoOp(t *testing.T) {
	_, peerA, _ := twoPeerRepo(t)
	pushed, err := PushBrain(peerA, "peerA", time.Now)
	if err != nil {
		t.Errorf("expected nil err, got %v", err)
	}
	if pushed {
		t.Error("nothing to push but pushed=true")
	}
}

func TestPushBrain_HappyPath(t *testing.T) {
	_, peerA, _ := twoPeerRepo(t)
	must(t, os.WriteFile(filepath.Join(peerA, "tokyo.md"), []byte("plan\n"), 0o644))
	mustGit(t, peerA, "add", "tokyo.md")
	mustGit(t, peerA, "commit", "-q", "-m", "tokyo")

	pushed, err := PushBrain(peerA, "peerA", time.Now)
	if err != nil {
		t.Errorf("PushBrain: %v", err)
	}
	if !pushed {
		t.Error("expected pushed=true after fresh commit")
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

	if _, err := PushBrain(peerB, "peerB", now); err != nil {
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

	must(t, os.WriteFile(filepath.Join(peerA, "tokyo.md"), []byte("plan\n"), 0o644))
	mustGit(t, peerA, "add", "tokyo.md")
	mustGit(t, peerA, "commit", "-q", "-m", "A: tokyo")
	mustGit(t, peerA, "push", "-q", "origin", "main")

	res, err := PullBrain(peerB)
	if err != nil {
		t.Fatalf("PullBrain: %v", err)
	}
	if !res.Pulled {
		t.Errorf("expected Pulled=true (remote was strictly ahead); skip reason: %q", res.SkipReason)
	}
	if _, err := os.Stat(filepath.Join(peerB, "tokyo.md")); err != nil {
		t.Errorf("tokyo.md should be present after PullBrain: %v", err)
	}
}

func TestPullBrain_DirtyWorkTree_Skips(t *testing.T) {
	_, peerA, peerB := twoPeerRepo(t)

	must(t, os.WriteFile(filepath.Join(peerA, "tokyo.md"), []byte("plan\n"), 0o644))
	mustGit(t, peerA, "add", "tokyo.md")
	mustGit(t, peerA, "commit", "-q", "-m", "A: tokyo")
	mustGit(t, peerA, "push", "-q", "origin", "main")

	must(t, os.WriteFile(filepath.Join(peerB, "paris.md"), []byte("draft B\n"), 0o644))

	res, err := PullBrain(peerB)
	if err != nil {
		t.Fatalf("PullBrain: %v", err)
	}
	if res.Pulled {
		t.Error("expected Pulled=false (tracked-file change in working tree)")
	}
	if res.SkipReason == "" {
		t.Error("expected a SkipReason explaining why pull was a no-op")
	}
	if _, err := os.Stat(filepath.Join(peerB, "tokyo.md")); err == nil {
		t.Error("tokyo.md should NOT be present (PullBrain should skip on dirty work tree)")
	}
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

func TestPeerIDFor_FallbackToHostname(t *testing.T) {
	repo := initRepoForTest(t)
	// Unset user.name so PeerIDFor has to fall back.
	mustGit(t, repo, "config", "--unset", "user.name")
	got := PeerIDFor(repo)
	// Should be the hostname (lowercased, .local stripped). Anything but
	// "unknown-peer" is good — we know hostname is set on macOS test runners.
	if got == "unknown-peer" {
		t.Errorf("PeerIDFor with no user.name returned 'unknown-peer'; expected hostname-derived")
	}
	if got != strings.ToLower(got) {
		t.Errorf("expected lowercased: got %q", got)
	}
	if strings.HasSuffix(got, ".local") || strings.HasSuffix(got, ".lan") {
		t.Errorf("expected no .local/.lan suffix: got %q", got)
	}
}
