# brain-sync daemon — implementation plan

> **For agentic workers:** Consult AGENTS.md Section 3 (Subagent Strategy) to determine the appropriate execution approach: use superpowers-subagent-driven-development (if subagents are suitable per AGENTS.md) or superpowers-executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build `brain-sync`, a single-binary Go daemon that watches shared-brain repos and propagates edits between peers via gcrypt'd github with file-level conflict resolution (loser → conflict file, never content-merge).

**Architecture:** Mirror charon's CLI shape — cobra-based subcommands, 🤖<`daemon`>[the opposite is true, daemon means background. you should follow charon convention, brain-sync itself is foreground, but when you run with `brain-sync service install`, it installs as service] for foreground run, `service install/uninstall/start/stop/status` for launchd integration, 🤖<`pre-write`/`post-write`>[this is too aggressive. we should wait till new commits are made. a commit is an atomic unit] for Claude Code PreToolUse/PostToolUse hooks. 🤖<fsnotify>[too aggressive, see comment on atomic unit] for file events; debounce; resolve conflicts at file level via custom algorithm (no `git merge`); `git fetch` + `git commit` + `git push` with retry-on-rejection.

**Tech Stack:**
- Go 1.22 (already nous's version)
- `github.com/spf13/cobra` — CLI (charon uses it, nous already has it as transitive via brew installs but will need explicit add to go.mod)
- `github.com/fsnotify/fsnotify` — filesystem events
- Standard library `os/exec` for `git` (don't pull in go-git; we want exact behavior of system git including gcrypt)
- `text/template` for launchd plist (charon pattern)
- No third-party logging — `log/slog` standard library

🤖[generally speaking, just follow charon pattern in tool ergonomic. this includes single binary, etc. when unsure, ask. ]

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
        ├── watcher.go           # fsnotify wrapper + debouncing
        ├── watcher_test.go
        ├── git.go               # git ops: fetch / status / add / commit / push
        ├── git_test.go
        ├── resolve.go           # file-level conflict resolution algorithm
        ├── resolve_test.go
        ├── daemon.go            # main run loop tying it all together
        ├── daemon_test.go
        ├── service.go           # service.Manager interface
        └── service_darwin.go    # launchd plist + plist-management
```

**Why `lib/brainsync/` not `internal/brainsync/`:** nous already uses `lib/` (per `lib/gmail/`); follow established convention. `internal/` is charon's choice. Both are fine; consistency within nous is the value. 🤖[agree] 

**Why split files this way:** each file has one responsibility — discovery, watching, git ops, resolution, daemon orchestration, service install. Tests next to the unit they cover. The daemon.go file ties them together with thin orchestration code; if it grows past ~250 lines, split further.

---

## Architectural Decisions

### Single binary with subcommands (charon pattern)

```
brain-sync                           # foreground watcher; Ctrl+C to stop
brain-sync service install           # write launchd plist; doesn't start
brain-sync service status            # is it running, last log lines
brain-sync service uninstall         # rm plist
brain-sync resolve <path>            # (deferred to future) interactive resolve helper
```

🤖[the `brain-sync resolve` like would be done inside an agent, not driven by brain-sync and essentially subagent, because the agent, while user's chatting, have more context.]

### Brain discovery: explicit + auto-discoverable

Two modes:
- **Explicit** (v1, simpler to test): `brain-sync daemon --brain ~/workspace/brain-shared-family --brain ~/workspace/another` — repeatable flag.
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

🤖[brain-sync might need to be a menu tray application so that when conflict happen, we can send a notification to the user. same charon pattern. ]

🤖[separately, now we have two programs in the "personal assistant domain" that need user attention in certain cases and user interaction outside terminal. we may need to create later an uber app so user only need to isntall one visible menubar app]

Reference implementation in `lib/brainsync/resolve.go`. Extensively tested in `resolve_test.go` with synthetic conflicts (no real git ops needed for the algorithm tests — operate on in-memory data structures).

### Per-peer ID

Stored at `~/.config/brain-sync/peer-id` (or `$XDG_CONFIG_HOME/brain-sync/peer-id`). Generated on first daemon run if missing — short slug like `xianxu-mbp-2026` (hostname-based default; user can edit). Used in conflict-file naming so it's human-readable.

🤖[what's this for?]

### Logging

`log/slog` (stdlib). Default JSON to stderr (so launchd captures it cleanly). Verbose flag for human-readable text format. Logs include the brain path, file changed, and operation.

🤖[follow charon convention to log to stderr I think]

### Test strategy

Three layers:

1. **Unit tests** — pure functions in `lib/brainsync/` operate on in-memory data or temp dirs. Cover the conflict-resolution algorithm exhaustively (it's the trickiest part).
2. **Integration tests** — `daemon_test.go` spins up a real local git repo (via `t.TempDir()` + `git init --bare`), exercises end-to-end watcher → commit → push → fetch flows. Uses real `git` binary via `os/exec`.
3. **VM-based end-to-end** — the M3 synthetic conflict test runs in tart VM with two peers (host + VM) hitting a shared bare git repo over file://. Same shape as `nous-test-roundtrip.sh`.

`gcrypt` is intentionally not exercised in the daemon's unit/integration tests — gcrypt is a pure transport layer (git remote helper); if our daemon's `git push` works against `file:///tmp/test-bare.git`, it works against `gcrypt::ssh://...`. The two-layer model in the atlas doc keeps gcrypt orthogonal.

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
// brain-sync — git-based sync daemon for shared brains.
//
// Watches shared-brain repos (those declaring `mode: shared` in
// .brain/config.md), commits and pushes edits with file-level conflict
// resolution. See workshop/plans/000004-shared-brain-sync-daemon-plan.md.
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func main() {
	root := &cobra.Command{
		Use:   "brain-sync",
		Short: "Git-based sync daemon for shared brains",
		Long:  "Watches shared-brain repos and propagates edits via gcrypt'd github with file-level conflict resolution.",
	}

	root.AddCommand(daemonCmd())
	// service, pre-write, post-write commands added in later chunks.

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// daemonCmd is a stub; real implementation in chunk 6.
func daemonCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "daemon",
		Short: "Run the watcher daemon (foreground)",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println("daemon not yet implemented — see chunk 6")
		},
	}
}
```

### Step 1.9: Build + run

- [ ] Run: `cd ~/workspace/nous && go build -o cmd/brain-sync/bin/brain-sync ./cmd/brain-sync && ./cmd/brain-sync/bin/brain-sync --help`
- [ ] Expected: cobra usage output naming `daemon` subcommand.

### Step 1.10: Commit chunk 1

- [ ] Run:

```bash
cd ~/workspace/nous
git add cmd/brain-sync/main.go lib/brainsync/discovery.go lib/brainsync/discovery_test.go go.mod go.sum
git commit -m "#4 M2: brain-sync skeleton + brain discovery

