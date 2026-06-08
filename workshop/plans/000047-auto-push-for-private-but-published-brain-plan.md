# auto-push for private-but-published brain — Implementation Plan

> **For agentic workers:** Consult AGENTS.md §3 (Subagent Strategy). This is
> single-pass, warm-context work on the brainsync daemon — implement in the main
> session, close in one `sdlc close` (one review boundary). Steps use checkbox
> (`- [ ]`) tracking.

**Goal:** Decouple the brainsync daemon's two cadences — *commit* (local
autosave) and *push* (publish to origin) — on a per-brain basis, so every brain
gets a local safety-net commit while auto-push stays opt-in for plain-remote
brains. (Issue #47.)

**Architecture:** Today `isWatchable` (`lib/brainsync/discovery.go:73`) collapses
"should we watch this brain?" into one bool (`shared OR gcrypt-remote`), and a
watched brain gets *both* commit and push. Replace that single bool with a pure
`BrainPolicy{Commit, Push, Pull, KeysAdmit}` derived from `(manifest, remoteKind)`.
The derivation is a **pure function** (`ARCH-PURE`); the only IO is classifying
the origin remote, reused from `brain.ReadOriginURL` (`ARCH-DRY`). One policy is
computed per brain and consumed by all three daemon behaviors — no re-deriving
"is this shared?" at each call site (`ARCH-DRY`).

**Tech stack:** Go. No new deps.

---

## Core concepts

### Worked matrix (`Pull` / `Push`)

| `publish:` | kind | shared | Pull | Push | note |
|-----------|------|--------|------|------|------|
| *absent* | none | – | ✗ | ✗ | local-only: commit only |
| *absent* | gcrypt | – | ✓ | ✓ | no regression |
| *absent* | plain | no | ✗ | ✗ | plain mirror: neither |
| *absent* | plain | yes | ✓ | ✓ | shared-over-plain stays watched |
| `on` | plain | no | ✓ | ✓ | **private-but-published opt-in** |
| `on` | none | – | ✗ | ✗ | nothing to sync to |
| `off` | gcrypt | – | ✓ | ✗ | **pull keeps running, push paused** |
| `off` | plain | yes | ✓ | ✗ | shared-over-plain, push paused |

### The two orthogonal axes

| Axis | Field | Default | Meaning |
|------|-------|---------|---------|
| Commit (autosave) | `autosave: on\|off` | on | run the 5s debounce local-commit loop |
| Push (publish) | `publish: on\|off` | *derived* | flush commits to origin on the 60s debounce |

These are independent switches in `.brain/config.md`. "Save my work" vs "publish
my work."

### `publish:` derivation (pure)

`shouldPush(manifest, remoteKind)`:

| `publish:` | no remote | gcrypt remote | plain remote |
|-----------|-----------|---------------|--------------|
| *(absent)* | ✗ | ✓ | ✓ only if `Shared()` (≥2 recipients) |
| `on` | ✗ (nothing to push) | ✓ | ✓ ← the "private but published" opt-in |
| `off` | ✗ | ✗ | ✗ |

No-regression check: a gcrypt or shared brain with no `publish:` field still
pushes (matches today's `isWatchable`). The only *new* push case is a
plain-remote brain that opts in with `publish: on`. The only *removed* case is a
plain-remote brain with ≥2 recipients but… that was already watched
(`m.Shared()`), and stays watched (`Shared()` branch above) — so no regression.

### `BrainPolicy` (pure, derived once per brain)

```go
type BrainPolicy struct {
    Commit    bool // autosave commit loop (local safety net)
    Push      bool // auto-push committed changes to origin
    Pull      bool // periodic fetch + ff-only from origin
    KeysAdmit bool // keys-sync + auto-admit — gcrypt/shared only (no keys branch elsewhere)
}
func (p BrainPolicy) Active() bool { return p.Commit || p.Push || p.Pull }
```

Derived via a `syncParticipant(m, kind)` predicate — "is this brain a
bidirectional sync target at all?":

```go
func syncParticipant(m, kind) bool {
    switch kind {
    case RemoteNone:   return false              // nothing to sync to
    case RemoteGcrypt: return true               // shared/encrypted: always a participant
    default:           return m.Shared() || publishMode(m) == "on" // plain: opt-in
    }
}
```

- `Commit    = m.AutosaveEnabled()`
- `Pull      = syncParticipant(m, kind)` — receive updates whenever we're a participant
- `Push      = syncParticipant(m, kind) && publishMode(m) != "off"` — **`publish: off` pauses only the push half; pull keeps running** (operator decision: a gcrypt/shared brain with `publish: off` still receives peer changes, it just stops auto-pushing).
- `KeysAdmit = syncParticipant(m, kind) && (kind == gcrypt || m.Shared())`

`Active()` replaces `isWatchable`: the daemon watches a brain iff its policy does
*something*. A fully opted-out brain (`autosave: off`, `publish: off` / no remote)
is not watched.

### Why watch ALL (active) brains now

Q2 (operator): a brain's work is unstructured; every brain should get a local
autosave commit even with no remote. So discovery no longer filters on
shared/gcrypt — it returns every brain whose policy is `Active()`. For a
no-remote brain that's just `{Commit:true}`: the commit loop runs, the push/pull
network paths are skipped entirely.

---

## Pure vs IO seam (`ARCH-PURE`)

| Piece | Kind | Location |
|-------|------|----------|
| `shouldPush`, `ComputePolicy`, `BrainPolicy.Active` | **pure** (manifest + kind → policy) | `lib/brainsync/policy.go` (new) |
| `remoteKind(brainRoot)` | thin IO (reads `brain.ReadOriginURL`) | `lib/brainsync/policy.go` |
| manifest `Publish` parse | pure string parse | `lib/brain/manifest.go` |

The policy table is unit-tested directly with no git/daemon (`ARCH-PURE`).

---

## Steps

### Manifest: the `publish:` field
- [ ] `lib/brain/manifest.go`: add `Publish string` to `Manifest` (doc: tri-state ""/on/off, consumed by brainsync `shouldPush`). Parse `publish:` in `parseManifest`.
- [ ] `lib/brain/write.go`: emit `autosave:` and `publish:` in `renderFrontmatter` when non-empty. **Root-cause fix:** today the writer drops both fields, so a recipient op (`Read → mutate → RewriteFrontmatter`) would silently lose a hand-added `publish: on` / `autosave: off`. Add a round-trip test.
- [ ] `lib/brain/manifest_test.go`: parse cases for `publish:` (on/off/absent). `lib/brain/write_test.go`: round-trip test (Read manifest with `publish: on` + `autosave: off` → RewriteFrontmatter → fields survive).

### Policy: the pure core
- [ ] New `lib/brainsync/policy.go`: `RemoteKind` enum (`RemoteNone`/`RemoteGcrypt`/`RemotePlain`), `remoteKind(brainRoot)` (uses `brain.ReadOriginURL`; "" → None, `gcrypt::` prefix → Gcrypt, else Plain), `shouldPush(m, kind)`, `ComputePolicy(m, kind) BrainPolicy`, `BrainPolicy.Active()`.
- [ ] New `lib/brainsync/policy_test.go`: table test over the `shouldPush` matrix + `ComputePolicy` (commit/push/pull/keysAdmit/Active) for every (publish × kind × shared × autosave) combination that matters.

### Discovery: watch all active brains
- [ ] `lib/brainsync/discovery.go`: delete `isWatchable`/`hasGcryptRemote`; rename `FindSharedBrains` → `FindBrains` and `FindAllSharedBrainsInWorkspace` → `FindAllBrainsInWorkspace`, including a brain iff `ComputePolicy(m, remoteKind(p)).Active()`. Update the package doc (no longer "the Shared test").
- [ ] `lib/brainsync/run.go`: update the one `FindAllSharedBrainsInWorkspace` call site + comments ("shared brain" → "brain").
- [ ] `lib/brainsync/discovery_test.go`: rewrite for the new semantics — a single-recipient no-remote brain and a plain-remote brain are now *included* (active via Commit); a fully opted-out brain (`autosave: off` + no remote) is *excluded*. Drop the obsolete `TestFindSharedBrains_SingleRecipientWithGcryptRemote` plain-mirror-excluded assertion (that decision is now in `policy_test.go`).

### Watch + AutoCommitter: consume the policy
- [ ] `lib/brainsync/watch.go`: compute `policies map[string]BrainPolicy` once at `Watch` start. Create an `AutoCommitter` iff `policy.Commit`, passing `policy.Push`. Gate the startup-push and the RefWatcher-event direct-push (the no-AutoCommitter branch) on `policy.Push`. In the ticker loop: skip the whole network block unless `policy.Pull`; run `syncBrainPubkeys`/`autoAdmitBrain` only when `policy.KeysAdmit`.
- [ ] `lib/brainsync/autocommit.go`: add a `push bool` field + `NewAutoCommitter(..., push bool, ...)` param. `armPush()` becomes a no-op when `!push` (so the push timer never arms; commits still flow). Update the startup log to name commit-only mode.
- [ ] Update any `NewAutoCommitter` test callers for the new signature; add a test that a commit-only committer (`push:false`) commits but never pushes (assert no `origin/main` advance / no push attempt against a bare-repo remote).

### Atlas + verification
- [ ] `atlas/nous/autosave-and-checkpoint.md`: document the decoupled axes, the `publish:` field + derivation table, and that all (active) brains are watched. Update `atlas/index.md` if the entry's hook changed.
- [ ] `go test ./lib/brain/... ./lib/brainsync/...` green. `go vet ./...`.
- [ ] Manual smoke (or scripted): a temp brain with a plain bare-repo remote + `publish: on` auto-commits and auto-pushes; the same brain without the field commits but does NOT push; a no-remote brain commits locally.

---

## Risks / notes
- **Manifest edits need a daemon restart to take effect** for a brain already in the watched set (policy is read once at `Watch` start — same as today's `autosave` read). Acceptable; note it in the atlas. (Discovery reconcile adds/removes brains but doesn't restart a live brain's `Watch`.)
- **`SyncGcryptParticipantsFromManifest` on a plain remote** sets a `gcrypt-participants` git-config key the plain remote ignores — inert, no harm. Left as-is (out of scope; could guard on gcrypt later).
- **`publish: off` pauses push but not pull**: a gcrypt/shared brain with `publish: off` keeps receiving peer changes (Pull stays on) and only stops auto-pushing. Documented in the atlas.
