package brainsync

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// shortCommit / shortPush keep autosave tests under a second while
// still exercising the debounce contract (events within the window
// coalesce; quiet after the window fires the timer).
const (
	shortCommit = 80 * time.Millisecond
	shortPush   = 200 * time.Millisecond
	// settle is a slack we wait on top of a debounce window to let the
	// timer fire and the resulting git operation complete. Generous to
	// avoid flakes on CI / loaded machines; the test still runs fast.
	settle = 300 * time.Millisecond
)

// readCommitMessages returns commit subjects in HEAD-first order.
func readCommitMessages(t *testing.T, repo string) []string {
	t.Helper()
	out, err := RunGit(repo, "log", "--format=%s")
	if err != nil {
		t.Fatalf("log: %v", err)
	}
	if len(out) == 0 {
		return nil
	}
	return strings.Split(strings.TrimRight(string(out), "\n"), "\n")
}

// TestAutoCommitter_CommitsModifiedTrackedFile is the golden path:
// modify a tracked file, wait the debounce, observe an autosave
// commit landed.
func TestAutoCommitter_CommitsModifiedTrackedFile(t *testing.T) {
	_, peerA, _ := twoPeerRepo(t)

	a, err := NewAutoCommitter(peerA, "test", shortCommit, shortPush, true, false)
	if err != nil {
		t.Fatalf("NewAutoCommitter: %v", err)
	}
	defer a.Stop()

	must(t, os.WriteFile(filepath.Join(peerA, "paris.md"), []byte("Day 1: arrive\nDay 2: louvre\n"), 0o644))

	time.Sleep(shortCommit + settle)

	msgs := readCommitMessages(t, peerA)
	if len(msgs) == 0 || !strings.HasPrefix(msgs[0], "autosave:") {
		t.Fatalf("expected an autosave commit on top, got %v", msgs)
	}
}

// TestAutoCommitter_DebouncesBurstOfSaves verifies multiple writes
// within the debounce window collapse to a single commit.
func TestAutoCommitter_DebouncesBurstOfSaves(t *testing.T) {
	_, peerA, _ := twoPeerRepo(t)

	a, err := NewAutoCommitter(peerA, "test", shortCommit, shortPush, true, false)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Stop()

	// 5 saves spaced well within the debounce window.
	for i := 0; i < 5; i++ {
		must(t, os.WriteFile(filepath.Join(peerA, "paris.md"),
			[]byte("Day "+string(rune('A'+i))+"\n"), 0o644))
		time.Sleep(shortCommit / 4)
	}
	time.Sleep(shortCommit + settle)

	msgs := readCommitMessages(t, peerA)
	autosaves := 0
	for _, m := range msgs {
		if strings.HasPrefix(m, "autosave:") {
			autosaves++
		}
	}
	if autosaves != 1 {
		t.Errorf("expected exactly 1 autosave commit after debounced burst, got %d (history: %v)", autosaves, msgs)
	}
}

// TestAutoCommitter_SkipsUntracked verifies untracked files are not
// auto-staged: a brand-new file appearing in the brain dir does NOT
// produce a commit, but the daemon log gets an explicit-add hint
// (we verify the no-commit half; hint deduping is exercised
// separately).
func TestAutoCommitter_SkipsUntracked(t *testing.T) {
	_, peerA, _ := twoPeerRepo(t)

	a, err := NewAutoCommitter(peerA, "test", shortCommit, shortPush, true, false)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Stop()

	preCount := len(readCommitMessages(t, peerA))
	must(t, os.WriteFile(filepath.Join(peerA, "tokyo.md"), []byte("new file\n"), 0o644))

	time.Sleep(shortCommit + settle)

	postCount := len(readCommitMessages(t, peerA))
	if postCount != preCount {
		t.Errorf("untracked file produced an autosave commit (commits went %d→%d); should require explicit git add",
			preCount, postCount)
	}
	// Status should still show the file as untracked, untouched.
	out, err := RunGit(peerA, "status", "--porcelain")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "?? tokyo.md") {
		t.Errorf("expected tokyo.md still untracked, got status:\n%s", out)
	}
}

// TestAutoCommitter_SkipsUnstagedDeletion verifies deletion of a
// tracked file is left for explicit `git rm` — autosave doesn't
// quietly drop tracked content.
func TestAutoCommitter_SkipsUnstagedDeletion(t *testing.T) {
	_, peerA, _ := twoPeerRepo(t)

	a, err := NewAutoCommitter(peerA, "test", shortCommit, shortPush, true, false)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Stop()

	preCount := len(readCommitMessages(t, peerA))
	must(t, os.Remove(filepath.Join(peerA, "paris.md")))

	time.Sleep(shortCommit + settle)

	postCount := len(readCommitMessages(t, peerA))
	if postCount != preCount {
		t.Errorf("deletion produced an autosave commit (commits went %d→%d); should require explicit git rm",
			preCount, postCount)
	}
}

