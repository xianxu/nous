# brain-sync daemon — implementation plan

> **For agentic workers:** Consult AGENTS.md Section 3 (Subagent Strategy) to determine the appropriate execution approach: use superpowers-subagent-driven-development (if subagents are suitable per AGENTS.md) or superpowers-executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build `brain-sync`, a single-binary Go daemon that watches shared-brain repos and propagates edits between peers via gcrypt'd github with file-level conflict resolution (loser → conflict file, never content-merge).

**Architecture:** Mirror charon's CLI shape — cobra-based subcommands. The bare `brain-sync` invocation runs the foreground watcher (charon-style verb-named-foreground, `serve`-equivalent). `brain-sync service install/uninstall/start/stop/status` installs that foreground process as a launchd agent. **Commit-driven sync, not edit-driven**: the watcher fsnotifies on `.git/refs/heads/main` of each shared brain, so local commits (the atomic unit) are the trigger to push — not per-file edits. A periodic timer fetches origin and fast-forwards if working tree is clean. Push-rejection triggers the file-level conflict resolve algorithm (no `git merge` content); cap retries at 5.

**Tech Stack:**
- Go 1.22 (already nous's version)
- `github.com/spf13/cobra` — CLI (charon uses it, nous already has it as transitive via brew installs but will need explicit add to go.mod)
- `github.com/fsnotify/fsnotify` — filesystem events. Scope: `.git/refs/heads/main` of each registered brain (commits as events), NOT the working tree.
- Standard library `os/exec` for `git` (don't pull in go-git; we want exact behavior of system git including gcrypt)
- `text/template` for launchd plist (charon pattern)
- Standard library `log` for output (charon convention) — to stderr; launchd captures via plist `StandardErrorPath`

**Spec source:**
- `nous/workshop/issues/000004-shared-brain-sync-daemon.md` (M2 plan items)
- `brain/atlas/sync-substrate-decision.md` (decision doc + daemon outline)

---

## File Structure

```
nous/
├── cmd/
│   └── brain-sync/
│       ├── main.go              # cobra root + subcommand wiring
│       ├── SKILL.md             # operator-facing usage doc
│       └── bin/                 # built binary (gitignored)
└── lib/
    └── brainsync/
        ├── discovery.go         # walk filesystem, find .brain/config.md, identify shared brains
        ├── discovery_test.go
        ├── refwatcher.go        # fsnotify on .git/refs/heads/main per brain (local-commit events)
        ├── refwatcher_test.go
        ├── git.go               # git ops: fetch / status / add / commit / push
        ├── git_test.go
        ├── resolve.go           # file-level conflict resolution algorithm
        ├── resolve_test.go
        ├── daemon.go            # main run loop tying it all together
        ├── daemon_test.go
        ├── service.go           # service.Manager interface
        └── service_darwin.go    # launchd plist + plist-management
```

**Why `lib/brainsync/` not `internal/brainsync/`:** nous already uses `lib/` (per `lib/gmail/`); follow established convention. `internal/` is charon's choice. Both are fine; consistency within nous is the value.

**Why split files this way:** each file has one responsibility — discovery, watching, git ops, resolution, daemon orchestration, service install. Tests next to the unit they cover. The daemon.go file ties them together with thin orchestration code; if it grows past ~250 lines, split further.

---

## Architectural Decisions

### Single binary with subcommands (charon pattern)

```
brain-sync                           # foreground watcher; Ctrl+C to stop
brain-sync service install           # write launchd plist; doesn't start
brain-sync service start             # launchctl load
brain-sync service stop              # launchctl unload
brain-sync service status            # is it running, last log lines
brain-sync service uninstall         # rm plist
```

`brain-sync` itself (no subcommand) is the foreground watcher — same shape as `charon serve` but charon's `Use` is verb-named while brain-sync's primary action is the bare binary, since "watching" is the only foreground mode. The cobra root command's `RunE` is wired to the watch handler.

**No `resolve` subcommand.** When a conflict file appears, the resolution is *semantic* — read both versions, decide what to keep, replace the canonical file. That's an agent task (the chat session has the context to make sensible merges), not a brain-sync responsibility. `nous#5` formalizes the agent-driven resolve flow.

**No `pre-write` / `post-write` subcommands.** Commits are the atomic sync unit, not file edits. The watcher reacts to local commits (via `.git/refs/heads/main`), not to per-file writes. Hooks at the editor / agent layer aren't needed.

### Brain discovery: explicit + auto-discoverable

Two modes:
- **Explicit** (v1, simpler to test): `brain-sync --brain ~/workspace/brain-shared-family --brain ~/workspace/another` — repeatable flag.
- **Auto-discovery** (v1.5, follow-on): default to walking `$HOME/workspace/`, finding `.brain/config.md`, auto-watching ones with `mode: shared`.

Plan ships explicit-flag for v1; auto-discovery is one extra task at the end.

### Conflict resolution algorithm

When the daemon's `git push` is rejected:

```
1. git fetch origin
2. compute_diff:
   - local_changes = files changed locally vs git's HEAD
   - remote_changes = files changed in origin/main vs git's HEAD
3. for f in local_changes ∩ remote_changes:
     # Conflict — file diverged on both sides
     if file_content(local, f) != file_content(remote, f):
         conflict_path = "{f}.conflict-{peer_id}-{utc_iso}.{ext}"
         mv local f → conflict_path
         git checkout origin/main -- f      # canonical = remote
4. for f in remote_changes \ local_changes:
     git checkout origin/main -- f          # take remote, no local change
5. git add -A
6. git commit -m "conflict-resolve: {comma-separated list of conflict files created}"
7. git push
8. on rejection: goto 1 (will normally succeed second time; cap at 5 retries)
```

Reference implementation in `lib/brainsync/resolve.go`. Extensively tested in `resolve_test.go` with synthetic conflicts (no real git ops needed for the algorithm tests — operate on in-memory data structures).

### Per-peer ID — derived from git config

Conflict-file names embed the peer that lost the race so the user can tell whose version is in the loser file (e.g., `paris.conflict-xianxu-mbp-20260507T221604Z.md` vs `paris.conflict-yifei-laptop-...md`). Without it, two-peer cases are fine but >2 recipients on a brain become ambiguous.

Source: derive from `git config user.name` (lowercased, hyphenated). Already set per-machine as part of the user's normal git setup; no separate brain-sync config file needed. If a user wants a different peer label per machine, they can `git config --local` it inside the brain repo.

### Logging

Standard library `log` (charon convention). All output to stderr. launchd's plist sets `StandardErrorPath` to capture it. Plain text format — `log.Printf("brainsync: ...")` style. No structured/JSON logging in v1; revisit if log volume needs filtering.

### Test strategy

Three layers:

1. **Unit tests** — pure functions in `lib/brainsync/` operate on in-memory data or temp dirs. Cover the conflict-resolution algorithm exhaustively (it's the trickiest part).
2. **Integration tests** — `daemon_test.go` spins up a real local git repo (via `t.TempDir()` + `git init --bare`), exercises end-to-end watcher → commit → push → fetch flows. Uses real `git` binary via `os/exec`.
3. **VM-based end-to-end** — the M3 synthetic conflict test runs in tart VM with two peers (host + VM) hitting a shared bare git repo over file://. Same shape as `nous-test-roundtrip.sh`.

`gcrypt` is intentionally not exercised in the daemon's unit/integration tests — gcrypt is a pure transport layer (git remote helper); if our daemon's `git push` works against `file:///tmp/test-bare.git`, it works against `gcrypt::ssh://...`. The two-layer model in the atlas doc keeps gcrypt orthogonal.

---

## Future enhancements (post-M2)

Captured here so the design surface is visible without bloating M2:

- **Menubar / tray UI for conflict notifications.** When `brain-sync` writes a conflict file, the user should learn about it without checking the directory. macOS Notification Center post + a tray icon showing N pending conflicts is the right shape. Same pattern as charon's tray. Tracked separately when M2 is shipping and friction surfaces.
- **Unified personal-assistant menubar app.** Two daemons on the user's Mac now occupy "needs-user-attention-outside-terminal" surface area: charon (credential lifecycle) and brain-sync (conflict notifications). Long-term these consolidate into one menubar app the user installs, that fans out to per-domain daemons internally. Not gating; cross-cutting issue when 2-3 daemons exist and the surface gets cluttered.

---

## Chunk 1: Project skeleton + brain discovery

**Files:**
- Create: `nous/cmd/brain-sync/main.go`
- Create: `nous/cmd/brain-sync/SKILL.md`
- Create: `nous/lib/brainsync/discovery.go`
- Create: `nous/lib/brainsync/discovery_test.go`
- Modify: `nous/go.mod` (add cobra dependency)
- Modify: `nous/go.sum` (auto)

### Step 1.1: Add cobra dependency

- [ ] Run: `cd ~/workspace/nous && go get github.com/spf13/cobra@latest`
- [ ] Expected: `go.mod` gets `github.com/spf13/cobra` line; `go.sum` populated.

### Step 1.2: Write the first test (discovery)

- [ ] Create `nous/lib/brainsync/discovery_test.go`:

```go
package brainsync

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFindSharedBrains(t *testing.T) {
	root := t.TempDir()

	// Two brains, one shared, one private, plus a non-brain dir.
	mustWriteBrain(t, filepath.Join(root, "shared-family"), "mode: shared\nname: family\nrecipients: [FP1, FP2]\n")
	mustWriteBrain(t, filepath.Join(root, "private-brain"), "mode: private\nname: personal\nrecipients: [FP1]\n")
	if err := os.MkdirAll(filepath.Join(root, "code-repo"), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := FindSharedBrains([]string{root})
	if err != nil {
		t.Fatalf("FindSharedBrains: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 shared brain, got %d: %v", len(got), got)
	}
	if filepath.Base(got[0]) != "shared-family" {
		t.Errorf("want shared-family, got %s", got[0])
	}
}

// mustWriteBrain creates dir/.brain/config.md with the given body. Helper for tests.
func mustWriteBrain(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".brain"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".brain", "config.md"), []byte("---\n"+body+"---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}
```

### Step 1.3: Run test — expect FAIL

- [ ] Run: `cd ~/workspace/nous && go test ./lib/brainsync/ -run TestFindSharedBrains -v`
- [ ] Expected: compile error (`FindSharedBrains` undefined).

### Step 1.4: Implement minimal discovery

- [ ] Create `nous/lib/brainsync/discovery.go`:

```go
// Package brainsync implements the shared-brain sync daemon.
//
// Identification: a directory is a "brain" iff it contains .brain/config.md.
// Mode is read from the YAML frontmatter; "shared" brains are the ones this
// daemon watches.
package brainsync

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// FindSharedBrains walks each root, returning the absolute path of every
// directory that contains a .brain/config.md with `mode: shared`.
//
// Walks one level deep — brains live as immediate children of $HOME/workspace
// or wherever the operator points it. A nested .brain/ inside a brain isn't a
// distinct brain; symlinks are followed once but cycles aren't (we read but
// don't recurse into a brain's interior).
func FindSharedBrains(roots []string) ([]string, error) {
	var found []string
	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", root, err)
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			p := filepath.Join(root, e.Name())
			cfg := filepath.Join(p, ".brain", "config.md")
			data, err := os.ReadFile(cfg)
			if err != nil {
				continue // not a brain, fine
			}
			if isSharedBrain(string(data)) {
				abs, err := filepath.Abs(p)
				if err != nil {
					return nil, err
				}
				found = append(found, abs)
			}
		}
	}
	return found, nil
}

// isSharedBrain returns true if the manifest body declares mode: shared.
// Tolerates the YAML frontmatter wrapper (--- ... ---).
func isSharedBrain(manifest string) bool {
	for _, line := range strings.Split(manifest, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "mode:") {
			val := strings.TrimSpace(strings.TrimPrefix(line, "mode:"))
			return val == "shared"
		}
	}
	return false
}
```

### Step 1.5: Run test — expect PASS

- [ ] Run: `cd ~/workspace/nous && go test ./lib/brainsync/ -run TestFindSharedBrains -v`
- [ ] Expected: `--- PASS: TestFindSharedBrains`.

### Step 1.6: Add edge-case tests

- [ ] Append to `discovery_test.go`:

```go
func TestFindSharedBrains_EmptyRoot(t *testing.T) {
	got, err := FindSharedBrains([]string{t.TempDir()})
	if err != nil {
		t.Fatalf("FindSharedBrains: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("want 0 brains in empty root, got %v", got)
	}
}

func TestFindSharedBrains_BadRoot(t *testing.T) {
	_, err := FindSharedBrains([]string{"/no/such/path"})
	if err == nil {
		t.Error("expected error for nonexistent root")
	}
}

func TestIsSharedBrain(t *testing.T) {
	tests := []struct{
		name, body string
		want bool
	}{
		{"shared", "---\nmode: shared\nname: family\n---\n", true},
		{"private", "---\nmode: private\nname: personal\n---\n", false},
		{"missing mode", "---\nname: thing\n---\n", false},
		{"shared with extra space", "---\nmode:   shared   \n---\n", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isSharedBrain(tc.body); got != tc.want {
				t.Errorf("isSharedBrain(%q) = %v, want %v", tc.body, got, tc.want)
			}
		})
	}
}
```

### Step 1.7: Run all discovery tests

- [ ] Run: `cd ~/workspace/nous && go test ./lib/brainsync/ -v`
- [ ] Expected: all pass.

### Step 1.8: Scaffold cmd/brain-sync/main.go

- [ ] Create `nous/cmd/brain-sync/main.go`:

```go
// brain-sync — git-based sync for shared brains.
//
// Watches shared-brain repos (those declaring `mode: shared` in
// .brain/config.md), pushes local commits and pulls remote ones with
// file-level conflict resolution. See workshop/plans/000004-shared-brain-sync-daemon-plan.md.
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func main() {
	root := &cobra.Command{
		Use:   "brain-sync",
		Short: "Git-based sync for shared brains",
		Long:  "Watches shared-brain repos and propagates commits via gcrypt'd github with file-level conflict resolution.",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Println("watcher not yet implemented — see chunk 5")
			return nil
		},
	}

	// service subcommand added in chunk 6.

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
```

### Step 1.9: Build + run

- [ ] Run: `cd ~/workspace/nous && go build -o cmd/brain-sync/bin/brain-sync ./cmd/brain-sync && ./cmd/brain-sync/bin/brain-sync --help`
- [ ] Expected: cobra usage output. Bare `brain-sync` runs the (stubbed) watcher; subcommands will show up after chunk 6.

### Step 1.10: Commit chunk 1

- [ ] Run:

```bash
cd ~/workspace/nous
git add cmd/brain-sync/main.go lib/brainsync/discovery.go lib/brainsync/discovery_test.go go.mod go.sum
git commit -m "#4 M2: brain-sync skeleton + brain discovery

cmd/brain-sync entrypoint with cobra root; bare invocation will run the
foreground watcher (stubbed for now, real impl in chunk 5).
lib/brainsync.FindSharedBrains walks root paths, reads .brain/config.md
manifests, returns paths whose mode is 'shared'.

Tests cover happy path, empty root, missing root, mode parsing edge cases."
```

---

## Chunk 2: Ref-watcher (commits as events)

**Files:**
- Create: `nous/lib/brainsync/refwatcher.go`
- Create: `nous/lib/brainsync/refwatcher_test.go`
- Modify: `nous/go.mod` (add fsnotify)

### Design

Each shared brain has a file at `<brain>/.git/refs/heads/main` whose contents are the SHA of the latest local commit on `main`. Git rewrites this file on commit, push, fetch, etc. We fsnotify that single file per brain and emit an event whenever it changes — once per local commit.

This is dramatically simpler than the working-tree-watching design we were considering before flipping the substrate decision: no debouncing, no markdown filter, no recursive directory tracking. The kernel-level event fires exactly when there's news to act on.

A subtlety: `fetch` also rewrites `.git/refs/heads/main`'s mtime in some configurations. That's fine — the watcher emits, the daemon checks `git rev-parse HEAD vs origin/main`, and if there's nothing local-only to push, does nothing. False-positive cost is one `git rev-parse` per fetch. Cheap.

### Step 2.1: Add fsnotify dependency

- [ ] Run: `cd ~/workspace/nous && go get github.com/fsnotify/fsnotify@latest`

### Step 2.2: Write the test first

- [ ] Create `nous/lib/brainsync/refwatcher_test.go`:

```go
package brainsync

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestRefWatcher_FiresOnLocalCommit(t *testing.T) {
	repo := initRepoForTest(t) // helper: git init + identity, returns repo path

	w, err := NewRefWatcher([]string{repo})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	// Make a commit; expect an event for `repo` on the channel.
	if err := os.WriteFile(filepath.Join(repo, "a.md"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, repo, "git", "add", "a.md")
	mustRun(t, repo, "git", "commit", "-q", "-m", "first")

	select {
	case got := <-w.Events():
		if got != repo {
			t.Errorf("got %s, want %s", got, repo)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no event after commit")
	}
}

// initRepoForTest is the same helper used in git_test.go (chunk 3).
// During chunk 2 we copy/import it here; in chunk 3 it gets unified.
func initRepoForTest(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.email", "test@nous.local"},
		{"config", "user.name", "Test"},
	} {
		mustRun(t, dir, "git", args...)
	}
	return dir
}

func mustRun(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	c := exec.Command(name, append([]string{"-C", dir}, args...)...)
	if name != "git" {
		c = exec.Command(name, args...)
		c.Dir = dir
	}
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("%s %v: %v: %s", name, args, err, out)
	}
}
```

### Step 2.3: Run — expect FAIL (NewRefWatcher undefined)

- [ ] Run: `go test ./lib/brainsync/ -run TestRefWatcher -v`

### Step 2.4: Implement RefWatcher

- [ ] Create `nous/lib/brainsync/refwatcher.go`:

```go
package brainsync

import (
	"path/filepath"

	"github.com/fsnotify/fsnotify"
)

// RefWatcher emits the brain path on every change to its
// .git/refs/heads/main file, which corresponds to a local commit (or a
// fetch updating the local ref). Consumers verify whether there's
// something local-only to push before acting.
//
// One RefWatcher serves multiple brains; events identify which brain
// changed by absolute path.
type RefWatcher struct {
	fs    *fsnotify.Watcher
	out   chan string
	stop  chan struct{}
	done  chan struct{}
	// pathToBrain maps the watched ref-file path back to its containing brain.
	pathToBrain map[string]string
}

// NewRefWatcher watches each brain's .git/refs/heads/main.
func NewRefWatcher(brains []string) (*RefWatcher, error) {
	fs, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	w := &RefWatcher{
		fs:          fs,
		out:         make(chan string, 16),
		stop:        make(chan struct{}),
		done:        make(chan struct{}),
		pathToBrain: make(map[string]string),
	}
	for _, b := range brains {
		ref := filepath.Join(b, ".git", "refs", "heads", "main")
		if err := fs.Add(ref); err != nil {
			fs.Close()
			return nil, err
		}
		w.pathToBrain[ref] = b
	}
	go w.loop()
	return w, nil
}

func (w *RefWatcher) loop() {
	defer close(w.done)
	for {
		select {
		case ev, ok := <-w.fs.Events:
			if !ok {
				return
			}
			// Any modification to the ref file = potential commit. Emit; the
			// daemon decides whether there's actually something to push.
			if ev.Op&(fsnotify.Write|fsnotify.Create) == 0 {
				continue
			}
			if brain, ok := w.pathToBrain[ev.Name]; ok {
				select {
				case w.out <- brain:
				case <-w.stop:
					return
				}
			}
		case <-w.stop:
			return
		}
	}
}

// Events returns a channel of brain paths whose ref changed.
func (w *RefWatcher) Events() <-chan string { return w.out }

func (w *RefWatcher) Close() {
	close(w.stop)
	<-w.done
	w.fs.Close()
	close(w.out)
}
```

### Step 2.5: Run — expect PASS

- [ ] Run: `go test ./lib/brainsync/ -run TestRefWatcher -v`
- [ ] Expected: PASS.

### Step 2.6: Commit chunk 2

```bash
git add lib/brainsync/refwatcher.go lib/brainsync/refwatcher_test.go go.mod go.sum
git commit -m "#4 M2: ref-watcher (commits as events)

fsnotify on each brain's .git/refs/heads/main. The kernel-level event
fires on local commit (and on fetch — the daemon dedup-checks whether
there's actually something to push). No debouncer needed; commits are
themselves discrete atomic events."
```

---

## Chunk 3: Git operations layer (no conflict resolution yet)

**Files:**
- Create: `nous/lib/brainsync/git.go`
- Create: `nous/lib/brainsync/git_test.go`

### Decisions

- Wrap `git` binary via `os/exec`. No go-git library — gcrypt is invoked by system git via its remote-helper interface; we want exact same behavior.
- `RunGit` helper: takes the repo path, args, returns stdout + error (with stderr captured into error). Sets a working dir explicitly; never relies on cwd.
- Operations needed for the daemon:
  - `Status(repo) ([]string, error)` — list of changed file paths (relative to repo root), or empty if clean.
  - `Fetch(repo) error` — `git fetch origin`.
  - `AddCommitPush(repo, msg) error` — `git add -A && git commit -m msg && git push`. Returns a typed error if push was rejected (so caller can resolve and retry).

### Step 3.1: Test that RunGit works

- [ ] Create `nous/lib/brainsync/git_test.go`:

```go
package brainsync

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// initRepo makes a fresh git repo and configures committer identity.
// Returns the repo path.
func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.email", "test@nous.local"},
		{"config", "user.name", "Test"},
	} {
		c := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	return dir
}

func TestRunGit_StatusOnEmpty(t *testing.T) {
	repo := initRepo(t)
	out, err := RunGit(repo, "status", "--porcelain")
	if err != nil {
		t.Fatalf("RunGit: %v", err)
	}
	if strings.TrimSpace(string(out)) != "" {
		t.Errorf("want empty status, got %q", out)
	}
}

func TestStatus_ListsModifiedFiles(t *testing.T) {
	repo := initRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "a.md"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := Status(repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "a.md" {
		t.Errorf("want [a.md], got %v", got)
	}
}
```

### Step 3.2: Implement git.go

- [ ] Create `nous/lib/brainsync/git.go`:

```go
package brainsync

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// RunGit runs `git -C repo args...` and returns stdout. Stderr is folded
// into the returned error on failure.
func RunGit(repo string, args ...string) ([]byte, error) {
	c := exec.Command("git", append([]string{"-C", repo}, args...)...)
	var stdout, stderr bytes.Buffer
	c.Stdout = &stdout
	c.Stderr = &stderr
	err := c.Run()
	if err != nil {
		return stdout.Bytes(), fmt.Errorf("git %v: %w: %s", args, err, stderr.String())
	}
	return stdout.Bytes(), nil
}

// Status returns the list of modified-or-untracked paths (relative to
// repo). Empty if working tree is clean.
func Status(repo string) ([]string, error) {
	out, err := RunGit(repo, "status", "--porcelain")
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, line := range strings.Split(string(out), "\n") {
		if len(line) < 4 {
			continue
		}
		// Format: "XY filename" — XY is two-char status, then space, then path.
		paths = append(paths, line[3:])
	}
	return paths, nil
}

// Fetch runs `git fetch origin`.
func Fetch(repo string) error {
	_, err := RunGit(repo, "fetch", "origin")
	return err
}

// ErrPushRejected is returned by AddCommitPush when git push fails because
// the remote rejected our push (typically: someone else pushed first).
// Caller should resolve and retry.
var ErrPushRejected = errors.New("push rejected by remote")

// AddCommitPush stages everything, commits with msg, pushes to origin.
// Returns ErrPushRejected if the push was rejected; nil on success;
// other error otherwise.
//
// If the working tree is clean (nothing to commit), returns nil without
// error — this is the "edited and reverted within debounce window" case.
func AddCommitPush(repo, msg string) error {
	if _, err := RunGit(repo, "add", "-A"); err != nil {
		return err
	}
	// Skip empty commits.
	if out, err := RunGit(repo, "diff", "--cached", "--quiet"); err == nil {
		_ = out
		return nil // nothing staged
	}
	if _, err := RunGit(repo, "commit", "-m", msg); err != nil {
		return err
	}
	if _, err := RunGit(repo, "push", "origin"); err != nil {
		// Detect rejection by stderr keywords.
		if strings.Contains(err.Error(), "rejected") || strings.Contains(err.Error(), "non-fast-forward") {
			return ErrPushRejected
		}
		return err
	}
	return nil
}
```

### Step 3.3: Run tests

- [ ] Run: `go test ./lib/brainsync/ -run TestRunGit -v && go test ./lib/brainsync/ -run TestStatus -v`
- [ ] Expected: pass.

### Step 3.4: Test AddCommitPush against a local bare remote

- [ ] Append to `git_test.go`:

```go
func initRepoWithBareRemote(t *testing.T) (workTree, bare string) {
	t.Helper()
	bare = t.TempDir()
	if out, err := exec.Command("git", "-C", bare, "init", "--bare", "-q", "-b", "main").CombinedOutput(); err != nil {
		t.Fatalf("init bare: %v: %s", err, out)
	}
	workTree = initRepo(t)
	if out, err := exec.Command("git", "-C", workTree, "remote", "add", "origin", bare).CombinedOutput(); err != nil {
		t.Fatalf("add remote: %v: %s", err, out)
	}
	return
}

func TestAddCommitPush_HappyPath(t *testing.T) {
	work, bare := initRepoWithBareRemote(t)
	if err := os.WriteFile(filepath.Join(work, "a.md"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := AddCommitPush(work, "test commit"); err != nil {
		t.Fatalf("AddCommitPush: %v", err)
	}
	// Verify the commit landed in the bare repo.
	out, err := exec.Command("git", "-C", bare, "log", "--oneline").CombinedOutput()
	if err != nil {
		t.Fatalf("verify log: %v", err)
	}
	if !strings.Contains(string(out), "test commit") {
		t.Errorf("commit didn't reach bare; log: %s", out)
	}
}

func TestAddCommitPush_NoChanges(t *testing.T) {
	work, _ := initRepoWithBareRemote(t)
	// No file changes — push should be no-op
	if err := AddCommitPush(work, "should be skipped"); err != nil {
		t.Errorf("expected nil, got %v", err)
	}
}
```

### Step 3.5: Run + commit chunk 3

- [ ] Run: `go test ./lib/brainsync/ -v`
- [ ] Commit:

```bash
git add lib/brainsync/git.go lib/brainsync/git_test.go
git commit -m "#4 M2: brainsync git ops layer

RunGit / Status / Fetch / AddCommitPush thin wrappers around system git
via os/exec. AddCommitPush returns ErrPushRejected on non-fast-forward
so callers can resolve and retry. No-change commits are silently skipped."
```

---

## Chunk 4: File-level conflict resolution algorithm

**Files:**
- Create: `nous/lib/brainsync/resolve.go`
- Create: `nous/lib/brainsync/resolve_test.go`

### The algorithm (recap from atlas)

```
on push rejection:
  git fetch origin
  local_diff   = files changed in HEAD that origin/main also touched
  for f in local_diff where local_content(f) != origin_content(f):
    # Conflict — rename our version, take theirs
    mv f f.conflict-<peer>-<utc>.<ext>
    git checkout origin/main -- f
  for f in remote-only (origin/main touched, we didn't):
    git checkout origin/main -- f       # bring it in
  git add -A; git commit -m "conflict-resolve: ..."; git push
  retry from top if rejected (cap 5 iterations)
```

### Step 4.1: Test conflict-name generation

- [ ] Create `nous/lib/brainsync/resolve_test.go`:

```go
package brainsync

import (
	"strings"
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
```

### Step 4.2: Implement conflictPath

- [ ] Create `nous/lib/brainsync/resolve.go`:

```go
package brainsync

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// conflictPath returns the path to use for the loser of a conflict.
// Format: <basename>.conflict-<peer>-<utc-iso8601-compact>.<ext>
//
// Example:
//   conflictPath("data/travel/paris.md", "xianxu-mbp", t) =
//       "data/travel/paris.conflict-xianxu-mbp-20260507T221604Z.md"
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
```

### Step 4.3: Run + verify

- [ ] Run: `go test ./lib/brainsync/ -run TestConflictPath -v`
- [ ] Expected: pass.

### Step 4.4: Test the full Resolve function with a real two-clone setup

This is integration-flavored — uses real git + bare remote + two clones to simulate two peers.

- [ ] Append to `resolve_test.go`:

```go
import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

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

func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	c := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}
func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestResolve_FileLevelConflict(t *testing.T) {
	bare, peerA, peerB := twoPeerRepo(t)

	// Both edit paris.md; A pushes first.
	must(t, os.WriteFile(filepath.Join(peerA, "paris.md"), []byte("A's plan\n"), 0o644))
	mustGit(t, peerA, "add", "paris.md")
	mustGit(t, peerA, "commit", "-q", "-m", "A: edit")
	mustGit(t, peerA, "push", "-q", "origin", "main")

	must(t, os.WriteFile(filepath.Join(peerB, "paris.md"), []byte("B's plan\n"), 0o644))
	mustGit(t, peerB, "add", "paris.md")
	mustGit(t, peerB, "commit", "-q", "-m", "B: edit")

	// B's push will fail; B should resolve.
	at := time.Date(2026, 5, 7, 22, 16, 4, 0, time.UTC)
	if err := Resolve(peerB, "peerB", at); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	mustGit(t, peerB, "push", "-q", "origin", "main")

	// Pull on A; verify both files exist with expected content.
	mustGit(t, peerA, "pull", "-q", "origin", "main")
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

	_ = bare
}
```

### Step 4.5: Implement Resolve

- [ ] Append to `resolve.go`:

```go
import (
	"os"
	"strings"
	"time"
)

// Resolve handles a push rejection by file-level resolution.
//
// Steps:
//   1. fetch origin
//   2. for each file changed locally that origin/main also changed:
//      if content differs: rename local to .conflict-<peer>-<ts>.<ext>
//                          and check out origin/main's version
//   3. for each file origin/main changed that we didn't: check it out
//   4. commit "conflict-resolve: ..." (if anything changed)
//   5. caller pushes; if still rejected, caller calls Resolve again
//
// Cap retries at the call site.
func Resolve(repo, peer string, now time.Time) error {
	if err := Fetch(repo); err != nil {
		return err
	}

	// Local commits since merge-base with origin/main.
	localChanged, err := changedFiles(repo, "HEAD..@{u}", false) // remote-only
	_ = localChanged
	if err != nil {
		// First fetch on a brand-new repo may not have @{u} set; tolerate.
		if !strings.Contains(err.Error(), "no upstream") {
			return err
		}
	}

	// Files touched by origin/main since our HEAD's merge-base.
	remoteChanged, err := changedFiles(repo, "HEAD..origin/main", false)
	if err != nil {
		return err
	}
	// Files we touched on top of merge-base.
	ourChanged, err := changedFiles(repo, "origin/main..HEAD", false)
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
			continue // not actually different — both made the same change
		}
		dst := filepath.Join(repo, conflictPath(f, peer, now))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(dst, ours, 0o644); err != nil {
			return err
		}
		// Take origin's version as canonical.
		if _, err := RunGit(repo, "checkout", "origin/main", "--", f); err != nil {
			return err
		}
	}

	// Pure remote-only changes: take them.
	for _, f := range remoteChanged {
		if containsString(conflicts, f) || containsString(ourChanged, f) {
			continue
		}
		if _, err := RunGit(repo, "checkout", "origin/main", "--", f); err != nil {
			return err
		}
	}

	// Reset HEAD to origin/main, then commit our conflict files on top.
	// Soft reset: keeps changes (now in working tree) but rebases HEAD onto origin/main.
	if _, err := RunGit(repo, "reset", "--soft", "origin/main"); err != nil {
		return err
	}
	// At this point: HEAD = origin/main, working tree = canonical-files + conflict-files we wrote.
	// add -A and commit if anything changed.
	if err := AddCommitPushNoCommit(repo); err != nil {
		return err
	}
	if isStagedClean(repo) {
		return nil
	}
	msg := buildConflictMsg(conflicts, peer)
	if _, err := RunGit(repo, "commit", "-m", msg); err != nil {
		return err
	}
	return nil
}

// AddCommitPushNoCommit just stages.
func AddCommitPushNoCommit(repo string) error {
	_, err := RunGit(repo, "add", "-A")
	return err
}

func isStagedClean(repo string) bool {
	_, err := RunGit(repo, "diff", "--cached", "--quiet")
	return err == nil
}

func buildConflictMsg(conflicts []string, peer string) string {
	if len(conflicts) == 0 {
		return "conflict-resolve: incorporate remote changes (no conflicts)"
	}
	return fmt.Sprintf("conflict-resolve: %d conflict file(s) by %s", len(conflicts), peer)
}

// changedFiles returns the list of files in the given diff range.
// _includeUntracked is unused for now; reserved for future.
func changedFiles(repo, refRange string, _includeUntracked bool) ([]string, error) {
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
```

### Step 4.6: Run + commit chunk 4

- [ ] Run: `go test ./lib/brainsync/ -run TestResolve -v`
- [ ] Expected: pass. Iterate on Resolve until the integration test is green; the algorithm above is the design intent but the test is the truth.
- [ ] Commit:

```bash
git add lib/brainsync/resolve.go lib/brainsync/resolve_test.go
git commit -m "#4 M2: file-level conflict resolution

Resolve handles a push rejection by file-level resolution: rename our
version of conflicted files to .conflict-<peer>-<ts>.<ext>, take
origin/main's version as canonical, soft-reset to origin/main, commit
our conflict files on top.

Tested via a two-peer integration scenario (bare + two clones) where
both peers edit the same file simultaneously; the second-pusher's
content lands as a .conflict-* file."
```

---

## Chunk 5: Watcher main loop (commit-driven)

**Files:**
- Create: `nous/lib/brainsync/watch.go`
- Create: `nous/lib/brainsync/watch_test.go`

### What the watcher does

Two event sources:
1. **Local commits** — RefWatcher (chunk 2) emits brain path on each `.git/refs/heads/main` change. Daemon checks `git rev-parse HEAD vs origin/main`; if local has unpushed commits, push (with resolve+retry on rejection).
2. **Periodic fetch** — every 30s, for each registered brain: `git fetch origin`; if working tree is clean and HEAD is strictly behind origin/main, `git pull --ff-only`. If working tree is dirty or there's a divergence, do nothing — the user will commit when ready and the push-side resolve will handle it.

Pseudocode:

```
on startup:
  brains = brains from --brain flags
  peer   = peerIDFor(brains[0])  // git config user.name slug
  rw     = NewRefWatcher(brains)
  ticker = NewTicker(30s)
  loop:
    select {
      case b := <-rw.Events():
        pushBrain(b, peer)
      case <-ticker.C:
        for b in brains: pullBrain(b)
      case <-ctx.Done(): return
    }

pushBrain(repo, peer):
  if !hasUnpushedCommits(repo): return
  for retry < 5:
    err := Push(repo)                // git push origin
    if err == nil: return
    if err != ErrPushRejected: log; return
    if err := Resolve(repo, peer, time.Now()); err != nil: log; return
    retry++
  log "exceeded retries"

pullBrain(repo):
  if err := Fetch(repo); err != nil: log; return
  if !cleanWorkingTree(repo): return        // skip; let user commit
  if behind, _ := isStrictlyBehind(repo); !behind: return
  if err := PullFF(repo); err != nil: log
```

### Step 5.1: Add Push, PullFF, hasUnpushedCommits to git.go

- [ ] Append to `lib/brainsync/git.go`:

```go
// Push runs `git push origin`. Returns ErrPushRejected on non-fast-forward.
func Push(repo string) error {
	if _, err := RunGit(repo, "push", "origin"); err != nil {
		if strings.Contains(err.Error(), "rejected") || strings.Contains(err.Error(), "non-fast-forward") {
			return ErrPushRejected
		}
		return err
	}
	return nil
}

// PullFF runs `git pull --ff-only origin main`.
func PullFF(repo string) error {
	_, err := RunGit(repo, "pull", "--ff-only", "origin", "main")
	return err
}

// HasUnpushedCommits returns true if HEAD is ahead of origin/main.
func HasUnpushedCommits(repo string) (bool, error) {
	out, err := RunGit(repo, "rev-list", "--count", "origin/main..HEAD")
	if err != nil {
		// First push (no upstream yet) — treat as having commits.
		if strings.Contains(err.Error(), "unknown revision") {
			return true, nil
		}
		return false, err
	}
	return strings.TrimSpace(string(out)) != "0", nil
}

// CleanWorkingTree returns true iff there are no uncommitted changes.
func CleanWorkingTree(repo string) (bool, error) {
	out, err := RunGit(repo, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(string(out)) == "", nil
}

// IsStrictlyBehind returns true iff origin/main is ahead of HEAD AND HEAD
// has nothing origin/main lacks (i.e., a fast-forward is possible).
func IsStrictlyBehind(repo string) (bool, error) {
	ahead, err := RunGit(repo, "rev-list", "--count", "origin/main..HEAD")
	if err != nil {
		return false, err
	}
	behind, err := RunGit(repo, "rev-list", "--count", "HEAD..origin/main")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(string(ahead)) == "0" && strings.TrimSpace(string(behind)) != "0", nil
}
```

### Step 5.2: Implement the watch loop

- [ ] Create `nous/lib/brainsync/watch.go`:

```go
package brainsync

import (
	"context"
	"errors"
	"log"
	"strings"
	"time"
)

// PeerIDFor derives a stable peer label from `git config user.name` in the
// given repo (lowercased, hyphenated). Falls back to "unknown-peer" if not
// set.
func PeerIDFor(repo string) string {
	out, err := RunGit(repo, "config", "user.name")
	if err != nil {
		return "unknown-peer"
	}
	name := strings.TrimSpace(string(out))
	name = strings.ReplaceAll(strings.ToLower(name), " ", "-")
	if name == "" {
		return "unknown-peer"
	}
	return name
}

// PushBrain pushes any unpushed commits, resolving + retrying on rejection.
// Caps retries at 5.
func PushBrain(repo, peer string, now func() time.Time) error {
	hasNew, err := HasUnpushedCommits(repo)
	if err != nil {
		return err
	}
	if !hasNew {
		return nil
	}
	for retry := 0; retry < 5; retry++ {
		err := Push(repo)
		if err == nil {
			return nil
		}
		if !errors.Is(err, ErrPushRejected) {
			return err
		}
		log.Printf("brainsync: push rejected for %s, resolving (retry %d)", repo, retry)
		if err := Resolve(repo, peer, now()); err != nil {
			return err
		}
	}
	return errors.New("exceeded retries")
}

// PullBrain fetches and fast-forwards if possible. Skips if working tree
// is dirty (lets user commit first; resolve happens on push).
func PullBrain(repo string) error {
	if err := Fetch(repo); err != nil {
		return err
	}
	clean, err := CleanWorkingTree(repo)
	if err != nil || !clean {
		return err
	}
	behind, err := IsStrictlyBehind(repo)
	if err != nil || !behind {
		return err
	}
	return PullFF(repo)
}

// Watch ties RefWatcher events + a periodic fetch ticker to push/pull.
// Blocks until ctx is cancelled.
func Watch(ctx context.Context, brains []string, fetchEvery time.Duration) error {
	if len(brains) == 0 {
		return errors.New("no brains to watch")
	}
	peer := PeerIDFor(brains[0])
	log.Printf("brainsync: watching %d brain(s) as peer %q", len(brains), peer)

	rw, err := NewRefWatcher(brains)
	if err != nil {
		return err
	}
	defer rw.Close()

	ticker := time.NewTicker(fetchEvery)
	defer ticker.Stop()

	for {
		select {
		case b := <-rw.Events():
			if err := PushBrain(b, peer, time.Now); err != nil {
				log.Printf("brainsync: push %s: %v", b, err)
			}
		case <-ticker.C:
			for _, b := range brains {
				if err := PullBrain(b); err != nil {
					log.Printf("brainsync: pull %s: %v", b, err)
				}
			}
		case <-ctx.Done():
			return nil
		}
	}
}
```

### Step 5.3: Wire watch into the cobra root command

- [ ] Modify `nous/cmd/brain-sync/main.go`:

```go
import (
	"context"
	"os/signal"
	"syscall"
	"time"

	"github.com/xianxu/nous/lib/brainsync"
)

var (
	brainPaths []string
	fetchEvery time.Duration
)

func main() {
	root := &cobra.Command{
		Use:   "brain-sync",
		Short: "Git-based sync for shared brains",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer cancel()
			if len(brainPaths) == 0 {
				return fmt.Errorf("--brain required (one or more)")
			}
			return brainsync.Watch(ctx, brainPaths, fetchEvery)
		},
	}
	root.Flags().StringSliceVar(&brainPaths, "brain", nil, "absolute path to a shared brain (repeatable)")
	root.Flags().DurationVar(&fetchEvery, "fetch-every", 30*time.Second, "periodic fetch interval")

	root.AddCommand(serviceCmd()) // chunk 6

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
```

### Step 5.4: Test PushBrain happy path + rejection retry

- [ ] Create `nous/lib/brainsync/watch_test.go` — uses the `twoPeerRepo` helper from chunk 4:
  - `TestPushBrain_NoUnpushed_NoOp`: clean repo, returns nil without error.
  - `TestPushBrain_HappyPath`: peer commits, PushBrain pushes successfully.
  - `TestPushBrain_RejectedThenResolves`: both peers commit different content to same file, second peer's PushBrain triggers Resolve, eventually pushes.

(Test code mirrors `TestResolve_FileLevelConflict` shape; ~80 lines.)

### Step 5.5: Build + smoke

- [ ] Run: `go build -o cmd/brain-sync/bin/brain-sync ./cmd/brain-sync && ./cmd/brain-sync/bin/brain-sync --brain /tmp/no-such-brain` — expect graceful error.

### Step 5.6: Commit chunk 5

```bash
git add lib/brainsync/watch.go lib/brainsync/watch_test.go lib/brainsync/git.go cmd/brain-sync/main.go
git commit -m "#4 M2: watch loop (commit-driven push + periodic ff-pull)

RefWatcher events → PushBrain (which resolves+retries on rejection).
Periodic ticker (default 30s) → fetch + ff-only-pull per brain (skipped
if working tree dirty; lets user commit before sync touches anything).

Bare 'brain-sync' invocation is now the foreground watcher (charon
serve-equivalent). Peer ID derived from git config user.name; no
separate config file."
```

---

## Chunk 6: service install/uninstall (launchd)

**Files:**
- Create: `nous/lib/brainsync/service.go`
- Create: `nous/lib/brainsync/service_darwin.go`
- Modify: `nous/cmd/brain-sync/main.go` (add `service` subcommand)

### Pattern — copy charon

Charon's `internal/service/` defines a `Manager` interface and `launchdManager` implementing it for Darwin. brain-sync mirrors this in `lib/brainsync/`.

### Step 6.1: Define the interface

- [ ] Create `nous/lib/brainsync/service.go`:

```go
package brainsync

import (
	"fmt"
	"runtime"
)

type ServiceManager interface {
	Install(binary string, args []string) error
	Uninstall() error
	Start() error
	Stop() error
	Status() (string, error)
}

func NewServiceManager() (ServiceManager, error) {
	switch runtime.GOOS {
	case "darwin":
		return &launchdServiceManager{}, nil
	default:
		return nil, fmt.Errorf("service mgmt not supported on %s yet", runtime.GOOS)
	}
}
```

### Step 6.2: Darwin implementation

- [ ] Create `nous/lib/brainsync/service_darwin.go`:

```go
//go:build darwin

package brainsync

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"
)

const (
	serviceLabel = "com.xianxu.brain-sync"
	plistTpl     = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>{{.Label}}</string>
    <key>ProgramArguments</key>
    <array>
        <string>{{.Binary}}</string>
{{- range .Args}}
        <string>{{.}}</string>
{{- end}}
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>StandardOutPath</key>
    <string>{{.LogPath}}</string>
    <key>StandardErrorPath</key>
    <string>{{.LogPath}}</string>
</dict>
</plist>
`
)

type launchdServiceManager struct{}

func plistPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", serviceLabel+".plist")
}
func logPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "Logs", "brain-sync.log")
}

