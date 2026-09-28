---
id: 000024
status: done
deps: [000023]
created: 2026-05-19
updated: 2026-05-19
estimate_hours: 2
---

# Manifest as canonical source of recipients; sync at push, not at callsite

## Problem

`.brain/config.md`'s `recipients:` list and `.git/config`'s
`remote.origin.gcrypt-participants` are two stores of the same fact:
"who is on this brain." Today the invariant "these must agree" is
maintained by every callsite that mutates one calling the helper to
mutate the other.

```
nous brain new           → WriteManifest      + SetGcryptParticipants
nous brain recipient add → RewriteFrontmatter + SetGcryptParticipants
nous brain recipient rm  → RewriteFrontmatter + SetGcryptParticipants
nous brain clone         → (clone)            + SyncGcryptParticipantsFromManifest
brainsync.PullBrain      → (pull)             + SyncGcryptParticipantsFromManifest
lib/tui/brain/recipient_add.go → (same as cmd/nous/brain_recipient.go)
```

Two reasons this is a smell:

1. **Doubles cognitive load.** "Did I update both?" is something
   future code paths shouldn't have to think about. The 2026-05-19
   bug fixed in `dd7eb95` was exactly this kind of omission — the
   clone path was missing the gcrypt-participants sync, only
   discovered when the e2e test (filed alongside) exercised the
   multi-peer push scenario.

2. **Hides the source of truth.** Both stores look authoritative
   from outside the code. The manifest is the *intended* canonical
   record (human-readable, travels with the brain, version
   controlled), but nothing enforces the "derived" relationship of
   `gcrypt-participants`.

The drift modes that survive today's discipline:

- Operator hand-edits `.brain/config.md` → gcrypt-participants
  stale → next push encrypts to old set.
- Operator runs `git config remote.origin.gcrypt-participants ...`
  → manifest stale → audit/identity tooling reads wrong list.
- New code path mutates manifest without thinking about config.

## Insight

`gcrypt-participants` exists for exactly one reason:
`git-remote-gcrypt` is a separate subprocess that reads from
`.git/config` at push time. We can't pass it in-memory state.
But that's its ONLY consumer. Every other thing that wants to know
"who's on this brain" can (and should) read the manifest.

So treat `gcrypt-participants` as a **write-only cache derived from
the manifest** rather than a parallel store. Refresh it at the only
moment it's actually used: right before push.

## Done when

- `lib/brainsync/git.go` `AddCommitPush` and `Push` call
  `brain.SyncGcryptParticipantsFromManifest` at the top, before
  the `git push` invocation. The push wrapper becomes the
  single sync point.

- Every other callsite that wrote `gcrypt-participants` is
  removed (their write is now redundant; the push wrapper
  handles it):
    - `cmd/nous/brain_new.go`
    - `cmd/nous/brain_recipient.go` (admit + revoke)
    - `lib/tui/brain/recipient_add.go`
    - `cmd/nous/brain_clone.go` (post-clone sync removed)
    - `lib/brainsync/watch.go` `PullBrain` (post-pull sync removed)

- Integration test (`lib/brain/integration_test.go`) helpers
  dropped their explicit `SetGcryptParticipants` calls and the
  test still passes — proves the push wrapper is sufficient.

- Manifest invariants documented: `lib/brain/write.go` docstring
  on `SetGcryptParticipants` updated to note "callers should
  prefer mutating the manifest and letting push-wrapper sync
  handle it; direct callers are now only the push wrapper
  itself."

## Out of scope

- **Removing `SetGcryptParticipants` entirely** — kept as the
  underlying primitive that `SyncGcryptParticipantsFromManifest`
  calls. Just no longer the operator-facing surface.