cmd/brain-sync entrypoint with cobra root and stubbed 'daemon' subcommand.
lib/brainsync.FindSharedBrains walks root paths, reads .brain/config.md
manifests, returns paths whose mode is 'shared'.

Tests cover happy path, empty root, missing root, mode parsing edge cases."
```

---

## Chunk 2: Filesystem watcher + debouncing

**Files:**
- Create: `nous/lib/brainsync/watcher.go`
- Create: `nous/lib/brainsync/watcher_test.go`
- Modify: `nous/go.mod` (add fsnotify)

### Step 2.1: Add fsnotify dependency

- [ ] Run: `cd ~/workspace/nous && go get github.com/fsnotify/fsnotify@latest`

### Step 2.2: Write tests for the debouncer first (no real fs)

- [ ] Create `nous/lib/brainsync/watcher_test.go`:

```go
package brainsync

import (
	"testing"
	"time"
)

func TestDebouncer_BatchesNearbyEvents(t *testing.T) {
	d := newDebouncer(50 * time.Millisecond)
	defer d.Close()

	out := d.C()
	for i := 0; i < 5; i++ {
		d.Add("/a/file.md")
		time.Sleep(10 * time.Millisecond)
	}

	select {
	case batch := <-out:
		if len(batch) != 1 {
			t.Errorf("want 1 unique file in batch, got %d", len(batch))
		}
		if batch[0] != "/a/file.md" {
			t.Errorf("want /a/file.md, got %s", batch[0])
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("debouncer did not emit within timeout")
	}
}

func TestDebouncer_Deduplicates(t *testing.T) {
	d := newDebouncer(30 * time.Millisecond)
	defer d.Close()

	d.Add("/a/foo.md")
	d.Add("/a/bar.md")
	d.Add("/a/foo.md") // duplicate — should not appear twice

	select {
	case batch := <-d.C():
		if len(batch) != 2 {
			t.Errorf("want 2 unique files, got %d: %v", len(batch), batch)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timeout")
	}
}
```

### Step 2.3: Run — expect FAIL (debouncer undefined)

- [ ] Run: `go test ./lib/brainsync/ -run TestDebouncer -v`

### Step 2.4: Implement debouncer

- [ ] Create `nous/lib/brainsync/watcher.go`:

```go
package brainsync

import (
	"sync"
	"time"
)

// debouncer collects file paths and emits them as a deduplicated batch
// once `quiet` time has passed since the last Add.
type debouncer struct {
	quiet time.Duration

	mu       sync.Mutex
	pending  map[string]struct{}
	timer    *time.Timer
	out      chan []string
	closed   bool
}

func newDebouncer(quiet time.Duration) *debouncer {
	return &debouncer{
		quiet:   quiet,
		pending: make(map[string]struct{}),
		out:     make(chan []string, 8),
	}
}

func (d *debouncer) C() <-chan []string { return d.out }

func (d *debouncer) Add(path string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return
	}
	d.pending[path] = struct{}{}
	if d.timer != nil {
		d.timer.Stop()
	}
	d.timer = time.AfterFunc(d.quiet, d.flush)
}

func (d *debouncer) flush() {
	d.mu.Lock()
	if d.closed || len(d.pending) == 0 {
		d.mu.Unlock()
		return
	}
	batch := make([]string, 0, len(d.pending))
	for p := range d.pending {
		batch = append(batch, p)
	}
	d.pending = make(map[string]struct{})
	d.mu.Unlock()

	d.out <- batch
}

func (d *debouncer) Close() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return
	}
	d.closed = true
	if d.timer != nil {
		d.timer.Stop()
	}
	close(d.out)
}
```

### Step 2.5: Run — expect PASS

- [ ] Run: `go test ./lib/brainsync/ -run TestDebouncer -v`

### Step 2.6: Add fsnotify-backed Watcher with `.git/` ignore

- [ ] Append to `watcher.go`:

```go
import "github.com/fsnotify/fsnotify"