func (m *launchdServiceManager) Install(binary string, args []string) error {
	tpl, err := template.New("plist").Parse(plistTpl)
	if err != nil {
		return err
	}
	f, err := os.Create(plistPath())
	if err != nil {
		return err
	}
	defer f.Close()
	return tpl.Execute(f, struct {
		Label, Binary, LogPath string
		Args                   []string
	}{serviceLabel, binary, logPath(), args})
}

func (m *launchdServiceManager) Uninstall() error {
	_ = m.Stop()
	return os.Remove(plistPath())
}
func (m *launchdServiceManager) Start() error {
	_, err := exec.Command("launchctl", "load", plistPath()).CombinedOutput()
	return err
}
func (m *launchdServiceManager) Stop() error {
	_, _ = exec.Command("launchctl", "unload", plistPath()).CombinedOutput()
	return nil
}
func (m *launchdServiceManager) Status() (string, error) {
	out, err := exec.Command("launchctl", "list", serviceLabel).CombinedOutput()
	if err != nil {
		if strings.Contains(string(out), "Could not find") {
			return "not running", nil
		}
		return "", err
	}
	return string(out), nil
}
```

### Step 6.3: Wire `service` subcommand into main.go

- [ ] Add to `cmd/brain-sync/main.go`:

```go
func serviceCmd() *cobra.Command {
	c := &cobra.Command{Use: "service", Short: "Manage brain-sync as a launchd service"}
	c.AddCommand(&cobra.Command{
		Use: "install", RunE: func(cmd *cobra.Command, _ []string) error {
			m, err := brainsync.NewServiceManager()
			if err != nil { return err }
			bin, err := os.Executable()
			if err != nil { return err }
			// Bare brain-sync is the foreground watcher; pass --brain flags only.
			var args []string
			for _, b := range brainPaths {
				args = append(args, "--brain", b)
			}
			return m.Install(bin, args)
		},
	})
	c.AddCommand(&cobra.Command{Use: "uninstall", RunE: func(cmd *cobra.Command, _ []string) error {
		m, _ := brainsync.NewServiceManager(); return m.Uninstall()
	}})
	c.AddCommand(&cobra.Command{Use: "start", RunE: func(cmd *cobra.Command, _ []string) error {
		m, _ := brainsync.NewServiceManager(); return m.Start()
	}})
	c.AddCommand(&cobra.Command{Use: "stop", RunE: func(cmd *cobra.Command, _ []string) error {
		m, _ := brainsync.NewServiceManager(); return m.Stop()
	}})
	c.AddCommand(&cobra.Command{Use: "status", RunE: func(cmd *cobra.Command, _ []string) error {
		m, _ := brainsync.NewServiceManager(); s, err := m.Status(); fmt.Println(s); return err
	}})
	c.PersistentFlags().StringSliceVar(&brainPaths, "brain", nil, "shared brain path (repeatable, used by 'install')")
	return c
}
```

- [ ] Add `root.AddCommand(serviceCmd())` in `main()`.

### Step 6.4: Smoke

- [ ] Run: `./cmd/brain-sync/bin/brain-sync service status` → "not running"
- [ ] (Don't actually install in this dev session — would auto-launch.)

### Step 6.5: Commit chunk 6

```bash
git add lib/brainsync/service.go lib/brainsync/service_darwin.go cmd/brain-sync/main.go
git commit -m "#4 M2: service install/uninstall/start/stop/status (launchd)

