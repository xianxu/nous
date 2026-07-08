package brainsync

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// DefaultCommitDebounce is the quiet-window after the last file
// event before AutoCommitter creates an autosave commit. Short
// enough that a burst of saves coalesces (no commit-per-keystroke);
// long enough that we don't commit mid-typing.
const DefaultCommitDebounce = 5 * time.Second

// DefaultPushDebounce is the quiet-window before AutoCommitter
// flushes commits to the remote. Decouples the cheap, granular
// local-commit cadence from the comparatively expensive (gcrypt +
// network) push cadence — at most one push per minute of edits.
const DefaultPushDebounce = 60 * time.Second

// AutoCommitter runs the daemon-side autosave loop for one brain:
//
//   - Recursive fsnotify on the brain dir (skipping .git/).
//   - 5s commit debounce: any content event resets a commit timer;
//     when it fires, we `git add` any *modified tracked* files
//     and commit them with an `autosave:` prefix message.
//   - 60s push debounce: any content event AND any commit resets a
//     push timer; when it fires, we PushBrain. RefWatcher events
//     for the same brain also reset this timer (via NotifyRefChange),
//     so manual `git commit` from the operator's shell still flows
//     through the same push debounce.
//
// Never auto-stages untracked or deleted files — those require an
// explicit `git add` / `git rm` gesture. When the working tree has
// untracked or deleted files at autosave time, we log a one-line
// hint (deduped against the previous one) instead of touching them.
//
// Concurrency: a single goroutine drives the state machine. Timer
// firings deliver onto channels read in the same select. The git
// operations run inline in that goroutine (no fan-out), so we don't
// need a mutex against ourselves; the only contention is with the
// outer Watch loop calling PushBrain on its own paths, which git's
// .git/index.lock serializes for us.
type AutoCommitter struct {
	brain          string
	peer           string
	commitDebounce time.Duration
	pushDebounce   time.Duration
	push           bool // when false, run commit-only (never auto-push) — nous#47
	verbose        bool

	fs *fsnotify.Watcher

	// Inbound external signals (e.g. RefWatcher event for this brain).
	refChanged chan struct{}

	stop chan struct{}
	done chan struct{}

	// lastUntrackedSig is the joined path set we last logged a hint
	// for. Dedup so the daemon log stays quiet when nothing changed.
	muHint           sync.Mutex
	lastUntrackedSig string
}

// NewAutoCommitter wires up an fsnotify recursive watch on the brain
// content (excluding .git/). Returns a started committer; caller must
// Stop() it on shutdown.
//
// push controls the push half: when false the committer runs commit-only
// (the local autosave safety net) and never flushes to origin — the
// arming of the push debounce becomes a no-op. nous#47 uses this for
// brains whose BrainPolicy has Push=false (e.g. a plain-remote brain with
// no `publish: on` opt-in, or `publish: off`).
func NewAutoCommitter(brain, peer string, commitDebounce, pushDebounce time.Duration, push, verbose bool) (*AutoCommitter, error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	a := &AutoCommitter{
		brain:          brain,
		peer:           peer,
		commitDebounce: commitDebounce,
		pushDebounce:   pushDebounce,
		push:           push,
		verbose:        verbose,
		fs:             w,
		refChanged:     make(chan struct{}, 4),
		stop:           make(chan struct{}),
		done:           make(chan struct{}),
	}
	if err := a.addRecursive(brain); err != nil {
		_ = w.Close()
		return nil, err
	}
	go a.loop()
	return a, nil
}

// NotifyRefChange resets the push debounce. RefWatcher calls this
// when the brain's refs/heads/main changes — covers the case where
// the operator commits manually outside the autosave path; we still
// debounce the push instead of firing immediately.
func (a *AutoCommitter) NotifyRefChange() {
	select {
	case a.refChanged <- struct{}{}:
	default:
		// Channel full — another notification is already pending.
		// One is enough; drop this one.
	}
}

// Stop signals the loop to exit and waits for it.
func (a *AutoCommitter) Stop() {
	close(a.stop)
	<-a.done
	_ = a.fs.Close()
}

