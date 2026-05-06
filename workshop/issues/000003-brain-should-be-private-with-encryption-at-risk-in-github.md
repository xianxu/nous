---
id: 000003
status: open
deps: [nous#8]
created: 2026-05-05
updated: 2026-05-05
estimate_hours: 5
---

# brain should be private with encryption at rest in github

## Done when

- Current personal brain repo runs against a `git-remote-gcrypt` remote on GitHub; pushed objects are opaque (no readable filenames, paths, or commit graph from the host's perspective).
- `brain-private` is established as the keyring substrate: holds GPG private keys, SSH keys, Syncthing device certs/IDs, paired-device trust list, in a documented layout.
- New-machine bootstrap procedure verified: from a clean machine, install gcrypt + git, clone brain-private with the symmetric passphrase, import keys, then clone any other gcrypt'd brain repo successfully.
- Atlas entry documents the encryption posture and bootstrap procedure so future agents and humans (me) find it.

## Spec

Sources: `brain/data/life/42shots/ideas/2026-04-28-01-pensive-collaborative-brain.md` §Privacy and bootstrap, §Build order step 1; threat model at `brain/atlas/threat-model-shared-brain.md` (authored under `nous#8` M1, this is the canonical reference for the encryption posture this issue implements). The default passphrase-storage mode is **macOS Keychain**, selected and justified in the threat model's *Passphrase storage* section.

Brain holds the rawest private capture in the system. Today it lives in a private GitHub repo, but the host still sees plaintext — filenames, commit messages, the commit graph, every typed artifact. That's the wrong default for data this sensitive, and it's the prerequisite for sharing: any `brain-shared-*` repo will be encrypted to a recipient list, so the host has to be untrusted by construction. Solo-mode benefit (encryption at rest with a passphrase) is immediate; collaborative-mode benefit (each shared brain encrypted to a different recipient set) compounds on top.

The load-bearing simplification from the pensive: **brain-private is the keyring**. One symmetric passphrase unlocks brain-private. Brain-private holds the GPG private key. The GPG key unlocks every `brain-shared-*` repo (each gcrypt'd to a recipient list that includes my GPG public key). So the only secret a human has to memorize is the brain-private passphrase. New machine = clone brain-private with passphrase → import keys → clone everything else.

Tool choice: `git-remote-gcrypt` for whole-remote opacity. `git-crypt` reserved for the unusual case where some files in a repo are public and others private — not the default.

Single-passphrase SPOF is a known and accepted risk. Mitigation: long passphrase, full-disk encryption on the laptop, frequent lock-on-sleep. Brain-private is more sensitive than any brain-shared (it's the keyring + the rawest capture), which is the right way around — sharing is a sensitivity downgrade, not an upgrade.

## Estimate

Range: **3–9 hr**. Best guess: **~5 hr**.

*Produced via `brain/data/life/42shots/velocity/estimate-logic-v2.1.md` against `baseline-v2.1.md`. Method A only.*

| Component | Primitive | Design (×0.5) | Impl (×1.5 familiarity) | Total |
|---|---|---|---|---|
| Passphrase wrapper script | Smaller Go / shell | 0–0.15 | 0.3–0.75 | 0.3–0.9 |
| Init script (M2 keyring layout) | Smaller Go / shell | 0–0.15 | 0.3–0.75 | 0.3–0.9 |
| Bootstrap script (M3) | Smaller Go / shell | 0–0.15 | 0.3–0.75 | 0.3–0.9 |
| Atlas docs (×3 milestones) | Atlas/docs | 0.075–0.3 | 0.15–0.6 | 0.225–0.9 |
| gcrypt tool discovery (real-tool budget) | Real-API discovery (×1.5 novel) | 0 | 0.45–0.9 | 0.45–0.9 |
| Second-machine dry-run + iterate (M3) | Verification + fix-loop | 0.25–0.75 | 0.45–1.5 | 0.7–2.25 |
| Mid-flight scope (likely keyring layout rework) | Mid-flight pivot | 0.1–0.25 | 0.3–0.75 | 0.4–1 |
| **Subtotal** | | 0.4–1.75 | 2.3–6 | 2.7–7.75 |
| **+30% design buffer** | | +0.12–0.5 | n/a | +0.12–0.5 |
| **Total** | | | | **~3–9 hr** |

Familiarity ×1.5 applied to impl (gcrypt is novel-but-bounded — well-documented tool, just unfamiliar to me). Spec-quality discount ×0.5 (mid-density spec; not charon-plan-doc thorough). The single biggest uncertainty is M3's dry-run on a second machine — first-time bootstrap procedures consistently surface unanticipated quirks (gpg-agent paths, syncthing cert formats, ssh known_hosts) that aren't visible until you try it cold.

## Plan

### M1 — gcrypt remote on personal brain

- [ ] Document the gcrypt setup procedure in `atlas/` (passphrase choice, remote URL form, push/clone semantics, what GitHub sees).
- [ ] Pick the passphrase (long, generated; storage mode chosen below).
- [ ] **Passphrase storage wrapper.** Tiny script (e.g., `scripts/brain-passphrase.sh`) that fetches the passphrase and pipes to gcrypt. Configurable source via env or config: `tty` (prompt), `keychain` (`security find-generic-password -w`), `op` (`op read "op://Personal/brain-private/passphrase"`), or `env` (for CI-like contexts). Default chosen per `#8` M1's recommendation. Threat-model implications of each mode are documented in `#8`, not duplicated here.
- [ ] Configure `brain` repo with a gcrypt remote alongside the existing GitHub remote, using the wrapper.
- [ ] Push history; verify on GitHub UI that contents are opaque.
- [ ] Keep the unencrypted remote alive temporarily as a safety net; do not retire until M3 verifies a clean clone-and-decrypt elsewhere.

### M2 — brain-private keyring layout

- [ ] Decide where in the brain tree keys live (e.g., `keys/gpg/`, `keys/ssh/`, `keys/syncthing/`, `keys/paired-devices.md`).
- [ ] Move existing keys into the layout; verify nothing breaks (gpg signing, ssh to github, etc.).
- [ ] Init script (`scripts/brain-bootstrap.sh` or similar in `nous/`) that reads the layout and installs keys onto a fresh machine.
- [ ] Document the layout in `atlas/`.

### M3 — new-machine bootstrap

- [ ] Write the bootstrap procedure as a checklist (manual first, then optionally scripted) in `atlas/`.
- [ ] Dry-run on a second machine (VM or actual second laptop): clone brain-private with passphrase, run init script, clone one other repo.
- [ ] Fix whatever doesn't work the first time; iterate until repeatable.
- [ ] Retire the unencrypted remote on the personal brain. Force-push only if necessary; prefer leaving it as a stale archive.

## Log

### 2026-05-05

- Issue spec'd from `brain/data/life/42shots/ideas/2026-04-28-01-pensive-collaborative-brain.md`. Originally a stub; now scoped as M1 of the shared-brain project.
