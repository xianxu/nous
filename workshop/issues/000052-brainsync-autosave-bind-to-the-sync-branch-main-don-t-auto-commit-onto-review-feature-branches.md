---
id: 000052
status: working
deps: []
github_issue:
created: 2026-07-07
updated: 2026-07-07
estimate_hours: 0.43
started: 2026-07-07T17:17:35-07:00
---

# brainsync autosave: bind to the sync branch (main) — don't auto-commit onto review/feature branches

## Problem

The brainsync autosave daemon (`AutoCommitter.performAutocommit`, `lib/brainsync/autocommit.go`)
commits modified-tracked files on **whatever branch is checked out**. But the entire
brainsync sync model is hardwired to `main` — `git.go` pushes/pulls/resets against
`origin main` throughout. So when the operator (or a tool) checks out a non-`main`
branch, the daemon keeps auto-committing onto it, which is both off-model and harmful:

- **Off-model:** those `autosave:` commits are never pushed (the push path targets
  `main`), so they just pile up on and pollute the side branch.
- **Clobbers structured branches:** a branch like `review/<slug>` (pair review workbench)
  or an `sdlc` feature branch owns its own deliberate commit cadence. The daemon
  committing underneath it races that cadence.

Observed live (2026-07-07): during a pair `review/pvp` session in the `brain` repo, the
5s autosave repeatedly **preempted `docflow` round journaling** (rounds landed as anonymous
`autosave:` commits instead of attributed `review(pvp): agent rN`), forcing a manual
soft-reset + re-journal each round; and once the autosave↔review-pane race left the pane
reporting "applied 3 edits" while **nothing persisted to disk** (a silent edit-loss desync).

Root cause: two git writers on the same branch. brain *intends* to autosave as a scratchpad
— but that's the behavior of the **scratch trunk (`main`)**, not of every branch. A branch is
a deliberate context with its own commit discipline.

## Spec

Bind the daemon autosave to the sync branch. `performAutocommit` becomes a no-op
(`committed=false`, no error) when HEAD is **not on `main`** — a `review/*` branch, an
`sdlc` feature branch, or a detached HEAD. On those, the operator/tool owns commits; the
daemon stands down.

Scope decisions:
- **Only the automatic daemon path is guarded.** `nous push` (the explicit operator
  gesture, `cmd/nous/push.go`) is unchanged — if the operator explicitly asks to flush on a
  side branch, that's their call.
- **`main` is hardcoded, consistent with the rest of brainsync** (push/pull/reset are all
  `origin main`). This isn't a *new* assumption — it matches the model. If the sync branch
  is ever generalized, it changes in one coordinated place across `git.go` + this guard.
- Guard sits alongside the existing `MergeOrRebaseInProgress` skip at the top of
  `performAutocommit` — same shape (a precondition that makes autosave a clean no-op).

## Done when

- `performAutocommit` returns `(false, nil)` without committing when HEAD is not `main`.
- A `CurrentBranch(repo)` helper exists in `git.go` (detached HEAD → `""`, not an error).
- Unit test proves: autosave commits on `main`; no-ops on a `review/x` branch and on a
  detached HEAD (working tree with a modified-tracked file → HEAD unchanged).
- `go test ./lib/brainsync/...` green; atlas note added for the branch-bound behavior.

## Estimate

Produced via `brain/data/life/42shots/velocity/estimate-logic-v3.1.md` against `baseline-v3.1.md`. Method A only.

```estimate
model: estimate-logic-v3.1
familiarity: 1.0
item: smaller-go-module   design=0.2 impl=0.2
design-buffer: 0.15
total: 0.43
```

One primitive: extend the well-specced `brainsync` module (a `CurrentBranch` helper +
a guard in `performAutocommit` + unit tests). Design is essentially resolved (code
analyzed, guard shape fixed against `autocommit.go`/`git.go`), so the +15% design buffer.

## Plan

- [x] Add `CurrentBranch(repo string) (string, error)` to `git.go` (via `git symbolic-ref
      --quiet --short HEAD`; detached HEAD → `""`, nil) + a git_test case.
- [x] Guard at the top of `performAutocommit`: if `CurrentBranch(a.brain) != "main"`, verbose-log
      and return `(false, nil)`.
- [x] `autocommit_test.go`: extend the commit test to assert commit-on-`main`, and add
      no-op cases for a `review/x` branch and detached HEAD.
- [x] `go test ./lib/brainsync/...`; update `atlas/` for the branch-bound autosave.

## Log

### 2026-07-07
- Root-caused from a live pair `review/pvp` session in `brain`: autosave (brainsync
  `AutoCommitter`) is branch-agnostic while the rest of brainsync is `main`-only. Fix is a
  sync-branch guard in `performAutocommit`. Design confirmed against `autocommit.go` +
  `git.go` (all `origin main`).
- Implemented: `CurrentBranch(repo)` in `git.go` (runs `symbolic-ref --quiet --short HEAD`
  via `exec` directly — NOT `RunGit` — so a detached HEAD's silent exit-1 maps to `("", nil)`
  instead of being folded into an error; the plan-quality judge flagged this trap). Guard at
  the top of `performAutocommit`, mirroring the `MergeOrRebaseInProgress` skip: `CurrentBranch
  != "main"` → verbose-log + `(false, nil)`; a real lookup error fails safe (surface it, don't
  commit blind — the judge's 2nd note).
- Tests (all pass): `TestCurrentBranch` (main / review-x / detached→""), plus
  `TestAutoCommitter_SkipsNonSyncBranch` (edit on `review/pvp` → no commit, stays
  modified-uncommitted) and `TestAutoCommitter_SkipsDetachedHead`. `go build ./...` +
  `go vet` clean; `go test ./lib/brainsync/...` green (15.5s).
- Atlas: `atlas/nous/autosave-and-checkpoint.md` — added the sync-branch binding to "Skip
  cases" + pointers.
- Scope held: `nous push` (explicit gesture, `cmd/nous/push.go`) intentionally left
  unguarded — operator intent overrides.