func (a *AutoCommitter) loop() {
	defer close(a.done)
	defer log.Printf("autocommit %s: loop exiting", a.brain)
	mode := "commit+push"
	if !a.push {
		mode = "commit-only"
	}
	log.Printf("autocommit %s: loop started (%s, commit-debounce=%v, push-debounce=%v)",
		a.brain, mode, a.commitDebounce, a.pushDebounce)

	// Timers start as nil; we lazy-create them on the first event so
	// a freshly-started AutoCommitter doesn't immediately fire a commit
	// or push attempt against a quiescent brain.
	var (
		commitTimer *time.Timer
		pushTimer   *time.Timer
	)
	// Convert timer channels through a helper so we can nil them out
	// when the timer hasn't been armed (select treats nil channels as
	// permanently-blocking, which is exactly what we want).
	commitC := func() <-chan time.Time {
		if commitTimer == nil {
			return nil
		}
		return commitTimer.C
	}
	pushC := func() <-chan time.Time {
		if pushTimer == nil {
			return nil
		}
		return pushTimer.C
	}

	armCommit := func() {
		if commitTimer == nil {
			commitTimer = time.NewTimer(a.commitDebounce)
		} else {
			// Reset() requires Stop() + drain for correctness when the
			// timer may have already fired and we've not read C yet.
			if !commitTimer.Stop() {
				select {
				case <-commitTimer.C:
				default:
				}
			}
			commitTimer.Reset(a.commitDebounce)
		}
	}
	armPush := func() {
		// Commit-only mode (nous#47): never arm the push timer, so the
		// pushC() case can't fire. Commits still flow; they accumulate
		// locally until an explicit `nous push` or a policy change.
		if !a.push {
			return
		}
		if pushTimer == nil {
			pushTimer = time.NewTimer(a.pushDebounce)
		} else {
			if !pushTimer.Stop() {
				select {
				case <-pushTimer.C:
				default:
				}
			}
			pushTimer.Reset(a.pushDebounce)
		}
	}

	for {
		select {
		case ev, ok := <-a.fs.Events:
			if !ok {
				log.Printf("autocommit %s: fsnotify Events channel closed — watcher dead, AutoCommitter exiting",
					a.brain)
				return
			}
			if a.shouldIgnoreEvent(ev) {
				if a.verbose {
					log.Printf("autocommit %s: ignored event %s on %s",
						a.brain, ev.Op, ev.Name)
				}
				continue
			}
			if a.verbose {
				log.Printf("autocommit %s: event %s on %s",
					a.brain, ev.Op, ev.Name)
			}
			// New directory under the brain → recurse so we keep
			// watching content as the operator creates new subtrees.
			if ev.Op&fsnotify.Create != 0 {
				if fi, err := os.Stat(ev.Name); err == nil && fi.IsDir() {
					if err := a.addRecursive(ev.Name); err != nil && a.verbose {
						log.Printf("autocommit: watch %s: %v", ev.Name, err)
					}
				}
			}
			armCommit()
			armPush()

		case err, ok := <-a.fs.Errors:
			if !ok {
				log.Printf("autocommit %s: fsnotify Errors channel closed", a.brain)
				return
			}
			// Always log — fsnotify errors usually mean dropped events
			// or watch-add failures, both of which the operator wants
			// to know about.
			log.Printf("autocommit %s: fsnotify error: %v", a.brain, err)

		case <-commitC():
			commitTimer = nil
			if a.verbose {
				log.Printf("autocommit %s: commit timer fired", a.brain)
			}
			committed, err := a.performAutocommit()
			if err != nil {
				log.Printf("autocommit %s: %v", a.brain, err)
				continue
			}
			if committed {
				// Always log commits — operator-visible state change.
				log.Printf("autocommit %s: committed", a.brain)
				// Fresh commit → restart push debounce so we wait
				// the full window for any follow-up activity before
				// going to the network.
				armPush()
			} else if a.verbose {
				log.Printf("autocommit %s: timer fired but nothing to commit", a.brain)
			}

		case <-pushC():
			pushTimer = nil
			// Always log the attempt — without this, a hung push
			// (gpg-agent waiting on pinentry, network stall, etc.)
			// produces zero log output until the next event arrives.
			// Operator-visible "I am about to talk to the network."
			log.Printf("autocommit %s: push debounce fired, attempting push", a.brain)
			pushed, err := PushBrain(a.brain, a.peer, time.Now)
			if err != nil {
				log.Printf("autocommit push %s: %v", a.brain, err)
				continue
			}
			if pushed {
				log.Printf("autocommit %s: push complete", a.brain)
			} else if a.verbose {
				log.Printf("autocommit %s: push checked, nothing to send", a.brain)
			}

		case <-a.refChanged:
			// Manual commit / RefWatcher event — push, but debounced.
			if a.verbose {
				log.Printf("autocommit %s: ref-changed signal, re-arming push", a.brain)
			}
			armPush()

		case <-a.stop:
			if commitTimer != nil {
				commitTimer.Stop()
			}
			if pushTimer != nil {
				pushTimer.Stop()
			}
			return
		}
	}
}