- **Custom git remote helper (option d from the design
  discussion)** — explicitly rejected. Reimplementing gcrypt's
  cryptographic protocol carries protocol-design risk
  (don't-roll-your-own-crypto at the protocol level) and ~500-1000
  lines of new code for marginal payoff at family-brain scale.
  Revisit if `git-remote-gcrypt` itself becomes unmaintained.

- **Doctor verb** (`nous brain doctor` that detects drift) —
  not needed if drift becomes impossible. The hand-edit path
  becomes self-correcting on next push.

## Spec

### Push-wrapper sync

In `lib/brainsync/git.go`:

```go
func AddCommitPush(repo, msg string) error {
    // BEFORE any push: ensure gcrypt-participants reflects the
    // brain's manifest. Drift-prevention single sync point.
    if err := brain.SyncGcryptParticipantsFromManifest(repo); err != nil {
        return fmt.Errorf("sync participants from manifest: %w", err)
    }
    // ... existing add / commit / push ...
}

func Push(repo string) error {
    if err := brain.SyncGcryptParticipantsFromManifest(repo); err != nil {
        return fmt.Errorf("sync participants from manifest: %w", err)
    }
    // ... existing push ...
}
```

Order matters subtly: the sync writes to `.git/config`, which the
gcrypt subprocess reads at `git push` time. Putting the sync at the
top of the wrapper makes the dependency clear and removes any
window where a concurrent operation could see stale state.

### Manifest pre-condition

`brain.SyncGcryptParticipantsFromManifest` already handles "no
manifest exists yet" by returning an error. The push wrapper
propagates that error. Callers (e.g. `provisionBrain` /
`cmd/nous/brain_new.go`) must write the manifest before calling
the push wrapper — which they already do.

### Removed redundant calls

For each location in "Done when," verify the manifest has been
written *before* the push wrapper runs, then delete the
SetGcryptParticipants / SyncGcryptParticipantsFromManifest call.
The push wrapper makes them all unnecessary.

### Integration test simplification

`provisionBrain` and `admitRecipient` in
`lib/brain/integration_test.go` drop their explicit
`SetGcryptParticipants` calls. If the test still passes with the
new push-wrapper-only sync, the refactor is correct end-to-end.

## Plan

- [x] M1: Add sync to `AddCommitPush` and `Push` in
      `lib/brainsync/git.go`.
- [x] M2: Remove redundant calls from cmd/nous and lib/tui paths.
      Audit confirmed: `SetGcryptParticipants` no longer called
      directly anywhere in `cmd/nous/*` or `lib/tui/*`. The push
      wrapper is the single caller.
- [x] M3: Drop the corresponding helper calls from the integration
      test. Test still passes locally (host-side; sandbox can't
      run gpg subprocesses).
- [x] M4: Update docstrings on `SetGcryptParticipants` and
      related code to name the push-wrapper as the canonical
      caller. Also dropped the post-clone (`cmd/nous/brain_clone.go`)
      and post-pull (`lib/brainsync/watch.go` PullBrain) sync
      calls — band-aids from `dd7eb95` that the new single sync
      point makes redundant.

Single commit.

## Test plan

Integration test (`lib/brain/integration_test.go`) is the main
gate. After removing explicit `SetGcryptParticipants` calls from
the test helpers, the multi-recipient + file-sync flow must still
pass end-to-end. Specifically:

- peerA's push after peerB is admitted should encrypt to the
  current recipient set (all three), without operator running
  any explicit "sync" command.
- operator can decrypt peerA's push.
- peerB can decrypt peerA's push.

If those work, the push-wrapper sync is doing the right thing.

## Notes

This is the architectural cleanup version of the fix landed in
`dd7eb95`. That commit added syncs to clone + pull as targeted
band-aids; this issue removes those band-aids in favor of a
single sync point that's correct by construction.

The trade-off table from the design discussion (lib `nous` chat
2026-05-19) is reproduced for posterity:

| Option | Mechanism | Pros | Cons |
|---|---|---|---|
| (a) doctor verb | detect + repair on demand | recoverable | doesn't prevent drift |
| (b) preflight check | check before push | catches everything | adds a read per push |
| (c) push-wrapper sync | manifest canonical, derived to config before push | drift impossible; one sync point | trusts the manifest write to be atomic-enough (it is) |
| (d) custom remote helper | replace `git-remote-gcrypt` | true single source | crypto protocol risk; 500-1000 LOC |

Picked (c).

## Log

### 2026-05-19 — landed

Implemented as the design called for: push wrapper is the single
sync point; every other `SetGcryptParticipants` call removed.

Changes by file:

- `lib/brainsync/git.go`: `AddCommitPush` and `Push` now call
  `brain.SyncGcryptParticipantsFromManifest` before invoking `git
  push`. Single sync point. Imports `lib/brain` (no cycle —
  brainsync already imported brain).
- `lib/brainsync/watch.go`: removed the post-pull sync added in
  `dd7eb95` (now redundant — next push handles it).
- `cmd/nous/brain_new.go`: removed `SetGcryptParticipants(abs,
  recipients)` call after `WriteManifest`.
- `cmd/nous/brain_recipient.go`: removed `SetGcryptParticipants`
  from both admit and revoke paths.
- `cmd/nous/brain_clone.go`: removed post-clone
  `SyncGcryptParticipantsFromManifest` (also added in `dd7eb95`).
  Also dropped the `deriveCloneTarget` helper (was only used by
  the now-removed sync call).
- `lib/tui/brain/recipient_add.go` and `recipient_remove.go`:
  removed `SetGcryptParticipants` from the bubbletea apply paths.
- `lib/brain/integration_test.go`: removed explicit
  `SetGcryptParticipants` calls in `provisionBrain` and
  `admitRecipient`. The test now also exercises the "manifest is
  canonical" property — if it still passes, the push wrapper is
  doing the right thing.
- `lib/brain/write.go`: docstring on `SetGcryptParticipants`
  rewritten to name the push wrapper as the canonical caller and
  flag "new code should not call this directly."
- `lib/brain/recipient.go` and `lib/tui/brain/recipient_add.go`:
  stale code-flow comments updated to drop the standalone
  `SetGcryptParticipants` step.

Audit: `grep -rn 'SetGcryptParticipants(' cmd/nous/ lib/tui/`
returns empty. Direct callers are now `SyncGcryptParticipantsFromManifest`
(in `lib/brain/write.go`) and the integration test (where it tests
the primitive directly, separate from the push-wrapper integration).

Verification: build + vet clean. The integration test would
exercise the full multi-recipient + file-sync story without any
explicit `SetGcryptParticipants` calls — if that passes on host,
the single-sync-point design is end-to-end correct.
