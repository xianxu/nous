---
id: 000032
status: working
deps: []
target: shared-brain-infrastructure-and-ui
created: 2026-05-21
updated: 2026-05-22
estimate_hours: 4
---

# Leave a shared brain — `nous brain leave` + TUI `l` key

## Problem

A collaborator on a shared brain has no first-class way to leave.
The only path today is:

  1. Ask an admin (likely a different person) to run `nous brain
     recipient remove` against my fingerprint.
  2. Wait for the manifest update to propagate.
  3. Manually go to GitHub and click "Leave repository."

That's clumsy, prone to delay (depends on someone else), and leaves
a limbo state where I'm still a GitHub collaborator (so the repo
shows up in `nous brain` accessible lists) but my fingerprint is
gone from the manifest (so I can't decrypt anything). The
collaborator-leave gesture should be one operation, owned by the
leaving party.

## Spec

### CLI: `nous brain leave [--brain PATH] [--delete-local]`

Default behavior (no flags), executed in this exact order:

  1. **Resolve target brain.** Walk up from cwd looking for
     `.brain/config.md` (the new `brain.EnclosingBrain` helper).
     Override with `--brain PATH`. Error cleanly if neither
     resolves.

  2. **Refuse-if-owner.** Parse `remote.origin.url` →
     `(owner, repo)`. If `owner` equals my `gh auth login`,
     refuse with an error pointing at "transfer ownership or
     delete the brain instead" (both out of scope for this
     issue). Rationale: owner-leave orphans the GitHub repo;
     less footgun to refuse.

  3. **Identify self fingerprint.** Find the fingerprint in
     `manifest.Recipients` that I hold the secret half of
     (`brain.LocalSecretFingerprints` already exists). If I'm
     not in the recipient list, refuse — "you're not a
     recipient on this brain, nothing to leave."

  4. **Last-recipient guard.** Reuse `brain.CanRemoveRecipient`
     — refuse to remove if it would orphan the brain. (For
     leave, this means I'm the last recipient; rare since the
     owner is normally a recipient, but possible if the owner
     already left and never transferred.)

  5. **Confirm.** Interactive prompt unless `--yes`:
     "Leave brain X? This removes your fingerprint from the
     manifest, pushes the change (re-encrypting to remaining
     recipients), and revokes your GitHub collaborator status.
     [y/N]"

  6. **Manifest-side update.** `WithoutRecipient` + `Write` +
     `brainsync.AddCommitPush` with message
     `"leave: <login> (<short-fp>) left the brain"`. The push
     wrapper syncs gcrypt-participants from the manifest before
     pushing, so the gcrypt push re-encrypts to the new
     (smaller) set. This step is the load-bearing one — without
     it, future commits would still be encrypted to me.

  7. **GitHub-side update.** `gh.RemoveCollaborator(owner, repo,
     my-login)` calls `DELETE /repos/{owner}/{repo}/collaborators/{me}`.
     GitHub allows a collaborator to remove themselves from a
     repo. Without this step, the repo still appears in
     `nous brain`'s accessible-but-not-cloned list (since the
     operator still has push access until GitHub revokes), and
     pull cycles continue to fetch encrypted-to-others packs
     that I can't decrypt.

  8. **Optional local cleanup.** With `--delete-local`:
     `os.RemoveAll(brainPath)`. Otherwise leave the directory
     alone; the operator can `rm -rf` manually. Reason
     `--delete-local` isn't default: the local dir is the only
     record of decrypted-by-me content; removing it loses any
     unique work I had locally. Make the gesture opt-in.

If any step fails mid-flow:

  - **Manifest commit fails** → abort, report error. My
    fingerprint is still in the manifest.
  - **Manifest push rejected** → resolve + retry (existing
    `brainsync.PushBrain` semantics).
  - **Manifest push hard fails** → abort, report. Don't remove
    GitHub collaborator status, since others can't see my
    departure yet.
  - **Collaborator removal fails** → report as a warning, exit
    success. The manifest update already landed; the leave is
    semantically done, just the GitHub-side cleanup didn't go
    through. Operator can retry the gh delete manually.

