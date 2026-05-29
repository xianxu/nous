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
- [ ] Extract GitHub-creation logic (`gh repo create`, remote add,
      gcrypt-participants sync, first push) out of `brain_new.go` /
      `scripts/new-brain.sh` into a reusable `publish` function in
      `lib/brain/`.
- [ ] `nous brain new` does git init + manifest (single recipient,
      `sync_substrate: none`, no remote) + first commit only. No network.
- [ ] Tests: offline creation produces a valid local brain; manifest has
      no remote; `LoadStatus` reports `OriginURL == ""`, `HasUpstream ==
      false`.
- [ ] Verify: `nous brain new <path>` with network/GitHub auth absent
      succeeds; `nous brain list` shows it.

### M2 — `publish` primitive (local → private)
- [ ] `nous brain publish [--brain PATH]` CLI calling the extracted
      primitive: `gh repo create --private` → `git remote add origin
      gcrypt::…` → gcrypt-participants sync → push.
- [ ] Guard: refuse publish if a remote already exists (idempotency /
      no double-host).
- [ ] Tests: publish a local brain → remote set, push succeeds, status
      flips to `private`. Re-publish refused.
- [ ] Verify: round-trip — create local, publish, confirm GitHub repo
      holds ciphertext and `brain list` shows `private`.

### M3 — TUI ladder
- [ ] `list.go`: 3-rung label (`local` / `private` / `shared · N`).
- [ ] `detail.go`: state-gated action footer per rung; remove
      silently-blocked actions.
- [ ] `root.go`: `screenPublish` + `p` keybinding wired from `local`
      detail view; on success re-render as `private`.
- [ ] Copy fix: `no upstream configured` → `local only — lives on this
      device, no remote`.
- [ ] `IsOperator`: local-only brain → operator (`*`).
- [ ] Verify: drive the TUI through local → publish → private → invite →
      shared; each rung shows exactly its next gesture.

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
