package filestore

import (
	"bytes"
	"context"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// setup creates a bare "remote" repo + a local brain repo pointing at
// it via remote.origin.url. Returns the brain directory. The bare repo
// stands in for GitHub — file:// URLs work for everything filestore
// needs (clone, fetch, push, ls-remote).
func setup(t *testing.T) string {
	t.Helper()
	remoteDir := filepath.Join(t.TempDir(), "remote.git")
	if out, err := exec.Command("git", "init", "--bare", remoteDir).CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v\n%s", err, out)
	}
	brainDir := t.TempDir()
	mustRun(t, brainDir, "init", "-q", "-b", "main")
	mustRun(t, brainDir, "config", "user.email", "test@example.com")
	mustRun(t, brainDir, "config", "user.name", "Tester")
	mustRun(t, brainDir, "remote", "add", "origin", "file://"+remoteDir)
	return brainDir
}

// setupGcryptStyle is like setup() but origin URL carries the
// `gcrypt::` prefix, simulating how a real gcrypt brain's remote
// looks. Verifies the plain-URL extraction.
func setupGcryptStyle(t *testing.T) string {
	t.Helper()
	remoteDir := filepath.Join(t.TempDir(), "remote.git")
	if out, err := exec.Command("git", "init", "--bare", remoteDir).CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v\n%s", err, out)
	}
	brainDir := t.TempDir()
	mustRun(t, brainDir, "init", "-q", "-b", "main")
	mustRun(t, brainDir, "config", "user.email", "test@example.com")
	mustRun(t, brainDir, "config", "user.name", "Tester")
	mustRun(t, brainDir, "remote", "add", "origin", "gcrypt::file://"+remoteDir)
	return brainDir
}

func mustRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func TestReadPlainOriginURL_StripsGcryptPrefix(t *testing.T) {
	brain := setupGcryptStyle(t)
	url, err := readPlainOriginURL(brain)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(url, "file://") || strings.HasPrefix(url, "gcrypt::") {
		t.Errorf("plain URL = %q, want file:// without gcrypt:: prefix", url)
	}
}

func TestReadPlainOriginURL_PlainURLUnchanged(t *testing.T) {
	brain := setup(t)
	url, err := readPlainOriginURL(brain)
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(url, "gcrypt::") || !strings.HasPrefix(url, "file://") {
		t.Errorf("plain URL = %q, want unchanged file://", url)
	}
}

func TestOpen_ErrorsOnMissingRemote(t *testing.T) {
	brainDir := t.TempDir()
	mustRun(t, brainDir, "init", "-q", "-b", "main")
	if _, err := Open(brainDir, "keys"); err == nil {
		t.Error("Open should error when no origin remote is configured")
	}
}

func TestPutListDelete_Roundtrip(t *testing.T) {
	brain := setup(t)
	store, err := Open(brain, "keys")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()

	// Initially empty (orphan branch, no commits yet).
	if got, err := store.List(ctx); err != nil {
		t.Fatal(err)
	} else if len(got) != 0 {
		t.Errorf("initial List = %d entries, want 0", len(got))
	}

	// Put two files.
	if err := store.Put(ctx, "alice.asc", []byte("pubkey-alice")); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(ctx, "bob.asc", []byte("pubkey-bob")); err != nil {
		t.Fatal(err)
	}

	got, err := store.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("List after 2 Puts = %d entries, want 2", len(got))
	}
	if !bytes.Equal(got["alice.asc"], []byte("pubkey-alice")) {
		t.Errorf("alice.asc content mismatch: %q", got["alice.asc"])
	}
	if !bytes.Equal(got["bob.asc"], []byte("pubkey-bob")) {
		t.Errorf("bob.asc content mismatch: %q", got["bob.asc"])
	}

	// Delete one.
	if err := store.Delete(ctx, "alice.asc"); err != nil {
		t.Fatal(err)
	}
	got, err = store.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got["alice.asc"]; ok {
		t.Error("alice.asc should be deleted")
	}
	if !bytes.Equal(got["bob.asc"], []byte("pubkey-bob")) {
		t.Errorf("bob.asc still expected; got %q", got["bob.asc"])
	}
}

func TestPut_IdempotentOnIdenticalContent(t *testing.T) {
	brain := setup(t)
	store, err := Open(brain, "keys")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()

	if err := store.Put(ctx, "k.asc", []byte("same")); err != nil {
		t.Fatal(err)
	}
	// A second Put with identical content should be a no-op (no
	// empty commit, no extra push). Hard to assert directly — we
	// just verify no error and the content stays the same.
	if err := store.Put(ctx, "k.asc", []byte("same")); err != nil {
		t.Fatal(err)
	}

	got, err := store.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got["k.asc"], []byte("same")) {
		t.Errorf("content = %q, want %q", got["k.asc"], "same")
	}
}

func TestPut_OverwritesExistingContent(t *testing.T) {
	brain := setup(t)
	store, err := Open(brain, "keys")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()

	if err := store.Put(ctx, "k.asc", []byte("v1")); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(ctx, "k.asc", []byte("v2")); err != nil {
		t.Fatal(err)
	}

	got, err := store.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got["k.asc"], []byte("v2")) {
		t.Errorf("content = %q, want v2", got["k.asc"])
	}
}

