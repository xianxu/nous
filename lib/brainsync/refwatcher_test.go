package brainsync

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRefWatcher_FiresOnLocalCommit(t *testing.T) {
	repo := initRepoForTest(t)

	w, err := NewRefWatcher([]string{repo})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	// fsnotify needs the file to exist to watch it. `git init` creates
	// .git/refs/heads/main only on first commit — but our NewRefWatcher
	// already added the path so let's check that case.
	// Actually: `git init -b main` should create the ref... let's not
	// preempt; if the test fails, we'll see what git's behavior actually is.

	if err := os.WriteFile(filepath.Join(repo, "a.md"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, repo, "add", "a.md")
	mustGit(t, repo, "commit", "-q", "-m", "first")

	select {
	case got := <-w.Events():
		if got != repo {
			t.Errorf("got %s, want %s", got, repo)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no event after commit")
	}
}