Mirrors charon's internal/service pattern. Single Manager interface,
launchd-specific implementation behind darwin build tag. plist gets
~/Library/LaunchAgents/com.xianxu.brain-sync.plist; logs to
~/Library/Logs/brain-sync.log."
```

---

## Chunk 7: Synthetic conflict test in tart VM

**Files:**
- Create: `nous/scripts/test-brain-sync.sh` (modeled on `nous-test-roundtrip.sh`)

### Plan

- Spin up a tart VM (`brain-sync-test`).
- Set up two peers: host + VM.
- Create a local bare repo on host (or use the github gcrypt'd brain-vm-test from earlier).
- Both peers clone, both run `brain-sync --brain <path>` (foreground watcher) against the brain dir.
- On peer A: edit a file, `git commit`; verify peer B sees the new file content within ~30s (after the periodic ff-pull).
- On peer A and B: edit the same file, both commit before either has fetched; verify both peers converge to canonical + conflict file.
- Tear down.

### Step 7.1: Write the test script

- [ ] Mirror `nous-test-roundtrip.sh` shape: pre-flight, clone snapshot, boot, ssh in, build + rsync brain-sync binary from host, run scenarios, tear down.

### Step 7.2: Add `make test-brain-sync` target

- [ ] Add to `Makefile.nous`:

```makefile
test-brain-sync:
	@$(NOUS_DIR)scripts/test-brain-sync.sh