// Watcher wires fsnotify events into a debouncer, filtering out paths we
// don't care about (.git/, hidden dotfiles, non-markdown).
type Watcher struct {
	fs        *fsnotify.Watcher
	deb       *debouncer
	roots     []string
	stop      chan struct{}
	stopped   chan struct{}
}

// NewWatcher creates a watcher that monitors each root recursively
// (excluding .git/) and emits debounced batches of changed paths.
func NewWatcher(roots []string, quiet time.Duration) (*Watcher, error) {
	fs, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	w := &Watcher{
		fs:      fs,
		deb:     newDebouncer(quiet),
		roots:   roots,
		stop:    make(chan struct{}),
		stopped: make(chan struct{}),
	}
	for _, r := range roots {
		if err := w.addTree(r); err != nil {
			fs.Close()
			return nil, err
		}
	}
	go w.loop()
	return w, nil
}

// addTree adds root and all its non-.git/ subdirectories to fsnotify.
// fsnotify needs explicit dir watches — recursive watches aren't a thing
// on Linux/macOS at the kernel level.
func (w *Watcher) addTree(root string) error {
	return filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return nil
		}
		if filepath.Base(p) == ".git" {
			return filepath.SkipDir
		}
		return w.fs.Add(p)
	})
}

func (w *Watcher) loop() {
	defer close(w.stopped)
	for {
		select {
		case ev, ok := <-w.fs.Events:
			if !ok {
				return
			}
			if !shouldTrack(ev.Name, ev.Op) {
				continue
			}
			w.deb.Add(ev.Name)
			// Newly created dir? watch it too. (One level deep — re-walk on dir creates.)
			if ev.Op&fsnotify.Create != 0 {
				if info, err := os.Stat(ev.Name); err == nil && info.IsDir() && filepath.Base(ev.Name) != ".git" {
					_ = w.fs.Add(ev.Name)
				}
			}
		case <-w.stop:
			return
		}
	}
}

