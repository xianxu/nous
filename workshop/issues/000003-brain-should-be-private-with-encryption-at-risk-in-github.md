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
| Cutover + path-reference hunt (M3) | Cross-cutting refactor | 0.2–0.5 | 0.45–1.2 | 0.65–1.7 |
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

- [ ] **0a — install dependencies** via Homebrew: `brew install gnupg pinentry-mac git-remote-gcrypt`. Verify each: `gpg --version`, `which pinentry-mac`, `which git-remote-gcrypt`.
- [ ] **0b — generate or import the GPG keypair.**
  - *Generate fresh* (recommended): `gpg --full-generate-key` — RSA 4096, no expiry (or 5-year expiry to match common practice), name `Xianxu Xu`, email `lovchatvol@gmail.com`, comment `brain encryption key`. Set a strong passphrase (long, generated, will be stored in Keychain).
  - *Import existing* (if a key already exists in 1Password / USB backup / etc.): `gpg --import < /path/to/private-key.asc` and enter the existing passphrase.
- [ ] **0c — configure gpg-agent + pinentry-mac.** Add to `~/.gnupg/gpg-agent.conf`:
  ```
  pinentry-program /opt/homebrew/bin/pinentry-mac
  default-cache-ttl 600
  max-cache-ttl 7200
  ```
  Restart gpg-agent: `gpgconf --kill gpg-agent`. Test by running `gpg --decrypt` against a small test ciphertext — first time prompts pinentry-mac with "Save in Keychain" checkbox; tick it, enter passphrase. Subsequent prompts are non-interactive (cached) until cache expires.
- [ ] **0d — verify Keychain stores the passphrase.** `security find-generic-password -s "GnuPG" -g` should now return a result (the saved item from pinentry-mac's "Save in Keychain"). This is the entry charon will fetch in `charon#21`.

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

### M3 — new-machine bootstrap, then cutover

`brain#10` Apple-account hardening is end-of-project, not a prereq for M3. The iCloud-Keychain bootstrap channel is workable for personal MVP without it because the GPG-key blob stored in iCloud Keychain is itself passphrase-encrypted — an Apple-ID-account compromise yields ciphertext, not a usable key. Hardening upgrades the channel later; M3 can proceed.

- [ ] **Write the bootstrap procedure** as a checklist in `nous/atlas/` (and pointer from `brain/atlas/threat-model-shared-brain.md`'s *Bootstrap* section). The procedure walks through: install gpg + gcrypt + git on the fresh machine; sign into iCloud + open Keychain Access; find the `[brain GPG]` secure note; copy the ASCII-armored block; `gpg --import`; register the GPG-key passphrase in macOS Keychain (`security add-generic-password ...`); configure pinentry-mac in `~/.gnupg/gpg-agent.conf`; clone `brain-private`.
- [ ] **Dry-run on a second machine** (VM or actual second laptop). Run through the procedure cold. Verify: (a) `gpg --list-secret-keys` shows the imported key; (b) `gpg --decrypt` of a small test ciphertext succeeds without prompting (key cached via Keychain); (c) `git clone gcrypt::...brain-private` succeeds and decrypts cleanly.
- [ ] **Fix whatever doesn't work** the first time; iterate until repeatable. Keep the original `brain` operational — we are not committed to cutover until the dry-run is clean.
- [ ] **Final sync** of operational content from `~/workspace/brain` into `~/workspace/brain-private` via the M1.5 mirror script, immediately before cutover.
- [ ] **Cutover.** Update agent path conventions and tool references from `~/workspace/brain` to `~/workspace/brain-private`. Update `brain/data/project/*.md`'s sources, `nous/AGENTS.local.md` if it has paths, anything else grep'd for `/workspace/brain` (excluding intended legacy references). Land in one commit per repo so rollback is a single revert.
- [ ] **Archive the legacy.** `mv ~/workspace/brain ~/workspace/brain.legacy` so accidental writes have no plausible target. Make the legacy GitHub repo private + add a README pointing at the new repo's location for any future archaeology. Do **not** delete the legacy repo for at least one quarter; archived state is cheap insurance.
- [ ] **Optional:** rename `brain-private` → `brain` on disk and on GitHub once dust settles (a week of clean operation). Defer; not gating issue close.

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