### TUI: `l` key on detail page

  1. New `leaveModel` (lib/tui/brain/leave.go) with stages:
     `leaveStageConfirm` → `leaveStageWorking` → `leaveStageDone`.
  2. `l` key on detail → emits `launchLeaveMsg{brainPath}`.
  3. Root model switches to the leave screen.
  4. Confirm stage shows the same summary text as the CLI; y/N.
  5. Working stage runs the same flow as the CLI (refactored
     into a shared function).
  6. Done stage shows ✓/✗ banner; any key returns to the list
     (with cache invalidated, since manifest + accessible list
     changed).

The TUI flow doesn't expose the `--delete-local` option in v1 —
keep it CLI-only until we've used it once and confirmed the
default behavior is right.

## Out of scope

- **Owner leave / transfer / delete.** A separate issue.
- **Multi-brain leave** (leave several at once). Single brain
  per invocation; matches the single-threaded-human assumption.
- **Undo / re-join.** Once you've pushed yourself out and the
  collaborator's been revoked, re-joining requires a fresh
  invite from the operator.

## Plan

- [ ] **M1 — gh primitive**
  - [ ] `gh.RemoveCollaborator(owner, repo, login)` — DELETE
        wrapper. Error semantics: nil on 204; gh error
        bubbled otherwise.
- [ ] **M2 — Shared leave logic**
  - [ ] Helper in cmd/nous: `runBrainLeave(ctx, brainPath,
        deleteLocal bool, confirm func() (bool, error)) error`.
        Confirm-injected so the TUI can reuse without prompting
        on stdin.
- [ ] **M3 — CLI**
  - [ ] `cmd/nous/brain_leave.go` with `newBrainLeaveCmd()`.
  - [ ] Register in `cmd/nous/brain.go`.
- [ ] **M4 — TUI**
  - [ ] `lib/tui/brain/leave.go` with `leaveModel`.
  - [ ] `lib/tui/brain/detail.go`: `l` keybind + help text.
  - [ ] `lib/tui/brain/root.go`: `screenLeave` + nav messages
        (`launchLeaveMsg`, `leaveDoneMsg`).
  - [ ] Cache invalidation on leaveDoneMsg success.
- [ ] **M5 — Tests + close**
  - [ ] Owner-refuse path.
  - [ ] Not-a-recipient path.
  - [ ] Last-recipient path.
  - [ ] Happy path: manifest update + push (two-peer-repo test).
  - [ ] `make close-issue` once operator-verifies on host.

## Log

- 2026-05-21: opened. Surfaced as a follow-up to the docs
  sweep — `nous brain leave` is the gesture symmetric to the
  TUI's accept-and-clone, and was missing.
- 2026-05-21: M1+M2 complete in one slice — `gh.RemoveCollaborator`
  added; leave logic factored into `brainsync.LeaveBrain` so
  the CLI + TUI both use the same code path. Refuse-checks
  (owner, not-a-collaborator, last-collaborator) live inside
  the shared function. Soft-fail on the github-revoke step so
  manifest-pushed-but-gh-failed is reported cleanly to the
  operator.
- 2026-05-21: M3 — `cmd/nous/brain_leave.go` registered;
  `nous brain leave [--brain PATH] [--delete-local] [-y]`.
- 2026-05-21: M4 — `lib/tui/brain/leave.go` with confirm /
  working / done stages; `l` key on detail page; root.go
  wires the screen and invalidates list cache on
  leaveDoneMsg.
- 2026-05-21: M5 — 4 unit tests passing (no-origin,
  non-github-origin, missing-brain, shortFpLast8). Owner-
  refuse + happy path tests need gh + gpg, deferred to
  operator-side e2e on the host.
- 2026-05-21: status = ready for operator-side e2e.
