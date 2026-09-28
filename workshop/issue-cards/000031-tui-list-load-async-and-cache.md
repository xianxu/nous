---
id: '000031'
status: done
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
