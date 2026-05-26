package brainsync

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRemoteRawURL_StripsGcryptPrefix(t *testing.T) {
	repo := initRepoForTest(t)
	mustGit(t, repo, "remote", "add", "origin", "gcrypt::ssh://git@github.com/x/y.git")

	got, err := RemoteRawURL(repo)
	if err != nil {
		t.Fatalf("RemoteRawURL: %v", err)
	}
	want := "ssh://git@github.com/x/y.git"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRemoteRawURL_PassesThroughPlain(t *testing.T) {
	repo := initRepoForTest(t)
	mustGit(t, repo, "remote", "add", "origin", "ssh://git@github.com/x/y.git")

	got, err := RemoteRawURL(repo)
	if err != nil {
		t.Fatalf("RemoteRawURL: %v", err)
	}
	want := "ssh://git@github.com/x/y.git"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRemoteRawURL_NoOrigin(t *testing.T) {
	repo := initRepoForTest(t)
	if _, err := RemoteRawURL(repo); err == nil {
		t.Error("want error when origin is unset, got nil")
	}
}

func TestLsRemoteRaw_StableOrdering(t *testing.T) {
	// Bare repo with several branches; ls-remote output should be
	// byte-stable across calls (sorted serialization).
	bare := t.TempDir()
	if out, err := exec.Command("git", "-C", bare, "init", "--bare", "-q", "-b", "main").CombinedOutput(); err != nil {
		t.Fatalf("init bare: %v: %s", err, out)
	}
	work := initRepoForTest(t)
	mustGit(t, work, "remote", "add", "origin", bare)
	if err := os.WriteFile(filepath.Join(work, "f"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, work, "add", ".")
	mustGit(t, work, "commit", "-m", "init")
	mustGit(t, work, "push", "origin", "main")
	mustGit(t, work, "branch", "keys")
	mustGit(t, work, "push", "origin", "keys")

	a, err := LsRemoteRaw(bare)
	if err != nil {
		t.Fatalf("LsRemoteRaw a: %v", err)
	}
	b, err := LsRemoteRaw(bare)
	if err != nil {
		t.Fatalf("LsRemoteRaw b: %v", err)
	}
	if a != b {
		t.Errorf("expected byte-stable serialization across calls;\nfirst:  %q\nsecond: %q", a, b)
	}
	if !strings.Contains(a, "refs/heads/main") || !strings.Contains(a, "refs/heads/keys") {
		t.Errorf("expected both refs in snapshot, got: %q", a)
	}
}

func TestLsRemoteRaw_DetectsChange(t *testing.T) {
	// Push, snapshot. Push again, snapshot — must differ.
	bare := t.TempDir()
	if out, err := exec.Command("git", "-C", bare, "init", "--bare", "-q", "-b", "main").CombinedOutput(); err != nil {
		t.Fatalf("init bare: %v: %s", err, out)
	}
	work := initRepoForTest(t)
	mustGit(t, work, "remote", "add", "origin", bare)
	if err := os.WriteFile(filepath.Join(work, "a"), []byte("1"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, work, "add", ".")
	mustGit(t, work, "commit", "-m", "c1")
	mustGit(t, work, "push", "origin", "main")

	before, err := LsRemoteRaw(bare)
	if err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(work, "a"), []byte("2"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, work, "add", ".")
	mustGit(t, work, "commit", "-m", "c2")
	mustGit(t, work, "push", "origin", "main")

	after, err := LsRemoteRaw(bare)
	if err != nil {
		t.Fatal(err)
	}
	if before == after {
		t.Errorf("expected snapshots to differ after push; both = %q", before)
	}
}

func TestLsRemoteRaw_BadURL(t *testing.T) {
	if _, err := LsRemoteRaw("/nonexistent/path/that/is/not/a/repo"); err == nil {
		t.Error("want error on bad url, got nil")
	}
}