func TestDelete_IdempotentOnAbsent(t *testing.T) {
	brain := setup(t)
	store, err := Open(brain, "keys")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()

	// Delete on never-existed file should not error.
	if err := store.Delete(ctx, "never-existed.asc"); err != nil {
		t.Errorf("Delete on absent: %v", err)
	}

	// Put + Delete + Delete should also be clean.
	if err := store.Put(ctx, "transient.asc", []byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(ctx, "transient.asc"); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(ctx, "transient.asc"); err != nil {
		t.Errorf("second Delete: %v", err)
	}
}

func TestPut_RejectsPathSeparators(t *testing.T) {
	brain := setup(t)
	store, err := Open(brain, "keys")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()

	if err := store.Put(ctx, "sub/foo.asc", []byte("x")); err == nil {
		t.Error("Put with '/' in name should error (flat namespace)")
	}
	if err := store.Put(ctx, `sub\foo.asc`, []byte("x")); err == nil {
		t.Error("Put with '\\' in name should error (flat namespace)")
	}
}

// TestList_FromSecondClient verifies that opening a second filestore
// against the same remote sees content published by the first. Models
// the peer-discovers-newly-added-pubkey case via the brain-sync watcher.
func TestList_FromSecondClient(t *testing.T) {
	// Shared remote.
	remoteDir := filepath.Join(t.TempDir(), "remote.git")
	if out, err := exec.Command("git", "init", "--bare", remoteDir).CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v\n%s", err, out)
	}
	remoteURL := "file://" + remoteDir

	// Brain A — publisher.
	brainA := t.TempDir()
	mustRun(t, brainA, "init", "-q", "-b", "main")
	mustRun(t, brainA, "config", "user.email", "a@example.com")
	mustRun(t, brainA, "config", "user.name", "Brain A")
	mustRun(t, brainA, "remote", "add", "origin", remoteURL)

	// Brain B — consumer (separate clone of the same shared remote).
	brainB := t.TempDir()
	mustRun(t, brainB, "init", "-q", "-b", "main")
	mustRun(t, brainB, "config", "user.email", "b@example.com")
	mustRun(t, brainB, "config", "user.name", "Brain B")
	mustRun(t, brainB, "remote", "add", "origin", remoteURL)

	ctx := context.Background()
	storeA, err := Open(brainA, "keys")
	if err != nil {
		t.Fatal(err)
	}
	defer storeA.Close()

	if err := storeA.Put(ctx, "alice.asc", []byte("alice-pub")); err != nil {
		t.Fatal(err)
	}
	if err := storeA.Put(ctx, "bob.asc", []byte("bob-pub")); err != nil {
		t.Fatal(err)
	}

	// Brain B opens later — it should clone the existing branch
	// (not re-init as orphan) and see both entries.
	storeB, err := Open(brainB, "keys")
	if err != nil {
		t.Fatal(err)
	}
	defer storeB.Close()

	got, err := storeB.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"alice.asc", "bob.asc"}
	names := make([]string, 0, len(got))
	for n := range got {
		names = append(names, n)
	}
	sort.Strings(names)
	if !equalStringSlices(names, want) {
		t.Errorf("Brain B sees %v, want %v", names, want)
	}
	if !bytes.Equal(got["alice.asc"], []byte("alice-pub")) {
		t.Errorf("alice.asc content drift across clients: %q", got["alice.asc"])
	}
}

// TestList_NewEntriesPropagate exercises the periodic-refresh contract:
// after Brain A adds a new entry, Brain B's next List call picks it up
// without needing to re-Open. Models the brain-sync watcher's per-tick
// fetch behavior.
func TestList_NewEntriesPropagate(t *testing.T) {
	remoteDir := filepath.Join(t.TempDir(), "remote.git")
	if out, err := exec.Command("git", "init", "--bare", remoteDir).CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v\n%s", err, out)
	}
	remoteURL := "file://" + remoteDir

	brainA := t.TempDir()
	mustRun(t, brainA, "init", "-q", "-b", "main")
	mustRun(t, brainA, "config", "user.email", "a@example.com")
	mustRun(t, brainA, "config", "user.name", "Brain A")
	mustRun(t, brainA, "remote", "add", "origin", remoteURL)

	brainB := t.TempDir()
	mustRun(t, brainB, "init", "-q", "-b", "main")
	mustRun(t, brainB, "config", "user.email", "b@example.com")
	mustRun(t, brainB, "config", "user.name", "Brain B")
	mustRun(t, brainB, "remote", "add", "origin", remoteURL)

	ctx := context.Background()
	storeA, _ := Open(brainA, "keys")
	defer storeA.Close()
	if err := storeA.Put(ctx, "v1.asc", []byte("one")); err != nil {
		t.Fatal(err)
	}

	storeB, _ := Open(brainB, "keys")
	defer storeB.Close()
	if got, _ := storeB.List(ctx); len(got) != 1 {
		t.Fatalf("Brain B initial = %d, want 1", len(got))
	}

	// Brain A publishes another after Brain B is open.
	if err := storeA.Put(ctx, "v2.asc", []byte("two")); err != nil {
		t.Fatal(err)
	}

	// Brain B's next List should refresh from origin and see both.
	got, err := storeB.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Errorf("Brain B after new push = %d entries, want 2 (refresh broken?)", len(got))
	}
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
