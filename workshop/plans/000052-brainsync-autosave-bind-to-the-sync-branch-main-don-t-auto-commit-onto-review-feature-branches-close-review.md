# Boundary Review — nous#52 (whole-issue close)

| field | value |
|-------|-------|
| issue | 52 — brainsync autosave: bind to the sync branch (main) — don't auto-commit onto review/feature branches |
| repo | nous |
| issue file | workshop/issues/000052-brainsync-autosave-bind-to-the-sync-branch-main-don-t-auto-commit-onto-review-feature-branches.md |
| boundary | whole-issue close |
| milestone | — |
| window | aca4d8547fccfe0a28839d4b23ea500f0f580e97..HEAD |
| command | sdlc close --issue 52 |
| reviewer | claude |
| timestamp | 2026-07-07T17:32:36-07:00 |
| verdict | SHIP |

## Review

```verdict
verdict: SHIP
confidence: high
```

This is a clean, tightly-scoped fix that fully delivers nous#52's stated purpose: the daemon autosave now stands down whenever HEAD is off the `main` sync branch. The guard is placed correctly (mirroring the existing `MergeOrRebaseInProgress` skip at the top of `performAutocommit`), fails safe on a genuine branch-lookup error, and the new `CurrentBranch` helper correctly navigates the `git symbolic-ref --quiet` exit-1 trap that the plan-quality judge flagged — using the same direct-exec idiom already established by `SafeToFastForward` in the same file. Tests are real integration tests against temp git repos (no mocks), covering `main`/review/detached-HEAD, and I ran them plus the full package suite, `go build ./...`, and `go vet`: all green (15.6s). Atlas is updated. Nothing blocks SHIP; only two Minor future-notes below.

**1. Strengths**
- `lib/brainsync/autocommit.go:363-381` — guard sits exactly where the Spec asked (alongside the merge skip), returns `(false, nil)` off-main and `(false, err)` on lookup failure. The fail-safe direction (surface error, don't commit blind) is the correct choice and is called out in-comment.
- `lib/brainsync/git.go:56-72` — `CurrentBranch` correctly distinguishes detached-HEAD (exit 1 + empty stderr → `"", nil`) from a real git failure, rather than folding both into an error via `RunGit`. Consistent with the pre-existing `SafeToFastForward` pattern — right idiom, not a one-off.
- `lib/brainsync/autocommit_test.go:198-201` — the non-sync-branch test doesn't just assert "no commit"; it asserts the edit *remains modified-uncommitted* (`status --porcelain` contains `paris.md`), pinning that content isn't silently dropped. That's the higher-value assertion.
- Scope held exactly to Spec: `performAutocommit` is the *only* caller of the guard (verified by grep); `nous push` (`AddCommitPush`) is deliberately left unguarded so the explicit operator gesture still flushes on a side branch.

**2. Critical findings**
None.

**3. Important findings**
None.

**4. Minor findings**
- ARCH-DRY (future): the sync-branch name `"main"` is a scattered literal — ~15 refs across `git.go` (push/pull/reset/rev-list) and now one more in the guard (`branch != "main"`). Pre-existing condition, and the Spec explicitly acknowledges the coordination point, so the diff follows the established convention rather than introducing new drift. A `const SyncBranch = "main"` in `brainsync` would make the "changes in one coordinated place" the Spec promises literally one place. Note for a future cleanup, not this boundary.
- Style: verbose log `on branch %q (not main)` renders a detached HEAD as `on branch ""` — readable, but slightly opaque. Optional: special-case `""` to `(detached HEAD)`.

**5. Test coverage notes**
- Covered well: `CurrentBranch` happy paths (main / review-x / detached→`""`) and the daemon no-op on both review branch and detached HEAD.
- Gap (low severity): `CurrentBranch`'s *error* branch (non-1 exit / non-empty stderr, e.g. a non-git dir) and the guard's `err != nil` fail-safe return are untested. Worth noting this is a low-risk gap because *both* untested directions fail safe — a broken error-mapping would return `("", nil)` → stands down, and a lookup error → surfaces + no commit; neither can cause a wrong commit. A one-line `CurrentBranch(t.TempDir())`-on-a-non-git case would still be cheap insurance for the exit-code discrimination, which is the subtle part of the helper.

**6. Architectural notes for upcoming work**
- ARCH-PURE: pass. The added logic is a trivial predicate (`branch != "main"`) inside the daemon's IO-glue; the git call lives in the `git.go` seam and is integration-tested with real repos, not mocks. No business logic buried in IO.
- ARCH-PURPOSE (shadow-sweep): pass. The purpose is a single behavioral guard on the one commit path; there's no "single-source compiled to N consumers" surface here. `nous push` is a genuinely separable, intentionally-excluded gesture, not a deferred piece of the point. Purpose fully delivered.
- Pre-existing, out of scope: off-`main`, the *push* timer still arms and fires (`PushBrain` runs), but `Push` pushes the local `main` refspec regardless of checkout, so it never propagates review-branch commits — the harmful axis was commits, which this fixes. If the sync branch is ever generalized, revisit `PushBrain`/`HasUnpushedCommits`'s `origin/main..HEAD` alongside the guard.

**7. Plan revision recommendations**
None — the Plan's four checklist items are all delivered as described, the Core-concepts entities (`CurrentBranch` in `git.go`, guard in `performAutocommit`, the three tests) exist at their stated paths, and the Log accurately reflects the code. Plan still matches the code.