// TestAutoCommitter_SkipsDuringMerge: with .git/MERGE_HEAD present,
// no autosave commit should land — we don't want to stomp on an
// operator-driven merge.
func TestAutoCommitter_SkipsDuringMerge(t *testing.T) {
	_, peerA, _ := twoPeerRepo(t)

	// Fake an in-progress merge.
	must(t, os.WriteFile(filepath.Join(peerA, ".git", "MERGE_HEAD"),
		[]byte("deadbeef\n"), 0o644))

	a, err := NewAutoCommitter(peerA, "test", shortCommit, shortPush, true, false)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Stop()

	preCount := len(readCommitMessages(t, peerA))
	must(t, os.WriteFile(filepath.Join(peerA, "paris.md"), []byte("during merge\n"), 0o644))

	time.Sleep(shortCommit + settle)

	postCount := len(readCommitMessages(t, peerA))
	if postCount != preCount {
		t.Errorf("autosave committed during in-progress merge (commits %d→%d); should defer", preCount, postCount)
	}
}

// TestAutoCommitter_SkipsNonSyncBranch: autosave is bound to the sync branch
// (main). On a non-main branch — e.g. a review/<slug> workbench branch that
// owns its own deliberate commit cadence — the daemon must stand down and not
// commit underneath it. (The commit-on-main path is the golden test above.)
func TestAutoCommitter_SkipsNonSyncBranch(t *testing.T) {
	_, peerA, _ := twoPeerRepo(t)
	mustGit(t, peerA, "checkout", "-q", "-b", "review/pvp")

	a, err := NewAutoCommitter(peerA, "test", shortCommit, shortPush, true, false)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Stop()

	preCount := len(readCommitMessages(t, peerA))
	must(t, os.WriteFile(filepath.Join(peerA, "paris.md"), []byte("edited on a review branch\n"), 0o644))
	time.Sleep(shortCommit + settle)

	if postCount := len(readCommitMessages(t, peerA)); postCount != preCount {
		t.Errorf("autosave committed on review/pvp (commits %d→%d); should stand down off the sync branch", preCount, postCount)
	}
	// The edit must remain uncommitted (modified-tracked), not silently dropped.
	out, err := RunGit(peerA, "status", "--porcelain")
	must(t, err)
	if !strings.Contains(string(out), "paris.md") {
		t.Errorf("expected paris.md to remain modified-uncommitted on the review branch, status:\n%s", out)
	}
}

// TestAutoCommitter_SkipsDetachedHead: on a detached HEAD (CurrentBranch → "")
// the daemon also stands down — a detached HEAD isn't the sync branch.
func TestAutoCommitter_SkipsDetachedHead(t *testing.T) {
	_, peerA, _ := twoPeerRepo(t)
	head, err := RunGit(peerA, "rev-parse", "HEAD")
	must(t, err)
	mustGit(t, peerA, "checkout", "-q", strings.TrimSpace(string(head)))

	a, err := NewAutoCommitter(peerA, "test", shortCommit, shortPush, true, false)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Stop()

	preCount := len(readCommitMessages(t, peerA))
	must(t, os.WriteFile(filepath.Join(peerA, "paris.md"), []byte("edited on detached HEAD\n"), 0o644))
	time.Sleep(shortCommit + settle)

	if postCount := len(readCommitMessages(t, peerA)); postCount != preCount {
		t.Errorf("autosave committed on a detached HEAD (commits %d→%d); should stand down", preCount, postCount)
	}
}