// shouldIgnoreEvent filters out events we don't want to react to:
// anything inside the brain's .git/, and editor-temp / OS-junk
// patterns that produce noise without representing operator intent.
func (a *AutoCommitter) shouldIgnoreEvent(ev fsnotify.Event) bool {
	rel, err := filepath.Rel(a.brain, ev.Name)
	if err != nil {
		return true
	}
	if rel == "." {
		return false
	}
	parts := strings.Split(rel, string(filepath.Separator))
	if len(parts) > 0 && parts[0] == ".git" {
		return true
	}
	base := filepath.Base(ev.Name)
	// Common editor / OS noise that doesn't map to real content edits.
	if strings.HasPrefix(base, ".#") || // emacs lock
		strings.HasSuffix(base, "~") || // emacs/vim backup
		strings.HasSuffix(base, ".swp") || strings.HasSuffix(base, ".swo") || // vim swap
		base == ".DS_Store" {
		return true
	}
	return false
}

// addRecursive walks dir and adds every directory (skipping .git/ and
// other ignored basenames) to the fsnotify watcher. Used both at
// startup and dynamically when new subdirs appear.
//
// Errors on individual subdirs are logged-and-skipped at the call
// site; we never propagate "couldn't add this one dir" upward, because
// dropping a subtree from watch is degraded-but-functional, not fatal.
func (a *AutoCommitter) addRecursive(dir string) error {
	return filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable
		}
		if !d.IsDir() {
			return nil
		}
		base := filepath.Base(path)
		if base == ".git" {
			return filepath.SkipDir
		}
		if err := a.fs.Add(path); err != nil && a.verbose {
			log.Printf("autocommit: watch add %s: %v", path, err)
		}
		return nil
	})
}

// performAutocommit runs the commit half of the debounce. Returns
// committed=true iff a commit landed. No-op (committed=false) when:
//   - a merge / rebase / cherry-pick is in progress
//   - there are no modified-tracked files and nothing staged
//
// Stages modified-tracked files only (`git diff --diff-filter=M`).
// Untracked / deleted-from-disk are deliberately left for explicit
// operator handling; a deduped hint goes to the log when they're
// present so the operator knows the daemon saw them.
func (a *AutoCommitter) performAutocommit() (bool, error) {
	if inProgress, marker := MergeOrRebaseInProgress(a.brain); inProgress {
		if a.verbose {
			log.Printf("autocommit %s: skipping (%s in progress)", a.brain, marker)
		}
		return false, nil
	}

	// Autosave is bound to the brain's sync branch (main). brainsync's whole
	// model targets main — push/pull/reset are all `origin main` (git.go) — so
	// on any other branch (a review/<slug> workbench branch, an sdlc feature
	// branch, a detached HEAD) an autosave commit is off-model: it would never
	// be pushed and would just pollute that branch, racing whatever tool owns
	// its commit cadence. Off main, the daemon stands down; the operator (or
	// that tool) owns commits there.
	branch, err := CurrentBranch(a.brain)
	if err != nil {
		// Branch unknowable (broken repo / git failure) → fail safe: surface
		// the error and do not commit, rather than silently committing blind.
		return false, fmt.Errorf("current branch: %w", err)
	}
	if branch != "main" {
		if a.verbose {
			log.Printf("autocommit %s: on branch %q (not main) — autosave stands down", a.brain, branch)
		}
		return false, nil
	}

	// 1. Stage modified-tracked files (additions to the working tree
	//    of files git already knows about). Excludes A/D/R/U/C — we
	//    only sweep up edits.
	mod, err := RunGit(a.brain, "diff", "--name-only", "--diff-filter=M", "--no-renames")
	if err != nil {
		return false, fmt.Errorf("diff: %w", err)
	}
	modified := strings.Fields(string(mod))
	if len(modified) > 0 {
		args := append([]string{"add", "--"}, modified...)
		if _, err := RunGit(a.brain, args...); err != nil {
			return false, fmt.Errorf("add: %w", err)
		}
	}

	// 2. Log hint for untracked / unstaged-deletions (deduped).
	a.maybeLogUntrackedHint()

	// 3. If nothing is staged at this point, no-op. (Operator may have
	//    `git rm`'d a file but not committed yet — that goes through
	//    their own commit, not autosave.)
	if _, err := RunGit(a.brain, "diff", "--cached", "--quiet"); err == nil {
		return false, nil
	}

	msg := fmt.Sprintf("autosave: %s [%d file(s)]",
		time.Now().UTC().Format(time.RFC3339), len(modified))
	if _, err := RunGit(a.brain, "commit", "-m", msg); err != nil {
		return false, fmt.Errorf("commit: %w", err)
	}
	return true, nil
}

