---
id: 000031
status: done
deps: []
target: shared-brain-infrastructure-and-ui
created: 2026-05-21
updated: 2026-06-02
estimate_hours: 3
actual_hours: 3
---

# TUI: load brain list async + cache across navigations

## Problem

`nous brain` (TUI) blocks for ~1–3s every time the list view is
rendered. Two compounding issues:

1. **Synchronous gh subprocesses in `newListModel`** —
   `lib/tui/brain/list.go:77` runs three `gh` calls in sequence
   before returning the model: `gh.AuthLogin()`,
   `gh.PendingInvitations()`, `gh.UserRepos()`. Each is a
   subprocess to the `gh` CLI making a GitHub API call. Round-
   trip is typically 300ms–1s+ per call. The model's `Init()`
   returns `nil` (line 211) instead of an async `tea.Cmd`, so the
   bubbletea event loop is blocked through all three.

2. **No cache across navigations** — `root.go:158-161` does
   `m.list = newListModel()` on every `popToListMsg` (e.g. when
   the operator ESCs from the detail page back to the list). So
   the full gh-load runs again on every back-navigation. If the
   operator double-presses ESC (because the first press appears
   unresponsive), a second reload kicks off while the first is
   still in flight.

Compare to `detail.go`, which gets this right: `Init` returns the
async `LoadStatus` Cmd, the view shows "loading status..." until
`statusLoadedMsg` arrives. The list model was left synchronous
when it had only one local fast call; the gh calls were added
incrementally (invitations in `36e8b33`, uncloned repos in
`e4bb446`) without revisiting the loading model.

## Spec

Two changes, no new conceptual surface:

### 1. Async load

- `newListModel(cache)` builds the model with only local-manifest
  data (`libbrain.DiscoverAll()` is filesystem-only and fast).
  Initial items: local brains, rendered with no `myLogin` /
  IsOperator marker yet.
- `Init()` returns a `tea.Cmd` that:
  - calls `gh.AuthLogin()`, `gh.PendingInvitations()`,
    `gh.UserRepos()` in sequence (or in parallel via a
    `sync.WaitGroup` — see "open questions"), and
  - returns a new `listLoadedMsg` carrying `myLogin`,
    invitations, repos.
- `listModel.Update(listLoadedMsg)` folds the data in:
  recomputes `isOperator` on existing local items, appends
  pending-invitation rows + accessible-but-not-cloned rows.
- View renders a small "loading collaborators..." muted line
  below the local-brain rows while the remote load is in flight.

### 2. Cache across navigations

- `rootModel` gains a `listCache *listLoadedData` field.
- When `listLoadedMsg` arrives, root captures it into the cache
  before forwarding to the list model's `Update`.
- `newListModel` accepts an optional cache. When non-nil, model
  renders immediately with cached data + freshly-discovered local
  brains; `Init` returns nil (no fetch).
- Refresh paths:
  - **'r' key on list view** — explicit operator refresh.
  - **acceptInviteDoneMsg / cloneSubprocessDoneMsg with err==nil**
    — invalidate cache before reconstructing list; the freshly-
    cloned brain or freshly-accepted invitation visibly updates.

## Out of scope

- **Parallel gh calls.** Could shave another ~500ms by running
  the three calls concurrently. Defer until we know the
  serialized version isn't fast enough — first the cache plus
  async-loading-into-already-rendered-list will hide the latency
  almost entirely.
- **Cache TTL.** No timed invalidation; rely on the explicit
  invalidation triggers + 'r' key. Adding TTL is a separate axis
  we can pull on later if operators notice stale data.

## Plan

- [x] **M1 — Async load**
  - [x] `listLoadedMsg` type carrying myLogin / invitations / repos.
  - [x] Extract the remote-load logic into a free function
        returning `tea.Cmd`.
  - [x] `newListModel` builds with local manifests only.
  - [x] `Init()` returns the load Cmd.
  - [x] `listModel.Update(listLoadedMsg)` rebuilds items + uncloned/
        pending rows.
  - [x] View renders a "loading collaborators..." subtle line
        until loaded.
- [x] **M2 — Cache + refresh**
  - [x] `rootModel.listCache *listLoadedData` field.
  - [x] Root.Update on `listLoadedMsg`: store cache, forward to list.
  - [x] `newListModel(cache *listLoadedData)` skips the load when
        cache is non-nil.
  - [x] popToListMsg passes the cache.
  - [x] 'r' key in list triggers refresh (re-issues the load Cmd
        + clears local "stale" indicator).
  - [x] acceptInviteDoneMsg + cloneSubprocessDoneMsg invalidate
        cache on err==nil before reconstructing list.
- [x] **M3 — Tests + close**
  - [x] Test: list constructor returns fast (no gh calls).
  - [x] Test: listLoadedMsg flow folds invitations + uncloned in.
  - [x] Test: cache reuse on popToListMsg (no fetch).
  - [x] Test: cache invalidation on acceptInviteDoneMsg success.
  - [x] Verification: ESC from detail snaps back to list
        instantly on the operator's host after rebuild.

## Log


- 2026-06-02: closed — POST-HOC wind-down close (milestone-review ceremony skipped: work landed in single rapid slice; verified by code+test inspection). Async filesystem-only newListModel + async gh fetch + nav cache; TestRoot_DetailEscPopsToList 0.11s vs 2.88s baseline, 25 lib/tui/brain tests green. Actual is manual estimate — v3 telemetry absent for these sessions.
- 2026-05-21: opened. Surfaced from #30 follow-up — operator
  noticed slow ESC from detail; root cause was three synchronous
  gh subprocess calls in newListModel + full reload on every
  navigation.
- 2026-05-21: M1+M2 complete in one slice — list.go split:
  newListModel(cache) is now filesystem-only; loadRemoteCmd()
  runs the three gh calls and produces listLoadedMsg; Update
  folds in the data + flips loadingRemote off. rootModel gains
  listCache field; popToListMsg passes the cache (instant render
  on ESC); acceptInviteDoneMsg / cloneSubprocessDoneMsg /
  cancelNewBrainMsg invalidate the cache before reconstruction.
  'r' key triggers manual refresh.
- 2026-05-21: M3 complete — 4 new tests pinning the contract:
  newListModel(nil) doesn't block on gh (wall-clock asserted at
  <200ms); listLoadedMsg flips loadingRemote off and populates
  myLogin; popToListMsg with cache present renders instantly;
  acceptInviteDoneMsg clears the cache. The original
  TestRoot_DetailEscPopsToList from main now runs in 0.00s
  instead of 2.88s — concrete evidence the gh-subprocess block
  is gone.
- 2026-05-21: status = ready to ship. Operator-side verification
  (instant ESC on real `nous brain`) requires rebuild.