```

### Step 7.3: Run + commit

```bash
make test-brain-sync
git add scripts/test-brain-sync.sh Makefile.nous
git commit -m "#4 M2: VM-based synthetic conflict test for brain-sync"
```

---

## Chunk 8: Auto-discovery

**Files:**
- Modify: `nous/lib/brainsync/discovery.go`
- Modify: `nous/cmd/brain-sync/main.go`

### Goal

When `--brain` is not specified, walk `$HOME/workspace/`, find all directories with `.brain/config.md` declaring `mode: shared`, watch those automatically. Operator's normal layout works without configuration.

### Step 8.1: Add `FindAllSharedBrainsInWorkspace()` to discovery.go

- [ ] Append to `lib/brainsync/discovery.go`:

```go
// FindAllSharedBrainsInWorkspace looks under $HOME/workspace/ (or
// $WORKSPACE_ROOT if set) for shared brains. Wrapper over FindSharedBrains
// for the auto-discovery default.
func FindAllSharedBrainsInWorkspace() ([]string, error) {
	root := os.Getenv("WORKSPACE_ROOT")
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		root = filepath.Join(home, "workspace")
	}
	return FindSharedBrains([]string{root})
}
```

### Step 8.2: Wire fallback in main.go

- [ ] In the root command's `RunE`:

```go
if len(brainPaths) == 0 {
	auto, err := brainsync.FindAllSharedBrainsInWorkspace()
	if err != nil { return err }
	if len(auto) == 0 {
		return fmt.Errorf("no shared brains found under $HOME/workspace; pass --brain explicitly")
	}
	brainPaths = auto
	log.Printf("brainsync: auto-discovered %d shared brain(s)", len(auto))
}
```

### Step 8.3: Tests

- [ ] Add a test for `FindAllSharedBrainsInWorkspace` using `WORKSPACE_ROOT` env override + temp dir setup.

### Step 8.4: Commit

```bash
git add lib/brainsync/discovery.go cmd/brain-sync/main.go
git commit -m "#4 M2: auto-discover shared brains under \$HOME/workspace

Bare 'brain-sync' (no --brain flags) walks workspace dir and watches
every .brain/config.md with mode: shared. Honors WORKSPACE_ROOT env
for testability and non-default layouts."
```

---

## Verification before marking M2 done

- [ ] `go test ./lib/brainsync/...` — all pass.
- [ ] `go build -o cmd/brain-sync/bin/brain-sync ./cmd/brain-sync` — builds clean.
- [ ] `./cmd/brain-sync/bin/brain-sync --help` — root command shows `--brain` flag and `service` subcommand.
- [ ] `make test-brain-sync` — VM end-to-end conflict test passes.
- [ ] Manual smoke: run `brain-sync` foreground against a real shared brain on host, commit a file, verify the commit lands on github (gcrypt-encrypted).
- [ ] Update `nous#4` issue: tick M2 plan items, log close, attach actual_hours.
- [ ] Update `brain/data/project/shared-brain.md`: tick M2 task, optional detail block.
- [ ] Atlas update: extend `brain/atlas/sync-substrate-decision.md` "Daemon outline" section if implementation diverged from outline (e.g., commit-driven trigger replaced the originally-sketched fsnotify-on-working-tree).
