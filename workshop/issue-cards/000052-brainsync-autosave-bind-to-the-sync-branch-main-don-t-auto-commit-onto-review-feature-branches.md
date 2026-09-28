---
id: '000052'
status: done
started: 2026-07-07T17:17:35-07:00
created: 2026-07-07
updated: 2026-07-07
estimate_hours: 0.43
actual_hours: 0.24
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
