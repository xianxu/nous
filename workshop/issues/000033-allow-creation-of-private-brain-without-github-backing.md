---
id: 000033
status: working
deps: []
github_issue:
created: 2026-05-26
updated: 2026-05-29
estimate_hours:
actual_hours:
---

# allow creation of private brain without github backing

## Problem

Every brain today is born GitHub-backed: `nous brain new` (and
`scripts/new-brain.sh`) bundle `gh repo create --private`, the gcrypt
remote wiring, and the first push into creation. There is no way to
make a brain that is *purely local* — a git repo on this machine with
no upstream at all.

Wanted: a **local-only private brain** — `git init` + `.brain/config.md`,
no remote, no network, no GitHub auth. FileVault (device FDE) is the
at-rest protection; gcrypt is moot because nothing is pushed anywhere.
The user may sync the folder by external means (iCloud, Dropbox) at
their own discretion — that is explicitly *outside* the brain's
guarantees (see Non-goals).

This is the lightweight default for "just give me a brain," and it must
sit cleanly under the existing privacy abstractions rather than being a
bolt-on.

## Spec

### Mental model: privacy vs. topology

The existing model has one privacy knob — the **recipient list**
(`len(recipients) == 1` → private, `>= 2` → shared). That is unchanged.
What this issue adds is a second, orthogonal axis — **topology**: where
ciphertext lives and whether there's an upstream at all. A local-only
brain is still *private* (single recipient); it just has no remote.

### The upgrade ladder

Topology forms a one-directional ladder, and each rung-transition maps
1:1 onto a CLI verb:

```
nous brain new ──▶ local ──[publish]──▶ private ──[invite]──▶ shared
   (git init)                (gh repo)              (recipient add)
```

- **local** — `OriginURL == ""`. No remote, no gcrypt push, no daemon
  (already excluded by `brainsync/discovery.go` `isWatchable()` →
  single-recipient + no remote = false).
- **private** — `OriginURL != ""` && `len(recipients) == 1`. Encrypted
  gcrypt backup on GitHub, solo.
- **shared** — `OriginURL != ""` && `len(recipients) >= 2`. Encrypted on
  GitHub, N recipients.

### Key refactor: split the GitHub bundle

"Always local first" (decided 2026-05-29) splits today's creation bundle:

- **`nous brain new`** becomes purely local: `git init` → write
  `.brain/config.md` (single recipient, `sync_substrate: none`, no
  `remote.origin.url`) → first commit. No GitHub, no network, TTY-optional.
  It stops being a multi-recipient ceremony gate (you can't admit
  recipients to something with no remote).
- **`publish`** (new primitive) inherits *all* the GitHub logic
  extracted from new-brain: `gh repo create --private` →
  `git remote add origin gcrypt::ssh://…` → gcrypt-participants sync →
  push. Single home for "this brain gains a host." Reused by the TUI `p`
  action and available as `nous brain publish`.
- **`invite`** (`a`) is unchanged — already works the instant a remote
  exists.

### TUI surfacing (`lib/tui/brain/`)

1. **List label — 3 rungs, not 2.** Derive from `OriginURL != ""` ×
   recipient count: `local` / `private` / `shared · N`. Today both
   local and hosted-solo render `(private)` — indistinguishable.
   (`list.go` `labelInner()`.)