func (w *Watcher) Events() <-chan []string { return w.deb.C() }

func (w *Watcher) Close() {
	close(w.stop)
	<-w.stopped
	w.fs.Close()
	w.deb.Close()
}

// shouldTrack returns true if the event is one we care about.
// Filters: ignore .git/, hidden files (.X), non-markdown extensions.
// Markdown-only is a brain-content-scope decision (atlas doc).
func shouldTrack(path string, op fsnotify.Op) bool {
	base := filepath.Base(path)
	if strings.HasPrefix(base, ".") {
		return false
	}
	if strings.Contains(path, "/.git/") {
		return false
	}
	if !strings.HasSuffix(base, ".md") {
		return false
	}
	// Track writes/creates/removes/renames; ignore Chmod-only events.
	return op&(fsnotify.Write|fsnotify.Create|fsnotify.Remove|fsnotify.Rename) != 0
}
```

### Step 2.7: Add test for shouldTrack

- [ ] Append to `watcher_test.go`:

```go
import "github.com/fsnotify/fsnotify"

func TestShouldTrack(t *testing.T) {
	tests := []struct{
		name string
		path string
		op   fsnotify.Op
		want bool
	}{
		{"markdown write",  "/brain/notes.md",         fsnotify.Write,  true},
		{"markdown create", "/brain/notes.md",         fsnotify.Create, true},
		{"git internal",    "/brain/.git/index",       fsnotify.Write,  false},
		{"hidden dotfile",  "/brain/.DS_Store",        fsnotify.Write,  false},
		{"non-markdown",    "/brain/photo.jpg",        fsnotify.Write,  false},
		{"chmod only",      "/brain/notes.md",         fsnotify.Chmod,  false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldTrack(tc.path, tc.op); got != tc.want {
				t.Errorf("shouldTrack(%s, %v) = %v, want %v", tc.path, tc.op, got, tc.want)
			}
		})
	}
}
```

### Step 2.8: Test the full Watcher with a real temp dir

- [ ] Append:

```go
func TestWatcher_DetectsMarkdownWrite(t *testing.T) {
	root := t.TempDir()
	w, err := NewWatcher([]string{root}, 30*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	if err := os.WriteFile(filepath.Join(root, "hello.md"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}

	select {
	case batch := <-w.Events():
		if len(batch) != 1 || filepath.Base(batch[0]) != "hello.md" {
			t.Errorf("unexpected batch: %v", batch)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("watcher didn't emit event")
	}
}
```

### Step 2.9: Run all watcher tests

- [ ] Run: `go test ./lib/brainsync/ -v`
- [ ] Expected: all pass.

### Step 2.10: Commit chunk 2

- [ ] Run:

```bash
git add lib/brainsync/watcher.go lib/brainsync/watcher_test.go go.mod go.sum
git commit -m "#4 M2: brainsync watcher + debouncer

debouncer collects path events and emits a deduplicated batch after
'quiet' time has elapsed since last Add. Watcher wraps fsnotify, walks
the brain directory tree (excluding .git/), filters to markdown-only
content per brain content scope decision."
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

## Chunk 5: Daemon main loop

**Files:**
- Create: `nous/lib/brainsync/daemon.go`
- Create: `nous/lib/brainsync/daemon_test.go`

### What the daemon does

Pseudocode:

```
on startup:
  brains = explicit brains from --brain flags (or auto-discover)
  peer  = read peer-id from ~/.config/brain-sync/peer-id (or generate)
  for each brain b:
    watcher_b = NewWatcher([b], 2s)
  loop:
    select {
      case batch := <-watcher_b.Events():
        sync(b, batch, peer)
      case <-ctx.Done(): return
    }

sync(repo, paths, peer):
  retry := 0
  for retry < 5:
    err := AddCommitPush(repo, msg(paths))
    if err == nil: return nil
    if err != ErrPushRejected: log and return
    err := Resolve(repo, peer, time.Now())
    if err != nil: log and return
    retry++
  log "exceeded retries"
```

### Step 5.1: Test the sync helper

- [ ] Create `nous/lib/brainsync/daemon_test.go` covering:
  - sync call with no rejection: commits and pushes
  - sync with rejection on first push, succeeds after Resolve

(Detailed test code follows the same pattern as `TestResolve_FileLevelConflict`; ~80 lines.)

### Step 5.2: Implement daemon

- [ ] Create `nous/lib/brainsync/daemon.go`:

```go
package brainsync

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// Sync does add+commit+push, with conflict resolution + retry on rejection.
// Caps retries to avoid pathological loops.
func Sync(repo, peer string, paths []string, now func() time.Time) error {
	msg := buildEditMsg(paths, peer)
	for retry := 0; retry < 5; retry++ {
		err := AddCommitPush(repo, msg)
		if err == nil {
			return nil
		}
		if !errors.Is(err, ErrPushRejected) {
			return err
		}
		slog.Info("push rejected, resolving", "repo", repo, "retry", retry)
		if err := Resolve(repo, peer, now()); err != nil {
			return fmt.Errorf("resolve: %w", err)
		}
	}
	return fmt.Errorf("exceeded retries for %s", repo)
}

func buildEditMsg(paths []string, peer string) string {
	if len(paths) == 1 {
		return fmt.Sprintf("edit: %s", paths[0])
	}
	return fmt.Sprintf("edit: %d files (%s)", len(paths), strings.Join(paths, ", "))
}

// Daemon ties watcher + sync. Run blocks until ctx cancelled.
func Daemon(ctx context.Context, brains []string, peer string) error {
	type brainState struct {
		path    string
		watcher *Watcher
	}
	var states []brainState
	for _, b := range brains {
		w, err := NewWatcher([]string{b}, 2*time.Second)
		if err != nil {
			return err
		}
		defer w.Close()
		states = append(states, brainState{b, w})
	}
	slog.Info("brain-sync started", "brains", brains, "peer", peer)

	// Multiplex: one goroutine per brain emits to a shared sync channel,
	// or use reflect.Select. Keep it simple — one goroutine per brain.
	type event struct{ repo string; paths []string }
	syncCh := make(chan event, 16)
	for _, st := range states {
		st := st
		go func() {
			for batch := range st.watcher.Events() {
				syncCh <- event{st.path, batch}
			}
		}()
	}

	for {
		select {
		case ev := <-syncCh:
			if err := Sync(ev.repo, peer, ev.paths, time.Now); err != nil {
				slog.Error("sync failed", "repo", ev.repo, "err", err)
			}
		case <-ctx.Done():
			return nil
		}
	}
}
```

### Step 5.3: Wire daemon into cmd

- [ ] Modify `nous/cmd/brain-sync/main.go` to replace the stub `daemonCmd()` with a real implementation:

```go
import (
	"context"
	"os/signal"
	"syscall"

	"github.com/xianxu/nous/lib/brainsync"
)

var (
	brainPaths []string
	peerID     string
)

func daemonCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "Run the watcher daemon (foreground)",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer cancel()

			peer, err := loadOrCreatePeerID()
			if err != nil {
				return err
			}
			if peerID != "" {
				peer = peerID // explicit override
			}
			if len(brainPaths) == 0 {
				return fmt.Errorf("--brain required (one or more)")
			}
			return brainsync.Daemon(ctx, brainPaths, peer)
		},
	}
	cmd.Flags().StringSliceVar(&brainPaths, "brain", nil, "absolute path to a shared brain (repeatable)")
	cmd.Flags().StringVar(&peerID, "peer-id", "", "override peer ID for this run")
	return cmd
}

func loadOrCreatePeerID() (string, error) {
	dir := filepath.Join(os.Getenv("HOME"), ".config", "brain-sync")
	p := filepath.Join(dir, "peer-id")
	if data, err := os.ReadFile(p); err == nil {
		return strings.TrimSpace(string(data)), nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	host, _ := os.Hostname()
	id := strings.ReplaceAll(host, " ", "-")
	if err := os.WriteFile(p, []byte(id+"\n"), 0o644); err != nil {
		return "", err
	}
	return id, nil
}
```

### Step 5.4: Build + smoke

- [ ] Run: `go build -o cmd/brain-sync/bin/brain-sync ./cmd/brain-sync && ./cmd/brain-sync/bin/brain-sync daemon --brain /tmp/no-such-brain` — expect graceful error.

### Step 5.5: Commit chunk 5

- [ ] Commit:

```bash
git add lib/brainsync/daemon.go lib/brainsync/daemon_test.go cmd/brain-sync/main.go
git commit -m "#4 M2: daemon main loop + brain-sync daemon CLI

Daemon ties Watcher events to Sync (commit + push, resolve + retry on
rejection). One goroutine per brain feeds a shared event channel.
Peer ID generated from hostname on first run, persisted to
~/.config/brain-sync/peer-id."
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
			args := []string{"daemon"}
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

## Chunk 7: pre-write / post-write hook subcommands

**Files:**
- Modify: `nous/cmd/brain-sync/main.go`

### What they do

`pre-write <path>`:
- Find the brain containing `<path>` (walk up to `.brain/config.md`).
- If not a shared brain: exit 0 silently.
- Else: `git fetch`; if remote has new content for any of our locally-modified files, run Resolve to surface conflict files BEFORE the agent writes. (This shrinks the agent's stale-state window.)

`post-write <path>`:
- Find the brain.
- If shared: trigger a sync immediately (don't wait for fs-watcher debounce). This is essentially calling `Sync(repo, peer, [path], time.Now)`.

These are short-lived CLI invocations; they're fine to spawn the daemon's git work synchronously.

### Step 7.1: Add to main.go

- [ ] Add subcommands:

```go
func preWriteCmd() *cobra.Command {
	return &cobra.Command{
		Use: "pre-write <path>", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			repo, ok := findContainingBrain(args[0])
			if !ok || !brainsync.IsSharedBrainPath(repo) { return nil }
			if err := brainsync.Fetch(repo); err != nil { return err }
			peer, _ := loadOrCreatePeerID()
			return brainsync.PreWriteResolve(repo, peer, time.Now())
		},
	}
}

func postWriteCmd() *cobra.Command {
	return &cobra.Command{
		Use: "post-write <path>", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			repo, ok := findContainingBrain(args[0])
			if !ok || !brainsync.IsSharedBrainPath(repo) { return nil }
			peer, _ := loadOrCreatePeerID()
			return brainsync.Sync(repo, peer, []string{args[0]}, time.Now)
		},
	}
}
```

(Helper functions `findContainingBrain`, `IsSharedBrainPath`, `PreWriteResolve` are added to `lib/brainsync/`.)

### Step 7.2: Commit chunk 7

```bash
git add cmd/brain-sync/main.go lib/brainsync/...
git commit -m "#4 M2: pre-write / post-write hook subcommands"
```

---

## Chunk 8: Synthetic conflict test in tart VM

**Files:**
- Create: `nous/scripts/test-brain-sync.sh` (modeled on `nous-test-roundtrip.sh`)

### Plan

- Spin up a tart VM (`brain-sync-test`).
- Set up two peers: host + VM.
- Create a local bare repo on host (or use github gcrypt'd brain-vm-test from earlier).
- Both peers clone, both run `brain-sync daemon` against the brain dir.
- Trigger a simultaneous edit on both, wait, verify the canonical + conflict file appears on both peers.

### Step 8.1: Write the test script

- [ ] Mirror `nous-test-roundtrip.sh` shape: pre-flight, clone snapshot, boot, ssh in, install brain-sync (rsync from host), run scenarios, tear down.

### Step 8.2: Add `make test-brain-sync` target

- [ ] Add to `Makefile.nous`:

```makefile
test-brain-sync:
	@$(NOUS_DIR)scripts/test-brain-sync.sh
```

### Step 8.3: Run + commit

```bash
make test-brain-sync
git add scripts/test-brain-sync.sh Makefile.nous
git commit -m "#4 M2: VM-based synthetic conflict test for brain-sync"
```

---

## Chunk 9: Claude Code hook wiring + auto-discovery

**Files:**
- Modify: `nous/.claude/settings.json` (add PreToolUse / PostToolUse hooks for brain-sync)
- Modify: `lib/brainsync/discovery.go` (add auto-discover-from-$HOME/workspace mode)

### Hooks

- [ ] In `nous/.claude/settings.json`, add:

```json
{
  "hooks": {
    "PreToolUse": [{
      "matcher": "Edit|Write|NotebookEdit",
      "hooks": [{
        "type": "command",
        "command": "brain-sync pre-write \"${TOOL_INPUT_FILE_PATH}\""
      }]
    }],
    "PostToolUse": [{
      "matcher": "Edit|Write|NotebookEdit",
      "hooks": [{
        "type": "command",
        "command": "brain-sync post-write \"${TOOL_INPUT_FILE_PATH}\""
      }]
    }]
  }
}
```

(Exact field names per Claude Code's hook schema; verify against current docs.)

### Auto-discovery

- [ ] Add `FindAllSharedBrainsInWorkspace()` that walks `$HOME/workspace/`. Use as default when `--brain` is not specified.

### Step 9.1: Wire + test + commit

```bash
git add nous/.claude/settings.json lib/brainsync/discovery.go
git commit -m "#4 M2: Claude Code hooks + auto-discovery"
```

---

## Verification before marking M2 done

- [ ] `go test ./lib/brainsync/...` — all pass.
- [ ] `go build -o cmd/brain-sync/bin/brain-sync ./cmd/brain-sync` — builds clean.
- [ ] `./cmd/brain-sync/bin/brain-sync --help` — lists daemon, service, pre-write, post-write.
- [ ] `make test-brain-sync` — VM end-to-end conflict test passes.
- [ ] Manual smoke: run daemon against a real shared brain on host, edit a file, verify commit lands on github.
- [ ] Update `nous#4` issue: tick M2 plan items, log close, attach actual_hours.
- [ ] Update `brain/data/project/shared-brain.md`: tick M2 task, optional detail block.
- [ ] Atlas update: extend `brain/atlas/sync-substrate-decision.md` "Daemon outline" section if implementation diverged from outline.
