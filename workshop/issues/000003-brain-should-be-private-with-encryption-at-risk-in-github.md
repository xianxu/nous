---
id: 000003
status: working
deps: [nous#8, ariadne#22]
created: 2026-05-05
updated: 2026-05-06
estimate_hours: 6
---

# brain should be private with encryption at rest in github

## Done when

- A **new** `brain-private` repo on GitHub runs against a `git-remote-gcrypt` remote with the user's GPG public key as the (single-element) recipient list; pushed objects are opaque.
- New repo's on-disk checkout at `~/workspace/brain-private` mirrors the operational content of the existing `~/workspace/brain` checkout. Content stays in sync until cutover (M3).
- Existing `~/workspace/brain` and the existing GitHub `brain` repo are **untouched** through M1 and M2 — they remain the safety net. If anything goes sideways, abandoning `brain-private` costs nothing material.
- `brain-private` holds the cross-cutting state — projects, roadmaps, life data, paired-device trust list, recipient public keys for known brains. It does **not** hold private key material (GPG private key lives at `~/.gnupg/` per the threat-model single-GPG-scheme posture; bootstrap is via iCloud Keychain, not via a brain-private export).
- New-machine bootstrap procedure verified end-to-end on a second machine: import GPG private key from iCloud Keychain → register passphrase in macOS Keychain → clone `brain-private` (gpg-agent decrypts transparently).
- Cutover landed: agent path conventions and tool references switched from `~/workspace/brain` to `~/workspace/brain-private`; the old `brain` checkout and GitHub repo are archived.
- Atlas entry documents the encryption posture and bootstrap procedure so future agents and humans find it.

## Spec

Sources: `brain/data/life/42shots/ideas/2026-04-28-01-pensive-collaborative-brain.md` §Privacy and bootstrap, §Build order step 1; threat model at `brain/atlas/threat-model-shared-brain.md` (authored under `nous#8` M1, this is the canonical reference for the encryption posture this issue implements).

**Single-GPG-scheme posture:** all brains — private and shared — are gcrypt-encrypted to a GPG recipient list. brain-private has a single recipient (the user). There is no separate gcrypt symmetric passphrase; the daily-use unlock chain is uniform across machines (gpg-agent + pinentry-mac → Keychain). New-machine bootstrap pulls the GPG private key from iCloud Keychain (recommended default per the threat model). Apple-account hardening (`brain#10`) is end-of-project follow-on, not gating — the GPG-key blob in iCloud Keychain is itself passphrase-encrypted, so an Apple-ID-account compromise yields ciphertext, not a usable key. Hardening upgrades the channel from "good enough for personal MVP" to "production-grade for shared-brain at family or team scale."

**Safety-net migration shape:** rather than adding a gcrypt remote to the existing `brain` repo and gradually retiring the unencrypted one (which puts the live repo at risk during setup), the migration provisions a *new* `brain-private` repo on GitHub and on disk, mirrors the operational content into it, and verifies end-to-end on a second machine before any cutover. The existing `brain` checkout and remote remain fully intact and operational throughout. Worst-case rollback is "abandon the new repo, keep using the old one" — costs nothing, breaks nothing. Cutover (M3) is the single point at which agent paths switch over; everything before that is non-destructive.

Brain holds the rawest private capture in the system. Today it lives in a private GitHub repo, but the host still sees plaintext — filenames, commit messages, the commit graph, every typed artifact. That's the wrong default for data this sensitive, and it's the prerequisite for sharing: any `brain-shared-*` repo will be encrypted to a recipient list, so the host has to be untrusted by construction. Solo-mode benefit (encryption at rest with a passphrase) is immediate; collaborative-mode benefit (each shared brain encrypted to a different recipient set) compounds on top.

The load-bearing simplification from the pensive: **brain-private is the keyring**. One symmetric passphrase unlocks brain-private. Brain-private holds the GPG private key. The GPG key unlocks every `brain-shared-*` repo (each gcrypt'd to a recipient list that includes my GPG public key). So the only secret a human has to memorize is the brain-private passphrase. New machine = clone brain-private with passphrase → import keys → clone everything else.

Tool choice: `git-remote-gcrypt` for whole-remote opacity. `git-crypt` reserved for the unusual case where some files in a repo are public and others private — not the default.

Single-passphrase SPOF is a known and accepted risk. Mitigation: long passphrase, full-disk encryption on the laptop, frequent lock-on-sleep. Brain-private is more sensitive than any brain-shared (it's the keyring + the rawest capture), which is the right way around — sharing is a sensitivity downgrade, not an upgrade.

## Estimate

Range: **3.5–10 hr**. Best guess: **~5.5 hr**.

*Produced via `brain/data/life/42shots/velocity/estimate-logic-v2.1.md` against `baseline-v2.1.md`. Method A only.*

| Component | Primitive | Design (×0.5) | Impl (×1.5 familiarity) | Total |
|---|---|---|---|---|
| GPG/gcrypt tooling install + keypair gen + gpg-agent config (M1 step 0) | Real-tool discovery + setup | 0.1–0.3 | 0.45–1.2 | 0.55–1.5 |
| New repo provisioning + GPG-recipient gcrypt config + initial mirror (M1 step 1) | Real-tool discovery + setup | 0.15–0.5 | 0.45–1.2 | 0.6–1.7 |
| Mirror sync helper (M1.5) | Smaller Go / shell | 0–0.15 | 0.3–0.75 | 0.3–0.9 |
| Paired-device + recipient layout (M2) | Atlas/docs + small scripting | 0.05–0.2 | 0.15–0.45 | 0.2–0.65 |
| Bootstrap procedure doc (M3) | Atlas/docs | 0.05–0.2 | 0.1–0.4 | 0.15–0.6 |
| Atlas / threat-model link maintenance | Atlas/docs | 0.05–0.2 | 0.1–0.3 | 0.15–0.5 |
| gcrypt + GPG tooling discovery | Real-API discovery | 0 | 0.45–0.9 | 0.45–0.9 |
| Second-machine dry-run + iterate (M3) | Verification + fix-loop | 0.25–0.75 | 0.45–1.5 | 0.7–2.25 |
| Backup creation + rename-and-cutover (M3 step 3b–3d) | Cross-cutting + verification | 0.15–0.4 | 0.3–0.8 | 0.45–1.2 |
| Verification window dogfood (M3 step 3e) | Verification (mostly wall-clock) | 0–0.1 | 0.1–0.3 | 0.1–0.4 |
| Mid-flight scope buffer | Mid-flight pivot | 0.1–0.25 | 0.3–0.75 | 0.4–1 |
| **Subtotal** | | 0.95–3.05 | 3.2–8.65 | 4.15–11.7 |
| **+30% design buffer** | | +0.3–0.9 | n/a | +0.3–0.9 |
| **Total** | | | | **~4.5–12.6 hr** |

Rounded to **4–12 hr**. Bumped slightly from the prior 3.5–10 hr after discovering the GPG/gcrypt tooling is not yet installed on this machine — Step 0 (install gnupg + pinentry-mac + git-remote-gcrypt; generate keypair; configure gpg-agent; verify Keychain entry) adds ~1 hr at the midpoint. Still well under the dual-scheme version's 4–12 hr range despite this, because the single-GPG-scheme savings (no wrapper script, no GPG export round-trip, no init script for restoring keys from exports) offset Step 0's cost.

Familiarity ×1.5 applied to impl (gcrypt + GPG-recipient flow is novel-but-bounded). Spec-quality discount ×0.5. The biggest residual uncertainty remains the second-machine dry-run, which now also depends on the iCloud Keychain bootstrap path being well-trodden (which it isn't yet — first time anyone in this stack does it).

## Plan

### M1 — provision new gcrypt'd `brain-private` repo and mirror content

Throughout M1, the existing `~/workspace/brain` and the existing GitHub `brain` repo stay untouched. Operationally we keep working in the old one; the new one is a parallel target being filled.

**Prereq state on this machine (verified 2026-05-06):** none of the GPG/gcrypt tooling is set up yet. `~/.gnupg/` has only the empty keybox auto-created by `gpg --list-secret-keys`. No `gpg-agent.conf`. No `git-remote-gcrypt` binary. No `GnuPG` Keychain entry. The prereq-establishment work is therefore the **first phase of M1**, not a precondition. Folded in below as steps 0a–0d.

#### Step 0 — establish GPG / gcrypt tooling

Generalized as a reusable `make identity` target in `nous` (script: `scripts/identity.sh`, target: `Makefile.nous`). Idempotent: re-running detects an existing key and skips generation. Any user, any fresh machine: clone nous, `make identity`, done.

- [ ] **0a — `make identity`** in `nous`. The script (`scripts/identity.sh`):
  - Installs `gnupg`, `pinentry-mac`, `git-remote-gcrypt` via Homebrew if missing.
  - Configures `~/.gnupg/gpg-agent.conf` with `pinentry-program /opt/homebrew/bin/pinentry-mac` (so passphrase prompts route through the Keychain-saving pinentry-mac dialog).
  - Generates a fresh GPG keypair (`gpg --quick-generate-key 'Name <email> (brain encryption key)' rsa4096 default 5y`) if no secret key exists.
  - Pre-fills name / email from `git config --global user.name|email` unless `IDENTITY_NAME` / `IDENTITY_EMAIL` env vars are set.
  - Skips key generation idempotently if a secret key already exists.
  - Prints the resulting fingerprint and "next steps" pointer at the end.
- [ ] **0b — first decrypt to populate Keychain.** First decrypt operation against any gcrypt'd content triggers pinentry-mac with the "Save in Keychain" checkbox. Tick it; passphrase is then in macOS login Keychain for non-interactive subsequent uses. Test artifact: `echo test | gpg --encrypt --recipient <fingerprint> | gpg --decrypt`.
- [ ] **0c — verify Keychain entry.** `security find-generic-password -s "GnuPG" -g` should now return a result. This is the entry charon will fetch in `charon#21`.

#### Step 1 — provision the new repo

- [ ] **Document the gcrypt setup procedure** in `nous/atlas/` (recipient-list form, remote URL form, push/clone semantics, what GitHub sees, how to add a new recipient later). Land before any provisioning so the procedure is the source of truth.
- [ ] **Identify the GPG public-key fingerprint** to use as recipient. Confirm with `gpg --list-keys --keyid-format LONG`.
- [ ] **Create new private GitHub repo** `brain-private`. Empty.
- [ ] **Initialize new local checkout** at `~/workspace/brain-private`. `git init` + configure a gcrypt remote with `git config remote.origin.gcrypt-participants <fingerprint>` so gcrypt encrypts to the GPG recipient list (single recipient = the user).
- [ ] **Author `.brain/config.md`** at the new repo's root per the constitutional convention (ariadne `AGENTS.md` §1): `mode: private`, `name: personal`, `recipients: [<your-fingerprint>]`, `sync_substrate: none`.
- [ ] **Mirror content.** Copy operational content from `~/workspace/brain/` to `~/workspace/brain-private/` (excluding `.git/`, transient build artifacts, anything in a migration ignore-list). Initial commit. Push to gcrypt remote — gcrypt invokes gpg-agent for the encryption operation; pinentry-mac unlocks the key from Keychain on first use.
- [ ] **Verify opacity.** Open the new GitHub repo in a browser; confirm contents are opaque (no readable filenames, paths, commit graph).
- [ ] **Verify continued operation of the existing `brain`.** Run a no-op edit + commit + push on the original `~/workspace/brain` to confirm it's still fully functional. Old repo is the safety net; we keep verifying it works.

### M1.5 — keep `brain-private` in sync with operational `brain` during migration window

Until cutover (M3), changes happen in the operational `brain`. We keep `brain-private` current so cutover is a swap rather than a re-migration.

- [ ] **Sync helper.** Tiny script (e.g., `scripts/brain-mirror.sh`) that re-runs the file copy + commits + pushes to gcrypt. Idempotent. Runs on demand.
- [ ] **Cadence:** run after every meaningful operational `brain` push, until cutover. Manual is fine; the migration window is short.

### M2 — paired-device + recipient layout in `brain-private`

Under the single-GPG-scheme posture, `brain-private` does **not** hold private key material. The GPG private key stays at `~/.gnupg/` on each device; bootstrap arrives via iCloud Keychain (M3). What `brain-private` does hold is the cross-cutting trust metadata that survives across machines.

- [ ] Decide layout — proposed: `keys/paired-devices.md` (list of devices admitted to brain-private + brain-shared-\*'s; freeform markdown), `keys/recipients/<brain-name>/<fingerprint>.asc` (public keys of known recipients per shared brain, for use when adding them to a new shared brain's recipient list).
- [ ] Author the initial `paired-devices.md` listing the current laptop + any other Apple devices the user uses with iCloud.
- [ ] If shared brains exist yet: place recipient public keys under `keys/recipients/<brain-name>/`. (Likely empty at MVP — first shared brain provisioning happens in `nous#4` M4.)
- [ ] Verify operational keys at `~/.gnupg/` etc. still work; nothing should be touched here.
- [ ] Document the layout in the threat-model doc; link from atlas.

### M3 — bootstrap dry-run, then rename-and-cutover

`brain#10` Apple-account hardening is end-of-project, not a prereq for M3. The iCloud-Keychain bootstrap channel is workable for personal MVP without it because the GPG-key blob stored in iCloud Keychain is itself passphrase-encrypted — an Apple-ID-account compromise yields ciphertext, not a usable key. Hardening upgrades the channel later; M3 can proceed.

The cutover shape preserves on-disk and on-GitHub paths: the encrypted repo ends up at `xianxu/brain` and `~/workspace/brain` exactly where the legacy plaintext lived. No path-reference hunt across project files; no agent re-onboarding. The cost is a deliberate rename + double-backup dance during cutover.

#### Step 3a — bootstrap dry-run on a second machine

- [ ] **Write the bootstrap procedure** as a checklist in `nous/atlas/gcrypt-brain-encryption.md` (or a sibling). The procedure: install gpg + gcrypt + git; sign into iCloud + open Keychain Access; find the `[brain GPG]` secure note; copy the ASCII-armored block; `gpg --import`; configure pinentry-mac in `~/.gnupg/gpg-agent.conf`; trigger first decrypt to populate Keychain via "Save in Keychain"; clone `brain-private`.
- [ ] **Dry-run on a second machine** (VM or actual second laptop). Run through the procedure cold. Verify: (a) `gpg --list-secret-keys` shows the imported key; (b) `gpg --decrypt` of a small test ciphertext succeeds; (c) `git clone gcrypt::...brain-private` succeeds and decrypts cleanly.
- [ ] **Fix whatever doesn't work** the first time; iterate until repeatable. Keep the original `brain` operational — we are not committed to cutover until the dry-run is clean.

#### Step 3b — backup the legacy `brain` (two channels)

- [ ] **Cloud backup.** Create `xianxu/brain-backup` as a private GitHub repo. Push the entire current `brain` history there (`git push --mirror git@github.com:xianxu/brain-backup.git` from `~/workspace/brain`). Durable cloud backup; survives local-disk failures.
- [ ] **Local backup.** `cp -R ~/workspace/brain ~/workspace/brain.legacy`. Survives GitHub-account incidents.
- [ ] Verify both: clone `brain-backup` into a scratch dir to confirm the cloud copy is complete; `ls ~/workspace/brain.legacy/` to confirm the local copy.

#### Step 3c — final mirror sync

- [ ] **Final mirror sync** of operational content from `~/workspace/brain` into `~/workspace/brain-private` via the M1.5 mirror helper. Immediately before the rename. Confirms the encrypted target is current.

#### Step 3d — rename-and-cutover

The destructive sequence. After `brain-backup` (cloud) and `brain.legacy` (local) are confirmed in step 3b, this is recoverable.

- [ ] `gh repo delete xianxu/brain --confirm` — destructive. Cloud safety net is `brain-backup`.
- [ ] `gh repo rename brain --repo xianxu/brain-private` — renames brain-private → brain on GitHub.
- [ ] `mv ~/workspace/brain ~/workspace/brain.legacy.original` (only if local brain wasn't already moved aside in 3b's `cp` — it wasn't, because 3b uses `cp` not `mv`. So this step renames the now-orphaned local plaintext brain checkout out of the way before reusing the path).
- [ ] `mv ~/workspace/brain-private ~/workspace/brain` — local rename, paths now match the new GitHub state.
- [ ] `cd ~/workspace/brain && git remote set-url origin gcrypt::ssh://git@github.com/xianxu/brain.git` — update remote URL to match the rename. (GitHub's rename redirects work for HTTPS but are flakier for SSH; explicit set-url is safer.)
- [ ] `git fetch && git pull` to confirm the renamed remote works end-to-end.

#### Step 3e — verification window

Before any cleanup, **operate in the new `~/workspace/brain` for at least 1 week.** Verify:

- [ ] Multiple push/pull cycles succeed.
- [ ] `gh browse` (or browser) of `xianxu/brain` shows opaque contents.
- [ ] An agent-driven workflow edits, commits, pushes; second machine pulls and reads. End-to-end.
- [ ] Project file references at `/Users/xianxu/workspace/brain/data/project/...` still resolve in any tool that reads them.

If any break, restore is `mv` operations only — no re-encryption needed.

#### Step 3f — cleanup

After at least **1 week of clean operation** for local, **1 month** for cloud:

- [ ] Local: `rm -rf ~/workspace/brain.legacy ~/workspace/brain.legacy.original` (after 1 week).
- [ ] Cloud: `gh repo delete xianxu/brain-backup --confirm` (after 1 month).

Cleanup landing date should be tracked in the schedule datatype (`ariadne#23`) once it ships, so the 1-month deadline doesn't get forgotten. Until then, manually note the date.

## Log

### 2026-05-05

- Issue spec'd from `brain/data/life/42shots/ideas/2026-04-28-01-pensive-collaborative-brain.md`. Originally a stub; now scoped as M1 of the shared-brain project.
- **Reshape to safety-net migration:** original plan added a gcrypt remote to the existing `brain` repo, planning to retire the unencrypted remote after second-machine verification. Reshaped to provision a *new* `brain-private` repo, mirror operational content into it, verify end-to-end on a second machine, then cut over via path-reference swap. Existing `brain` checkout and remote stay fully intact through M1, M1.5, M2 — they remain the source of truth until cutover lands in M3. Worst-case rollback is "abandon the new repo, keep using the old one" rather than "untangle a half-migrated remote." Estimate bumped 3–9 hr → 4–12 hr (best guess 5 → 7) for the new-repo provisioning + mirror script + cutover hunt; well worth the rollback safety.

### 2026-05-06

- **Single-GPG-scheme reshape.** Adopted from threat-model `## Revisions` 2026-05-06: brain-private now uses gcrypt with a single-recipient GPG list (the user) instead of a symmetric passphrase. Concrete impact on this issue:
  - M1 drops the passphrase wrapper script entirely. gcrypt is configured with `gcrypt-participants <fingerprint>`; gpg-agent + pinentry-mac handle unlock per-machine.
  - M2 reframed: brain-private no longer holds key exports. Layout simplifies to paired-device list + recipient public keys for known shared brains.
  - M3 bootstrap simplifies: no decrypt-then-import dance. New machine pulls GPG private key from iCloud Keychain (recommended channel per threat model), imports, registers passphrase in Keychain, clones brain-private.
  - Estimate dropped 4–12 hr → 3.5–10 hr (best guess 7 → 5). Wrapper script removed (~0.6 hr saved), M2 simplified (~0.9 hr saved).
- **`brain#10` repositioned** as end-of-project hardening, not gating. The iCloud-Keychain bootstrap channel is workable for personal MVP without it because the GPG-key blob stored in iCloud Keychain is itself passphrase-encrypted — an Apple-ID-account compromise yields ciphertext, not a usable key. Hardening upgrades the channel later.
- **M3 reshaped to rename-and-cutover** (replacing "swap path references" cutover). End state: encrypted repo at `xianxu/brain` and `~/workspace/brain` — exactly where the legacy plaintext lived. No path-reference hunt across project files; no agent re-onboarding. Cost is a deliberate rename + double-backup dance during cutover. Substep structure:
  - 3a: bootstrap dry-run on second machine.
  - 3b: backup legacy via `xianxu/brain-backup` (cloud) + `~/workspace/brain.legacy` (local).
  - 3c: final mirror sync.
  - 3d: destructive sequence — delete legacy `xianxu/brain` → rename `xianxu/brain-private` → `xianxu/brain` → local `mv` → update remote URL.
  - 3e: 1-week verification window in the new `~/workspace/brain` before any cleanup commits.
  - 3f: cleanup — local after 1 week, `brain-backup` cloud after 1 month. Cleanup deadline goes into the schedule datatype (`ariadne#23`) once it ships.
