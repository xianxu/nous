---
id: 000030
status: working
deps: []
created: 2026-05-21
updated: 2026-05-21
estimate_hours: 8
---

# brainsync: autosave + `nous push` checkpoint

## Problem

Today, the operator drives sync explicitly: they run `git commit`
in a brain, RefWatcher sees the ref change, brainsync pushes.
Commit cadence = push cadence = explicit human gesture.

This breaks the "brain as extension of thinking" model in two ways:

1. **Cognitive overhead.** Operators are constantly choosing
   commit boundaries while doing the actual work (writing,
   editing, dropping new files into the brain). Git is the
   substrate, not the user surface — but it leaks through.
2. **Lost work on idle gaps.** Anything saved-but-not-committed
   is invisible to peers and unrecoverable if the laptop dies.

The substrate should hide git from operators in the common path:
they save files in their editor, the daemon takes care of
committing, batching, and pushing. The explicit human gesture
moves from `git commit && git push` to an optional
`nous push "label"` — a *checkpoint* that names a moment.

## Spec

Two layers, both run inside the existing brainsync daemon (one
per discovered brain, alongside the existing RefWatcher + fetch
ticker):

### Layer 1: Auto-commit on save (5s debounce)

- Recursive fsnotify on the brain dir, excluding `.git/`.
- On any file event → reset *commit-debounce timer* (5s).
- When timer fires, if `git status --porcelain` reports
  modifications to *tracked* files AND no merge/rebase/cherry-
  pick is in progress, run `git commit -a -m "<autosave msg>"`.
- Untracked / removed files are NEVER auto-handled. The first
  add is an explicit operator gesture; on first commit attempt
  that detects untracked or unstaged-deletions, surface a hint
  in the daemon log: `brain X: untracked files — use 'git add'
  to include them in autosave`.
- Per-brain opt-out via manifest `autosave: off`. Default **on**.
- Commit message format: `autosave: <RFC3339 timestamp> [N files]`.

### Layer 2: Debounced push (60s)

- Every file change OR commit resets a *push-debounce timer* (60s).
- When timer fires, run `PushBrain` (existing function, idempotent
  if nothing to push).
- This decouples commit cadence (5s, granular safety net) from
  push cadence (60s, what peers see) — addresses the gcrypt
  amplification concern: peer-visible work happens at most every
  60s instead of every 5s.

### Layer 3: `nous push` CLI

Manual flush + label gesture. Single positional message arg:

```
nous push                       # push enclosing brain now
nous push "finished tokyo draft" # commit-with-message + push
```

Semantics:
- Detect *enclosing brain* by walking up from cwd looking for
  `.brain/config.md`. Error out cleanly if not in a brain.
- Untracked-or-deleted files: print hint to use `git`. Do NOT
  abort — proceed with pushing what's already in the index.
- If tracked modifications exist:
  - With positional msg: `git commit -am "<msg>"`
  - Without: `git commit -am "<autosave msg>"`
- Then push, bypassing the 60s debounce.
- If nothing uncommitted AND positional msg provided: push
  existing commits; print notice that msg was ignored (v1: no
  empty commits, no tags).
- If nothing to commit AND nothing to push: silent no-op.
- Works whether daemon is running or not — calls `PushBrain`
  directly. (No IPC needed; git's lock files serialize against
  concurrent daemon pushes.)

### Out of scope for v1

- **Squash autosave commits at push time.** Granular history
  preserved both locally and remotely in v1. Squash policy
  (collapse consecutive `autosave:`-prefix commits into one at
  push) is its own issue (#31 — to be filed as follow-up).
- **Pluggable substrate abstraction.** The "explicit add for
  untracked" rule is a seam pointing at a future where the
  substrate isn't necessarily git. Not designed for here.
- **`nous push` from outside a brain.** Errors with a clean
  message; we don't try to flush "all brains" — matches the
  single-threaded-human assumption.

## Plan

- [ ] **M1 — Foundations**
  - [ ] Add `EnclosingBrain(cwd)` helper that walks up to find
        `.brain/config.md`. Returns absolute brain path + parsed
        manifest, or error.
  - [ ] Extend manifest schema with `autosave: on|off` (default on).
        Update brain.Manifest parser + tests.
  - [ ] Tests.
- [ ] **M2 — AutoCommitter daemon goroutine**
  - [ ] `lib/brainsync/autocommit.go`: recursive fsnotify watcher
        on brain dir (skip `.git/`); state machine with two
        timers (commit 5s, push 60s); skip during merge/rebase/
        cherry-pick.
  - [ ] Hook into existing `Watch` — one AutoCommitter per brain,
        honoring `autosave: off`.
  - [ ] Untracked-files hint log path.
  - [ ] Tests (debounce timing, skip-during-merge, skip-when-
        manifest-off, untracked-hint path).
- [ ] **M3 — `nous push` CLI**
  - [ ] `cmd/nous/push.go` — flag parsing + EnclosingBrain
        resolution + commit/push logic.
  - [ ] Registered in `cmd/nous/main.go`.
  - [ ] Tests covering: in-brain success, not-in-brain error,
        uncommitted+msg, uncommitted+no-msg, only-unpushed,
        nothing-to-do, untracked-hint.
- [ ] **M4 — Atlas + verification**
  - [ ] `atlas/autosave-and-checkpoint.md` describing the model.
  - [ ] Link from `atlas/index.md`.
  - [ ] End-to-end manual verification: edit two files in a
        brain, wait 5s → see autosave commit; wait 60s → see push.
        Run `nous push "label"` mid-flow → see immediate push
        with that message.
  - [ ] `make close-issue ISSUE=30 ACTUAL=<h> VERIFIED='<evidence>'`

## Log

- 2026-05-21: opened. Three independent prep fixes already
  landed (commits 741b38a, b09d429, 8ddc8dc): WORKSPACE_ROOT
  plist, startup catch-up push, 5s fetch default. These remove
  the most painful sharp edges in the current pull-driven model
  before we layer autosave on top.
- 2026-05-21: M1 complete — `brain.EnclosingBrain(cwd)` walks up
  from cwd to find `.brain/config.md`; manifest gains
  `Autosave` field + `AutosaveEnabled()` method (default on).
- 2026-05-21: M2 complete — `brainsync.AutoCommitter` ships
  with recursive fsnotify watcher, 5s commit debounce, 60s
  push debounce, skip-during-merge/rebase/cherry-pick,
  untracked/deleted-hint with dedup, and wired into
  `brainsync.Watch`: per-brain committer for autosave-enabled
  brains; RefWatcher events route to its push debouncer so
  manual commits coalesce. 8 unit tests + 5 manifest/enclosing
  tests passing.
- 2026-05-21: M3 complete — `nous push [msg]` lands in
  `cmd/nous/push.go` and registered in main.go. EnclosingBrain
  + commit + push, with untracked/deleted hint, merge/rebase
  refusal, and the "no empty commits in v1" rule (msg ignored
  if nothing uncommitted). 7 CLI tests passing.
- 2026-05-21: M4 complete — atlas/nous/autosave-and-
  checkpoint.md describes the new surface; pointers from there
  to all the relevant code files.
- 2026-05-21: verification status — all touched-package tests
  green (lib/brain, lib/brainsync, cmd/nous; total of 20 new
  tests + full existing suites). Sandbox-pre-existing failures
  in lib/identity (hardcoded /tmp paths + gpg-agent
  unavailability) are unrelated. End-to-end on a live brain
  with the daemon running requires operator-side verification
  on a real VM — not exercisable in this sandbox.
