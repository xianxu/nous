---
id: 000003
status: open
deps: [nous#8]
created: 2026-05-05
updated: 2026-05-05
estimate_hours: 7
---

# brain should be private with encryption at rest in github

## Done when

- A **new** `brain-private` repo on GitHub runs against a `git-remote-gcrypt` remote; pushed objects are opaque (no readable filenames, paths, or commit graph from the host's perspective).
- New repo's on-disk checkout at `~/workspace/brain-private` mirrors the operational content of the existing `~/workspace/brain` checkout. Content stays in sync until cutover (M3); during the migration window, both coexist.
- Existing `~/workspace/brain` and the existing GitHub `brain` repo are **untouched** through M1 and M2 — they remain the safety net. If anything goes sideways, abandoning `brain-private` costs nothing material; the old repo is the source of truth until cutover lands.
- `brain-private` is established as the *backup keyring* substrate per the threat model: passphrase-encrypted GPG export, SSH key exports, Syncthing device cert export, paired-device trust list. Operational keys stay in their canonical OS locations (`~/.gnupg/`, `~/.ssh/`, etc.).
- New-machine bootstrap procedure verified end-to-end on a second machine: clone `brain-private` with the symmetric passphrase, run bootstrap script, GPG key imported into `~/.gnupg/`, decrypt operations work transparently via gpg-agent.
- Cutover landed: agent path conventions and tool references switched from `~/workspace/brain` to `~/workspace/brain-private`; the old `brain` repo and checkout are archived (renamed to `brain.legacy` on disk; GitHub repo made private/archived; not deleted).
- Atlas entry documents the encryption posture and bootstrap procedure so future agents and humans find it.

## Spec

Sources: `brain/data/life/42shots/ideas/2026-04-28-01-pensive-collaborative-brain.md` §Privacy and bootstrap, §Build order step 1; threat model at `brain/atlas/threat-model-shared-brain.md` (authored under `nous#8` M1, this is the canonical reference for the encryption posture this issue implements). The default passphrase-storage mode is **macOS Keychain**, selected and justified in the threat model's *Passphrase storage* section.

**Safety-net migration shape:** rather than adding a gcrypt remote to the existing `brain` repo and gradually retiring the unencrypted one (which puts the live repo at risk during setup), the migration provisions a *new* `brain-private` repo on GitHub and on disk, mirrors the operational content into it, and verifies end-to-end on a second machine before any cutover. The existing `brain` checkout and remote remain fully intact and operational throughout. Worst-case rollback is "abandon the new repo, keep using the old one" — costs nothing, breaks nothing. Cutover (M3) is the single point at which agent paths switch over; everything before that is non-destructive.

Brain holds the rawest private capture in the system. Today it lives in a private GitHub repo, but the host still sees plaintext — filenames, commit messages, the commit graph, every typed artifact. That's the wrong default for data this sensitive, and it's the prerequisite for sharing: any `brain-shared-*` repo will be encrypted to a recipient list, so the host has to be untrusted by construction. Solo-mode benefit (encryption at rest with a passphrase) is immediate; collaborative-mode benefit (each shared brain encrypted to a different recipient set) compounds on top.

The load-bearing simplification from the pensive: **brain-private is the keyring**. One symmetric passphrase unlocks brain-private. Brain-private holds the GPG private key. The GPG key unlocks every `brain-shared-*` repo (each gcrypt'd to a recipient list that includes my GPG public key). So the only secret a human has to memorize is the brain-private passphrase. New machine = clone brain-private with passphrase → import keys → clone everything else.

Tool choice: `git-remote-gcrypt` for whole-remote opacity. `git-crypt` reserved for the unusual case where some files in a repo are public and others private — not the default.

Single-passphrase SPOF is a known and accepted risk. Mitigation: long passphrase, full-disk encryption on the laptop, frequent lock-on-sleep. Brain-private is more sensitive than any brain-shared (it's the keyring + the rawest capture), which is the right way around — sharing is a sensitivity downgrade, not an upgrade.

## Estimate

Range: **4–12 hr**. Best guess: **~7 hr**.

*Produced via `brain/data/life/42shots/velocity/estimate-logic-v2.1.md` against `baseline-v2.1.md`. Method A only.*

| Component | Primitive | Design (×0.5) | Impl (×1.5 familiarity) | Total |
|---|---|---|---|---|
| Passphrase wrapper script (M1) | Smaller Go / shell | 0–0.15 | 0.3–0.75 | 0.3–0.9 |
| New repo provisioning + initial mirror (M1) | Real-tool discovery + setup | 0.15–0.5 | 0.45–1.2 | 0.6–1.7 |
| Mirror sync helper (M1.5) | Smaller Go / shell | 0–0.15 | 0.3–0.75 | 0.3–0.9 |
| Backup keyring layout + GPG export (M2) | Smaller Go / shell | 0.1–0.4 | 0.45–1.2 | 0.55–1.6 |
| Bootstrap script (M3) | Smaller Go / shell | 0–0.15 | 0.3–0.75 | 0.3–0.9 |
| Atlas docs (×3 milestones) | Atlas/docs | 0.075–0.3 | 0.15–0.6 | 0.225–0.9 |
| gcrypt tool discovery (real-tool budget) | Real-API discovery | 0 | 0.45–0.9 | 0.45–0.9 |
| Second-machine dry-run + iterate (M3) | Verification + fix-loop | 0.25–0.75 | 0.45–1.5 | 0.7–2.25 |
| Cutover + path-reference hunt (M3) | Cross-cutting refactor | 0.2–0.5 | 0.45–1.2 | 0.65–1.7 |
| Mid-flight scope (likely keyring layout rework) | Mid-flight pivot | 0.1–0.25 | 0.3–0.75 | 0.4–1 |
| **Subtotal** | | 0.875–3.15 | 3.6–9.6 | 4.475–12.75 |
| **+30% design buffer** | | +0.26–0.95 | n/a | +0.26–0.95 |
| **Total** | | | | **~4.7–13.7 hr** |

Rounded down to **4–12 hr** because the upper-band stack assumes second-machine dry-run hits multiple unknown quirks; in practice one or two iterations is more likely. Bumped from prior estimate (3–9 hr) because of the safety-net shape: new-repo provisioning + content mirroring + cutover hunt for path references add real work the original "add gcrypt remote to existing repo" plan didn't have. Net add ~2 hr at the midpoint, well worth the rollback safety it buys.

Familiarity ×1.5 applied to impl (gcrypt is novel-but-bounded). Spec-quality discount ×0.5 (mid-density spec). The single biggest uncertainty remains the second-machine dry-run — first-time bootstrap procedures consistently surface unanticipated quirks (gpg-agent paths, gpg-preset-passphrase + keygrip dance, Keychain entry naming) that aren't visible until you try it cold.

## Plan

### M1 — provision new gcrypt'd `brain-private` repo and mirror content

Throughout M1, the existing `~/workspace/brain` and the existing GitHub `brain` repo stay untouched. Operationally we keep working in the old one; the new one is a parallel target being filled.

- [ ] **Document the gcrypt setup procedure** in `atlas/` (passphrase choice, remote URL form, push/clone semantics, what GitHub sees). Land before any provisioning so the procedure is the source of truth, not a retroactive write-up.
- [ ] **Pick and store the gcrypt passphrase.** Long, generated. Stored in macOS Keychain under a known entry name (e.g., `brain-private-gcrypt-passphrase`).
- [ ] **Passphrase wrapper script.** Tiny script (e.g., `scripts/brain-passphrase.sh`) that reads `.brain/config.md`'s `passphrase_source:` field and fetches from the named source: `tty` (prompt), `keychain` (`security find-generic-password -w`, default), `op` (`op read "op://Personal/brain-private/passphrase"`), or `env` (CI-like contexts). Pipes to gcrypt.
- [ ] **Create new private GitHub repo** `brain-private` (or appropriate slug). Empty.
- [ ] **Initialize new local checkout** at `~/workspace/brain-private`. `git init` + add gcrypt remote pointing at the new GitHub repo, configured to use the wrapper script.
- [ ] **Author `.brain/config.md`** at the new repo's root with `mode: private`, `name: personal`, `passphrase_source: keychain`, `sync_substrate: none`. This is the manifest the threat model's *Brain identification* section names.
- [ ] **Mirror content.** Copy operational content from `~/workspace/brain/` to `~/workspace/brain-private/` (excluding `.git/`, transient build artifacts, anything explicitly listed in a migration ignore-list). Initial commit. Push to gcrypt remote.
- [ ] **Verify opacity.** Open the new GitHub repo in a browser; confirm contents are opaque (no readable filenames, paths, commit graph).
- [ ] **Verify continued operation of the existing `brain`.** Run a no-op edit + commit + push on the original `~/workspace/brain` to confirm it's still fully functional. Old repo is the safety net; we keep verifying it works.

### M1.5 — keep `brain-private` in sync with operational `brain` during migration window

Until cutover (M3), changes happen in the operational `brain`. We keep `brain-private` current so cutover is a swap rather than a re-migration.

- [ ] **Sync helper.** Tiny script (e.g., `scripts/brain-mirror.sh`) that re-runs the file copy + commits + pushes to gcrypt. Idempotent. Runs on demand.
- [ ] **Cadence:** run after every meaningful operational `brain` push, until cutover. Manual is fine; the migration window is short.

### M2 — backup keyring layout in `brain-private`

Per the threat model: `brain-private` holds passphrase-encrypted *exports* of keys, not the live keys. Operational keys stay in their canonical OS locations (`~/.gnupg/`, `~/.ssh/`, Syncthing config dir). Lands in the new `~/workspace/brain-private`, not the old `~/workspace/brain`.

- [ ] Decide layout for exports — proposed: `keys/gpg-export.asc` (passphrase-encrypted GPG export via `gpg --export-secret-keys --armor`), `keys/ssh/` (SSH keys may stay plaintext if the threat model accepts it; otherwise re-encrypt), `keys/syncthing-cert.tar.gz.enc`, `keys/paired-devices.md`, `keys/recipients/` (public keys of known recipients per brain).
- [ ] Generate / locate the export passphrase. Store in Keychain under a separate entry from the gcrypt passphrase.
- [ ] Export existing GPG private key with passphrase protection; place under `keys/gpg-export.asc`. Verify `gpg --import` against the export round-trips on a scratch keyring.
- [ ] Verify operational keys at `~/.gnupg/` etc. still work; nothing broken (gpg signing, ssh to github, etc.).
- [ ] Init script (`scripts/brain-bootstrap.sh` or similar in `nous/`) that, on a fresh machine, reads exports from `brain-private`, prompts once for the export passphrase, imports GPG into `~/.gnupg/`, registers the GPG-key passphrase in Keychain, restores ssh / Syncthing config from exports.
- [ ] Document the layout and the operational vs backup distinction in the threat-model doc; link from atlas.

### M3 — new-machine bootstrap, then cutover

- [ ] **Write the bootstrap procedure** as a checklist (manual first, then optionally scripted) in `atlas/`.
- [ ] **Dry-run on a second machine** (VM or actual second laptop): clone `brain-private` with the gcrypt passphrase, run the bootstrap script, verify GPG import works, verify a decrypt operation against shared content (or a synthetic test artifact if no shared brain exists yet).
- [ ] **Fix whatever doesn't work** the first time; iterate until repeatable. Keep the original `brain` operational — we are not committed to cutover until this dry-run is clean.
- [ ] **Final sync** of operational content from `~/workspace/brain` into `~/workspace/brain-private` via the M1.5 mirror script, immediately before cutover.
- [ ] **Cutover.** Update agent path conventions and tool references from `~/workspace/brain` to `~/workspace/brain-private`. Update `brain/data/project/*.md`'s sources, `nous/AGENTS.local.md` if it has paths, anything else grep'd for `/workspace/brain` (excluding intended legacy references). Land in one commit per repo so rollback is a single revert.
- [ ] **Archive the legacy.** `mv ~/workspace/brain ~/workspace/brain.legacy` so accidental writes have no plausible target. Make the legacy GitHub repo private + add a README pointing at the new repo's location for any future archaeology. Do **not** delete the legacy repo for at least one quarter; archived state is cheap insurance.
- [ ] **Optional:** rename `brain-private` → `brain` on disk and on GitHub once dust settles (a week of clean operation). Defer; not gating issue close.

## Log

### 2026-05-05

- Issue spec'd from `brain/data/life/42shots/ideas/2026-04-28-01-pensive-collaborative-brain.md`. Originally a stub; now scoped as M1 of the shared-brain project.
- **Reshape to safety-net migration:** original plan added a gcrypt remote to the existing `brain` repo, planning to retire the unencrypted remote after second-machine verification. Reshaped to provision a *new* `brain-private` repo, mirror operational content into it, verify end-to-end on a second machine, then cut over via path-reference swap. Existing `brain` checkout and remote stay fully intact through M1, M1.5, M2 — they remain the source of truth until cutover lands in M3. Worst-case rollback is "abandon the new repo, keep using the old one" rather than "untangle a half-migrated remote." Estimate bumped 3–9 hr → 4–12 hr (best guess 5 → 7) for the new-repo provisioning + mirror script + cutover hunt; well worth the rollback safety.