// maybeLogUntrackedHint emits at most one log line per distinct
// "set of untracked+deleted paths" observed. If the set hasn't
// changed since the last log, stay silent — avoids the daemon log
// turning into an unread wall of identical hints on a long-lived
// untracked file like a draft the operator hasn't added yet.
func (a *AutoCommitter) maybeLogUntrackedHint() {
	out, err := RunGit(a.brain, "status", "--porcelain")
	if err != nil {
		return
	}
	var hints []string
	for _, line := range strings.Split(string(out), "\n") {
		if len(line) < 4 {
			continue
		}
		// Index status (col 0) and worktree status (col 1).
		xy := line[:2]
		path := line[3:]
		switch {
		case xy == "??":
			hints = append(hints, "untracked: "+path)
		case xy[1] == 'D':
			hints = append(hints, "deleted: "+path)
		}
	}
	if len(hints) == 0 {
		// Reset dedup so a future re-introduction logs again.
		a.muHint.Lock()
		a.lastUntrackedSig = ""
		a.muHint.Unlock()
		return
	}
	sort.Strings(hints)
	sig := strings.Join(hints, "\n")

	a.muHint.Lock()
	changed := sig != a.lastUntrackedSig
	a.lastUntrackedSig = sig
	a.muHint.Unlock()
	if !changed {
		return
	}
	log.Printf("autocommit %s: %d file(s) need explicit git add/rm (autosave skipped them): %s",
		a.brain, len(hints), strings.Join(hints, ", "))
}

// MergeOrRebaseInProgress checks for the marker files git creates
// while in the middle of an operation that has uncommitted state we
// shouldn't stomp on. Returns the name of the marker we found.
//
// Exported because both the autosave path (skip the commit) and the
// `nous push` CLI (refuse the flush) need to make the same call —
// keeping the predicate in one place avoids the two going out of
// sync on which markers count.
func MergeOrRebaseInProgress(repo string) (bool, string) {
	gitDir := filepath.Join(repo, ".git")
	for _, m := range []string{"MERGE_HEAD", "REBASE_HEAD", "CHERRY_PICK_HEAD", "rebase-apply", "rebase-merge"} {
		if _, err := os.Stat(filepath.Join(gitDir, m)); err == nil {
			return true, m
		}
	}
	return false, ""
}

// UntrackedAndDeleted returns the paths in repo that the autosave
// mechanism deliberately won't auto-stage: untracked additions and
// unstaged deletions of tracked files. Both classes require an
// explicit operator `git add` / `git rm` gesture.
//
// Used by `nous push` to print a hint, and shares its data source
// with the daemon's autocommit-log hint so the two surfaces report
// the same set of paths.
func UntrackedAndDeleted(repo string) (untracked, deleted []string, err error) {
	out, err := RunGit(repo, "status", "--porcelain")
	if err != nil {
		return nil, nil, err
	}
	for _, line := range strings.Split(string(out), "\n") {
		if len(line) < 4 {
			continue
		}
		xy := line[:2]
		path := line[3:]
		switch {
		case xy == "??":
			untracked = append(untracked, path)
		case xy[1] == 'D':
			deleted = append(deleted, path)
		}
	}
	return untracked, deleted, nil
}