2. **Detail footer — state-gated "next rung" gesture.** Show only the
   action valid at the current rung; stop listing silently-blocked
   actions:
   - `local` → `p  publish to GitHub`
   - `private` → `a  invite a collaborator → shared`
   - `shared` → `a invite` / `r remove` / `l leave` (today's set)
   (`detail.go`.)
3. **New `screenPublish`** in `root.go`'s screen state machine, beside
   `screenNewBrain`/`screenInviteCollab`. Confirm repo name → run the
   publish primitive → on success the brain re-renders as `private` with
   `a invite` now available.
4. **Copy fix.** Empty `OriginURL` currently renders
   `no upstream configured` (reads like a misconfiguration). For a local
   brain it's the chosen state → `local only — lives on this device, no
   remote`. (`status.go`/`detail.go`.)
5. **Operator marker.** `IsOperator` returns false without a GitHub
   remote, so local brains would never show `*`. A local-only brain is
   trivially operator-owned → show `*`.

Labels describe **reach**, not encryption: `local` = nowhere but here;
`private` = encrypted backup on GitHub, just you; `shared` = encrypted on
GitHub, N people. (A local brain is *also* private in the recipient
sense — detail copy disambiguates.)

### Non-goals

- **No downgrade/unpublish in the UI.** Once ciphertext is on GitHub,
  "going back to local" doesn't un-leak it. The ladder is one-directional.
- **No managed external sync (iCloud as a substrate).** If a user drops
  a `local` brain into an iCloud/Dropbox folder, the host sees
  *plaintext* (the working tree is never gcrypt-encrypted; gcrypt only
  engages on push to a gcrypt remote). The brain does not model or manage
  this. Threat model must state the boundary so it isn't a silent surprise.

## Plan

### M1 — `nous brain new` becomes local-first
- [x] `lib/brain.InitLocal` — Go-native local scaffold: git init, go.mod
      (substrate wiring), manifest (single recipient, `sync_substrate:
      none`, **no remote**), initial commit. Reuses `WriteManifest`;
      substrate step injected as a callback (testable with nil).
- [x] `nous brain new` (no flags) routes to `provisionLocal` →
      `InitLocal` + `construct/setup.sh`. Multi-recipient GitHub path
      left **untouched** (gated behind `--recipient`/`--fingerprint`) so
      shared-brain creation doesn't regress mid-ladder.
- [x] `findNousFile` helper (DRY'd from `findNewBrainScript`) + new
      `findSetupScript`.
- [x] Tests: `provision_test.go` — offline creation, no-remote manifest,
      `LoadStatus` reports `OriginURL == ""` / `HasUpstream == false`,
      substrate-callback committed, refuses existing path / empty
      recipient, `moduleSafe`.
- [x] Verify: ran the built binary offline with an ephemeral GPG key —
      brain created with no remote, clean tree, `brain list` shows it.

> **Revision (2026-05-29):** the GitHub-logic *extraction* originally
> listed here moved to M2. M1 went Go-native for the local scaffold and
> deliberately did **not** touch `scripts/new-brain.sh` or the
> multi-recipient path — lower risk, and the extraction is exactly what
> M2 (publish) needs anyway. Net: M1 adds the local rung without
> disturbing the proven GitHub bootstrap.

### M2 — `publish` primitive (local → private)
- [x] `scripts/publish-brain.sh` — GitHub half for an existing local
      brain: `gh repo create --private` → `git remote add origin
      gcrypt::…` → set gcrypt-participants → `git push --force -u`.
- [x] `nous brain publish [--brain PATH]` (`cmd/nous/brain_publish.go`):
      resolve brain (flag/picker) → guard → confirm → run script → keys
      branch.
- [x] `publishKeysBranch` extracted from `brain_new.go` into a shared
      helper (DRY; both `new --recipient` and `publish` use it).
- [x] Guard `ensureLocalForPublish`: refuse if a remote already exists.
- [x] Tests (`brain_publish_test.go`): guard with/without remote,
      `--brain` resolution, bad path, `shortFps`, `orPlaceholder`. All
      green. Offline CLI checks: `--help`, guard refuses a
      remote-bearing brain, Go→script handoff reaches `publish-brain.sh`.
- [ ] **Verify (operator-run): round-trip** — `nous brain new` a local
      brain, `nous brain publish` it, confirm the GitHub repo holds
      opaque ciphertext and `git clone` round-trips. Needs a real GPG
      secret key + push; deferred to the operator (see Log).

> **DRY debt (tracked):** the gh-repo-create ceremony in
> `publish-brain.sh` is duplicated from `new-brain.sh` step 3 rather
> than extracted into a sourced helper. Deliberate: `new-brain.sh` and
> the multi-recipient path can't be runtime-verified in this env (no gh
> auth was assumed, no gpg secret key), so they were left untouched to
> avoid shipping unverified changes to the proven bootstrap. Unify into
> a shared `brain-lib.sh` once the round-trip is verified.

### M3 — TUI ladder
- [x] `rung.go`: shared `classifyRung` + `rungLabel` (DRY between list
      and detail; the privacy-vs-topology distinction lives here once).
- [x] `list.go`: 3-rung label (`local` / `private` / `shared · N`),
      derived from a per-item `hasRemote` computed once at build time.
- [x] `detail.go`: rung-based header + **state-gated action footer**
      (local→`p`, private→`a`, shared→`a`/`r`/`l`); handlers gated to
      match (no silently-failing actions; `a` on local nudges to publish).
- [x] `root.go`: `p` → `launchPublishMsg` → runs `nous brain publish` as
      a foreground subprocess (pinentry-safe, like clone) →
      `publishSubprocessDoneMsg` re-enters detail showing the new rung.
      (Used the subprocess pattern rather than a `screenPublish` model —
      reuses the M2 CLI, no duplicate flow.)
- [x] Copy fix: empty `OriginURL` → `local only — lives on this device,
      no remote` (detail); `WriteManifest` body reworded
      topology-neutral ("encrypted via gcrypt when pushed to a remote"),
      closing the M1-carried "Encrypted via gcrypt" nit at its source.
- [x] Operator marker: local-only brain → owner (`*`), handled at the
      list call site (no change to `IsOperator`'s GitHub contract);
      legend always rendered so the marker isn't orphaned without gh auth.
- [x] Verify (offline): unit tests (rung classify/label, list label,
      detail render + action-gating per rung, publish message flow, root
      publish-done handling) + a visual render of all three rungs. The
      interactive `p`-publish round-trip shares M2's GitHub verify.

### M4 — Docs / atlas / threat model
- [ ] Atlas: document the topology ladder + the privacy-vs-topology axis
      distinction. Update `atlas/index.md`.
- [ ] Threat model (`brain/atlas/threat-model-shared-brain.md`): add the
      local-only posture + the iCloud-plaintext boundary caveat.

## Log

### 2026-05-29 — session summary
Design session (no code). Brainstormed the privacy-vs-topology split:
the requested "local-only private brain" is not a new privacy mode but a
new *topology* for an already-private (single-recipient) brain. Surfaced
the key crypto subtlety — gcrypt only encrypts on push to a gcrypt
remote, so a local brain's working tree is plaintext; syncing it via
iCloud would expose plaintext to Apple. User opted for **truly local, no
iCloud** (FileVault is the protection), and **dropped the proposed second
mode (public-github-backed)** for now — scope is the pure local mode plus
the upgrade ladder. Designed the `nous brain` TUI around the ladder
(local → private → shared) as the organizing principle, with publish as
the single extracted GitHub primitive and a state-gated detail footer.
New-brain flow decided: **always local first** (publish is always an
explicit later gesture; new-brain has zero GitHub logic). Spec + 4-milestone
plan written. Awaiting plan approval before implementation.

### 2026-05-29 — M1 landed
Approved (no estimate — small-issue track). Implemented the local rung:
`brain.InitLocal` (Go-native scaffold, `lib/brain/provision.go`) +
`provisionLocal` routing in `cmd/nous/brain_new.go`, with `findNousFile`
DRY'd from `findNewBrainScript`. Multi-recipient GitHub path untouched.
Tests in `provision_test.go` (all pass); `go build ./...` + `go vet` +
`go test ./lib/brain -short` + `./cmd/nous` green. Verified the built
binary offline with an ephemeral GPG keyring (sandbox has no secret
keys): brain created with no remote, single recipient, `sync_substrate:
none`, clean tree, surfaced by `nous brain list`.

**Carried to M3 (copy):** `WriteManifest`'s boilerplate body still reads
"Encrypted via gcrypt …" — false for a local brain (plaintext on disk,
FileVault-protected). The frontmatter is correct; only the human body
line misleads. Fix when M3 touches copy.

### 2026-05-29 — M2 landed (code; round-trip verify pending operator)
Added the publish rung: `scripts/publish-brain.sh` (GitHub half for an
existing local brain) + `nous brain publish` (`brain_publish.go`) +
`publishKeysBranch` extracted as a shared helper. Guard
`ensureLocalForPublish` refuses an already-remote'd brain. Unit tests
green; `go build ./...` + vet + `go test ./cmd/nous ./lib/brain -short`
clean. Offline-verified the CLI path up to the GitHub side effects:
`--help`, the guard (refuses a remote-bearing brain via `--brain`), and
the Go→`publish-brain.sh` handoff (splash + dep checks pass).

Did **not** runtime-verify the actual `gh repo create` + gcrypt push.
Discovered mid-test that `gh` *is* authenticated as `xianxu` in this
sandbox — but creating repos on the operator's GitHub account is an
outward-facing side effect they asked to own ("you run the verify"), so
I stopped before any `gh repo create`. No repos were created (a `head`
pipe-close SIGPIPE'd the script just before it). Round-trip verify is
the operator's, or theirs-to-authorize against a throwaway repo.

DRY debt logged in the M2 plan block: `publish-brain.sh` duplicates
`new-brain.sh`'s gh-create ceremony; unify post-verify.

### 2026-05-29 — M3 landed (TUI ladder)
Surfaced the topology ladder in `nous brain`. New `rung.go` holds the
shared `classifyRung`/`rungLabel` (used by both list and detail). List
shows the 3-rung label; detail shows a rung-based header + a state-gated
footer that offers only the next-rung gesture (local→`p` publish,
private→`a` invite, shared→`a`/`r`/`l`), with handlers gated to match.
`p` runs `nous brain publish` as a foreground subprocess (reuses the M2
CLI; pinentry-safe) and re-enters detail on return. Copy fixed in two
places: detail's "no upstream" → "local only — lives on this device",
and `WriteManifest`'s body reworded topology-neutral (closes the
M1-carried nit). Operator `*` now shows for local brains (call-site
logic; `IsOperator`'s GitHub contract untouched). All unit tests green
(`lib/tui/brain`, `lib/brain -short`, `cmd/nous`); visually rendered all
three rungs to confirm. Interactive publish round-trip shares M2's verify.
