---
id: 000047
status: done
deps: []
created: 2026-06-08
updated: 2026-06-08
estimate_hours: 3
actual_hours: 1.27
---

# auto push for private but published repo (brain)

auto commit and auto publish was designed to work with shared brain. now it seems to me that it should work for any published brain (even if it's private). that seems better ergonomics. the thesis is brain's work, is less structured, and thus don't need to follow very clear commit cleanness of code.

## Done when

- Every brain (any `.brain/config.md` dir) gets the local autosave commit safety net, even with no remote.
- A private-but-published brain (single recipient, plain non-gcrypt remote) auto-pushes when the operator opts in, without being confused with a read-only mirror.
- gcrypt/shared brains keep their current auto-push behavior (no regression).
- A plain-remote brain with no opt-in is committed locally but NOT auto-pushed.

## Spec

Today the brainsync daemon couples two things into one "is this brain watchable?"
predicate (`isWatchable` in `lib/brainsync/discovery.go`): a brain is watched
iff it is **shared** (≥2 recipients) OR has a **gcrypt::** remote. A watched
brain gets *both* the local autosave commit loop AND the auto-push loop. A
single-recipient brain with a plain remote, or with no remote, is skipped
entirely — no commit, no push.

This issue decouples the two cadences (commit vs. push) on a per-brain basis:

**Commit (autosave) — applies to every brain.** Local safety net. A brain's
work is less structured than code; we don't want the operator hand-managing
commit boundaries. Default on; existing `autosave: off` opt-out still disables
it. No-remote brains commit locally (push is simply skipped).

**Push (publish) — derived, with an opt-in for plain remotes.**
- gcrypt remote OR shared (≥2 recipients) → auto-push (current behavior, no regression).
- plain remote (e.g. a private GitHub repo) → auto-push ONLY if the manifest
  opts in (so a read-only mirror isn't surprised by auto-pushes).
- no remote → never push (nothing to publish), but still commits locally.

**Marker.** Add a manifest field `publish:` controlling the push axis:
- absent → *derived*: gcrypt/shared → push; plain remote → no push; no remote → no push.
- `publish: on` → auto-push whenever a remote exists (the "private but published" opt-in).
- `publish: off` → never auto-push (commit-only, even with a remote).

This mirrors the existing `autosave:` absent→on→off vocabulary, so the two
orthogonal axes (commit / push) read consistently in `.brain/config.md`.

### Why this shape
- `ARCH-DRY` — one per-brain policy computed once, consumed by the commit loop,
  the push loop, and the pull/keys tick — rather than re-deriving "is this
  shared?" at each call site.
- `ARCH-PURE` — the policy derivation is a pure function of (manifest, remote
  url); the IO (git config read) is a thin shell over it, so it unit-tests
  without a daemon.
- Simplicity — the operator's mental model is two switches: "save my work" and
  "publish my work," each defaulting sensibly.

### Affected surface
- `lib/brain/manifest.go` — parse + accessor for the `publish:` field.
- `lib/brainsync/discovery.go` — watch ALL brains; expose a per-brain policy
  (commit / push / sync) instead of a single watchable bool.
- `lib/brainsync/watch.go` — gate the per-tick network block (pull/keys/auto-admit)
  and the RefWatcher startup-push on the push/sync policy; create AutoCommitters
  for all brains.
- `lib/brainsync/autocommit.go` — push half becomes a no-op when push is disabled
  (commit-only mode).
- `atlas/nous/autosave-and-checkpoint.md` — document the decoupled cadences + `publish:`.

## Plan

Durable design: `workshop/plans/000047-auto-push-for-private-but-published-brain-plan.md`

- [x] Manifest: add + parse `publish:` field; round-trip `autosave:`/`publish:` in `renderFrontmatter` (root-cause fix for the writer dropping them).
- [x] Policy: new pure `lib/brainsync/policy.go` (`RemoteKind`, `syncParticipant`, `publishMode`, `ComputePolicy`, `BrainPolicy.Active`) + table tests.
- [x] Discovery: watch all *active* brains (`FindSharedBrains`→`FindBrains`); rewrite tests.
- [x] Watch + AutoCommitter: consume policy — commit-all, gate push/pull/keys-admit; `NewAutoCommitter(push bool)`.
- [x] Atlas: document decoupled cadences + `publish:`; tests green; manual smoke.

## Log

### 2026-06-08
- 2026-06-08: closed — go test ./lib/brain/ ./lib/brainsync/ green (TestComputePolicy table, TestFindBrains, TestAutoCommitter_CommitOnlyNeverPushes, TestRewriteFrontmatter_PreservesAutosaveAndPublish); pre-existing gpg-agent integration failures are environmental. Manual daemon smoke: plain-remote publish:on brain committed + auto-pushed to bare origin; no-remote brain committed locally, never pushed; pull polling ran only for published brain; daemon logged commit+push vs commit-only per brain.; review verdict: SHIP

- Explored the brainsync daemon: `isWatchable` (discovery.go:73) couples
  commit+push into one "shared OR gcrypt-remote" predicate. AutoCommitter
  already does commit (5s) + push (60s); the per-tick loop does pull/keys/
  auto-admit. `SyncGcryptParticipantsFromManifest` and the keys/auto-admit
  steps already no-op gracefully on plain / keys-less remotes, so broadening
  the watch set is safe.
- Confirmed scope with operator: (Q1) plain mirrors excluded from auto-push by
  default → opt-in marker; (Q2) all brains get local autosave commit even with
  no remote.
- Operator refined `publish: off` semantics mid-plan: it pauses **only the push
  half**, pull keeps running (a gcrypt/shared brain with `publish: off` still
  receives peer changes). Split `Pull` from `Push` in `BrainPolicy` accordingly
  — `Pull = syncParticipant`, `Push = syncParticipant && publish != off`.
- Implemented: pure `ComputePolicy` (`ARCH-PURE`, table-tested without daemon),
  `remoteKind` reusing `brain.ReadOriginURL` (`ARCH-DRY`, deleted the bespoke
  `hasGcryptRemote` git exec). Watch consumes one policy per brain for
  commit/push/pull/keys-admit gating.
- Root-cause fix folded in: `renderFrontmatter` never emitted `autosave:`, so a
  recipient op (`Read→mutate→RewriteFrontmatter`) silently dropped it — and would
  drop `publish:` too. Now both round-trip; regression test added.
- Verified: `go test ./lib/brain/ ./lib/brainsync/` green (pre-existing gpg-agent
  integration failures are environmental, unrelated). Manual daemon smoke: a
  plain-remote `publish: on` brain committed + auto-pushed to a bare origin; a
  no-remote brain committed locally and never pushed; pull polling ran only for
  the published brain. Daemon log showed `(commit+push)` vs `(commit-only)` modes
  per brain.