// TestAutoCommitter_WatchesNewSubdirs: a new subdirectory created
// at runtime should be picked up by the recursive watcher, so a
// file inside it triggers an autosave once added to the index.
//
// Specifically: we expect the *write event* on the new subdir file
// to propagate. Since the file is untracked at this point, no
// commit lands — but the test still proves the watch attaches.
// To verify a commit DOES land, we git add it first, then modify.
func TestAutoCommitter_WatchesNewSubdirs(t *testing.T) {
	_, peerA, _ := twoPeerRepo(t)

	a, err := NewAutoCommitter(peerA, "test", shortCommit, shortPush, true, false)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Stop()

	sub := filepath.Join(peerA, "notes", "tokyo")
	must(t, os.MkdirAll(sub, 0o755))

	// Give the watcher a moment to pick up the new dirs (fsnotify
	// CREATE → our addRecursive). 1 commit-debounce window is more
	// than enough.
	time.Sleep(shortCommit + settle)

	// Add a tracked file inside the new subdir.
	tracked := filepath.Join(sub, "day1.md")
	must(t, os.WriteFile(tracked, []byte("arrived\n"), 0o644))
	mustGit(t, peerA, "add", tracked)
	mustGit(t, peerA, "commit", "-q", "-m", "track new subdir file")

	// Now modify it — should trigger autosave through the new-subdir watch.
	preCount := len(readCommitMessages(t, peerA))
	must(t, os.WriteFile(tracked, []byte("arrived; settled in\n"), 0o644))

	time.Sleep(shortCommit + settle)

	msgs := readCommitMessages(t, peerA)
	if len(msgs) != preCount+1 {
		t.Fatalf("expected 1 new autosave commit after modifying file in new subdir, got %d new commits (history: %v)",
			len(msgs)-preCount, msgs)
	}
	if !strings.HasPrefix(msgs[0], "autosave:") {
		t.Errorf("expected new HEAD to be autosave, got %q", msgs[0])
	}
}

// TestAutoCommitter_PushDebounceFires verifies the push timer
// eventually pushes a committed change to the bare origin.
func TestAutoCommitter_PushDebounceFires(t *testing.T) {
	bare, peerA, _ := twoPeerRepo(t)

	a, err := NewAutoCommitter(peerA, "test", shortCommit, shortPush, true, false)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Stop()

	must(t, os.WriteFile(filepath.Join(peerA, "paris.md"), []byte("pushed via debounce\n"), 0o644))

	// Wait long enough for: commit debounce + push debounce + slack.
	time.Sleep(shortCommit + shortPush + settle)

	// The bare remote should now have the autosave commit on main.
	out, err := RunGit(bare, "log", "main", "--format=%s")
	if err != nil {
		t.Fatalf("log on bare: %v", err)
	}
	if !strings.Contains(string(out), "autosave:") {
		t.Errorf("expected autosave commit on origin/main, got log:\n%s", out)
	}
}

// TestAutoCommitter_CommitOnlyNeverPushes verifies the nous#47
// commit-only mode (push=false): edits still commit locally as a safety
// net, but nothing is ever flushed to origin even after the push
// debounce window elapses.
func TestAutoCommitter_CommitOnlyNeverPushes(t *testing.T) {
	bare, peerA, _ := twoPeerRepo(t)

	a, err := NewAutoCommitter(peerA, "test", shortCommit, shortPush, false, false)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Stop()

	must(t, os.WriteFile(filepath.Join(peerA, "paris.md"), []byte("local only\n"), 0o644))

	// Wait past commit + push windows: a push, if it were going to
	// happen, would have fired by now.
	time.Sleep(shortCommit + shortPush + settle)

	// Local commit landed (the safety net).
	msgs := readCommitMessages(t, peerA)
	if len(msgs) == 0 || !strings.HasPrefix(msgs[0], "autosave:") {
		t.Fatalf("expected a local autosave commit, got %v", msgs)
	}

	// Origin must NOT have received it.
	out, err := RunGit(bare, "log", "main", "--format=%s")
	if err != nil {
		t.Fatalf("log on bare: %v", err)
	}
	if strings.Contains(string(out), "autosave:") {
		t.Errorf("commit-only committer pushed to origin/main — got log:\n%s", out)
	}
}

// TestAutoCommitter_RefChangedSignalResetsPushTimer: invoking
// NotifyRefChange (simulating RefWatcher seeing a manual commit)
// should produce a push after the debounce window, even when the
// content watcher saw nothing.
func TestAutoCommitter_RefChangedSignalResetsPushTimer(t *testing.T) {
	bare, peerA, _ := twoPeerRepo(t)

	a, err := NewAutoCommitter(peerA, "test", shortCommit, shortPush, true, false)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Stop()

	// Operator commits manually outside autosave.
	must(t, os.WriteFile(filepath.Join(peerA, "paris.md"), []byte("manual\n"), 0o644))
	mustGit(t, peerA, "add", "paris.md")
	mustGit(t, peerA, "commit", "-q", "-m", "manual checkpoint")

	// Signal the autocommitter as RefWatcher would.
	a.NotifyRefChange()

	time.Sleep(shortPush + settle)

	out, err := RunGit(bare, "log", "main", "--format=%s")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "manual checkpoint") {
		t.Errorf("expected manual commit pushed via debounce after NotifyRefChange, got log:\n%s", out)
	}
}
