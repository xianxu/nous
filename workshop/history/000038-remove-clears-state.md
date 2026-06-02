---
id: 000038
status: done
deps: []
github_issue:
created: 2026-06-01
updated: 2026-06-02
estimate_hours: 2
actual_hours: 2
---

# recipient remove: clear all per-brain revoke state

## Problem

`nous brain recipient remove` (and the TUI "remove collaborator" action,
`lib/tui/brain/detail.go:128`) does not fully revoke a recipient *from the one
brain it operates on* — it leaves enough state that the next invite/sync
silently resurrects them (observed in the nous#36 dogfood, 2026-06-01; details
in nous#37). Three concrete leaks:

1. **`RevokePubkey` deletes only `<FP>.asc`** (`lib/brain/peerkeys.go`), but the
   nous#26 path also publishes `<login>.asc`; `AutoAdmitFromKeysBranch` reads
   every `.asc` and derives the fp from contents → the login-keyed file survives
   and auto-admit re-admits.
2. **`verified.yaml` entry is never cleared** → the `login→fp` stays "verified",
   so a later re-publish is auto-admitted with no fresh ceremony.
3. **GitHub collaborator is not removed** → they keep repo (transport) access;
   the TUI's "remove collaborator" label is a lie (only `nous brain leave`
   revokes collaborator, self-only).

This is the **per-brain** completeness fix. The cross-brain fan-out, ban list,
and `nous identity revoke` verb stay in **nous#37** — this issue just makes a
single-brain remove actually stick (which is what unblocks clean dogfood resets,
nous#12). #3 is easy precisely because there's no fan-out here.

## Spec

Make `recipient remove` clear all three places for the target brain, reusing
existing primitives (`gh.RemoveCollaborator`, `brain.GitHubOwnerRepo`,
`brain.LoginForFingerprint`, `Read/WriteVerified`, `identity.Inspect`):

- **#1** — `RevokePubkey` deletes *every* keys-branch `.asc` whose parsed
  fingerprint matches (naming-agnostic: `<FP>.asc` + `<login>.asc` + any),
  instead of the single `<FP>.asc`.
- **#2** — new `RemoveVerifiedFor(brainRoot, fp) ([]logins, error)`: drop
  verified.yaml entries whose fingerprint matches; return the removed login(s)
  (needed for #3). Cleared *before* the re-key push so manifest + verified.yaml
  land in one commit.
- **#3** — after the push + keys-branch revoke, if the brain has a GitHub remote,
  resolve the login (from the removed verified entry, else
  `LoginForFingerprint`) and call `gh.RemoveCollaborator(owner, repo, login)`.
  Best-effort: on failure print the manual `gh api -X DELETE …` fallback (mirror
  `nous brain leave`'s UX). Skip cleanly for local brains (no remote).
- **DRY/anti-drift:** the CLI and TUI must run the *same* complete sequence —
  extract it into one lib function (e.g. `brainsync`/`brain` helper) that both
  call, so they can't diverge again (CLI/TUI drift is how this class of bug
  arose).

Unchanged: the existing safeguards (last-recipient guard, `WouldLockOut`
self-removal `--force`, the revocation-reality caveat, TTY gate). The
forward-secrecy caveat still holds — this is about stopping *resurrection*, not
clawing back already-fetched blobs.

## Done when

- After `recipient remove <brain> <fp>`: the fp is gone from the manifest, both
  `<FP>.asc` and `<login>.asc` are gone from the keys branch, the verified.yaml
  entry is gone, and the GitHub collaborator is removed (or a clear manual-retry
  hint printed). A subsequent re-publish to the keys branch is NOT auto-admitted.
- CLI and TUI share one implementation.
- Unit test for `RemoveVerifiedFor` (match by fp, returns logins, no-op when
  absent). `brain-vm-e2e.sh` extended: admit a peer, remove them, re-publish
  their `<login>.asc`, assert auto-admit does NOT bring them back + manifest
  stays clean.

## Plan

- [x] M1: `RevokePubkey` content-match delete (all `.asc` for the fp);
  `RemoveVerifiedFor` in verified.go + unit test; extract a shared
  complete-remove helper (`brainsync.RemoveRecipient`); wire CLI + TUI through
  it; add the gh collaborator removal (best-effort + manual fallback);
  `--force` lifts the remove TTY gate (scriptable). Extend `brain-vm-e2e.sh`
  with the remove-sticks (no-resurrection) assertion. Single milestone.

## Log


- 2026-06-02: closed — per-brain recipient remove clears all stores (manifest+rekey+verified.yaml+keys-branch+collaborator); RemoveVerifiedFor unit tests + brain-vm-e2e remove-sticks section green. --force: trailers not used this session; codex review covered span
### 2026-06-01

Carved from nous#37 as the per-brain slice (leaks #1/#2/#3, no fan-out) so it
can land quickly and de-risk dogfood resets. nous#37 retains the cross-brain
fan-out + ban list + `nous identity revoke` verb.

Implemented. A **fourth** drift surfaced during the work: the TUI remove
(`applyCmd`) didn't even call `RevokePubkey` — it only rewrote the manifest +
pushed, so it was *more* incomplete than the CLI. Settled by extracting one
`brainsync.RemoveRecipient` (mirrors `LeaveBrain`) that does the complete
sequence; both CLI and TUI now call it, so they can't diverge again. `--force`
now also lifts the remove TTY gate (consistent with how `--verified-last8`
lifts it for add, and required to script remove in the e2e).

Verified: `RemoveVerifiedFor` unit tests (match/no-op/no-file); full
`scripts/brain-vm-e2e.sh` green end-to-end including a new remove-sticks
section that seeds BOTH resurrection vectors (verified.yaml + `<login>.asc`)
and asserts remove clears manifest + verified.yaml + every keys-branch entry
for the fp. `go test` green for lib/brain, lib/brainsync, lib/tui, cmd/nous
(lib/brain only fails under the sandbox's gpg-agent IPC block; passes
unsandboxed). #3 (collaborator) reuses `gh.RemoveCollaborator` (already proven
in `nous brain leave`); the file:// e2e can't exercise the GitHub call, so it's
covered by reuse + code, not the script.
