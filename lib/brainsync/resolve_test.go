package brainsync

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestConflictPath(t *testing.T) {
	at := time.Date(2026, 5, 7, 22, 16, 4, 0, time.UTC)
	got := conflictPath("data/life/travel/paris.md", "xianxu-mbp", at)
	want := "data/life/travel/paris.conflict-xianxu-mbp-20260507T221604Z.md"
	if got != want {
		t.Errorf("conflictPath = %s; want %s", got, want)
	}
}

func TestConflictPath_NoExtension(t *testing.T) {
	at := time.Date(2026, 5, 7, 22, 16, 4, 0, time.UTC)
	got := conflictPath("README", "p", at)
	want := "README.conflict-p-20260507T221604Z"
	if got != want {
		t.Errorf("got %s want %s", got, want)
	}
}

func TestConflictPath_RootDir(t *testing.T) {
	at := time.Date(2026, 5, 7, 22, 16, 4, 0, time.UTC)
	got := conflictPath("notes.md", "p", at)
	want := "notes.conflict-p-20260507T221604Z.md"
	if got != want {
		t.Errorf("got %s want %s", got, want)
	}
}

// twoPeerRepo creates a bare remote, an initial commit, and two clones
// (peerA, peerB) of it. Returns the three paths.
func twoPeerRepo(t *testing.T) (bare, peerA, peerB string) {
	t.Helper()
	bare = t.TempDir()
	mustGit(t, bare, "init", "--bare", "-q", "-b", "main")

	// Seed via a temporary clone with one commit, then push.
	seed := t.TempDir()
	mustGit(t, seed, "clone", "-q", bare, ".")
	must(t, os.WriteFile(filepath.Join(seed, "paris.md"), []byte("Day 1: arrive\n"), 0o644))
	mustGit(t, seed, "config", "user.email", "test@nous.local")
	mustGit(t, seed, "config", "user.name", "Test")
	mustGit(t, seed, "add", "paris.md")
	mustGit(t, seed, "commit", "-q", "-m", "seed")
	mustGit(t, seed, "push", "-q", "origin", "main")

	peerA = t.TempDir()
	mustGit(t, peerA, "clone", "-q", bare, ".")
	mustGit(t, peerA, "config", "user.email", "a@nous.local")
	mustGit(t, peerA, "config", "user.name", "A")

	peerB = t.TempDir()
	mustGit(t, peerB, "clone", "-q", bare, ".")
	mustGit(t, peerB, "config", "user.email", "b@nous.local")
	mustGit(t, peerB, "config", "user.name", "B")
	return
}

func TestResolve_FileLevelConflict(t *testing.T) {
	_, peerA, peerB := twoPeerRepo(t)

	// A edits + commits + pushes first.
	must(t, os.WriteFile(filepath.Join(peerA, "paris.md"), []byte("A's plan\n"), 0o644))
	mustGit(t, peerA, "add", "paris.md")
	mustGit(t, peerA, "commit", "-q", "-m", "A: edit")
	mustGit(t, peerA, "push", "-q", "origin", "main")

	// B edits + commits but doesn't push yet.
	must(t, os.WriteFile(filepath.Join(peerB, "paris.md"), []byte("B's plan\n"), 0o644))
	mustGit(t, peerB, "add", "paris.md")
	mustGit(t, peerB, "commit", "-q", "-m", "B: edit")

	// B's push will fail — verify it.
	if err := Push(peerB); err != ErrPushRejected {
		t.Fatalf("expected ErrPushRejected, got %v", err)
	}

	// B resolves.
	at := time.Date(2026, 5, 7, 22, 16, 4, 0, time.UTC)
	if err := Resolve(peerB, "peerB", at); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	mustGit(t, peerB, "push", "-q", "origin", "main")

	// Pull on A; verify both files exist with expected content.
	mustGit(t, peerA, "pull", "-q", "--ff-only", "origin", "main")
	got, err := os.ReadFile(filepath.Join(peerA, "paris.md"))
	must(t, err)
	if string(got) != "A's plan\n" {
		t.Errorf("paris.md should have A's content, got %q", got)
	}
	expectConflict := "paris.conflict-peerB-20260507T221604Z.md"
	got, err = os.ReadFile(filepath.Join(peerA, expectConflict))
	must(t, err)
	if string(got) != "B's plan\n" {
		t.Errorf("conflict file should have B's content, got %q", got)
	}
}

func TestResolve_RemoteOnlyChange(t *testing.T) {
	// A pushes a new file; B has no local commit. B's "Resolve" (or rather
	// just fetch+ff-pull) should bring it in.
	_, peerA, peerB := twoPeerRepo(t)

	must(t, os.WriteFile(filepath.Join(peerA, "tokyo.md"), []byte("plan\n"), 0o644))
	mustGit(t, peerA, "add", "tokyo.md")
	mustGit(t, peerA, "commit", "-q", "-m", "A: tokyo")
	mustGit(t, peerA, "push", "-q", "origin", "main")

	at := time.Date(2026, 5, 7, 22, 16, 4, 0, time.UTC)
	if err := Resolve(peerB, "peerB", at); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if _, err := os.Stat(filepath.Join(peerB, "tokyo.md")); err != nil {
		t.Errorf("tokyo.md should be present after resolve: %v", err)
	}
}
