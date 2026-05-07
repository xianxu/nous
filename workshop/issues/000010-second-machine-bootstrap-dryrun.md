---
id: 000010
status: done
deps: [000003]
created: 2026-05-06
updated: 2026-05-07
actual_hours: 1
---

# second-machine bootstrap dry-run for the encrypted brain

## Problem

`nous#3` M3 step 3a was scoped out of the cutover — the destructive rename-and-cutover landed on a single machine without first verifying the bootstrap procedure works on a fresh second machine. This was an **accepted risk** taken because the cloud + local backup channels (`xianxu/brain-backup` and `~/workspace/brain.legacy*`) were still in place and the personal-MVP rollback story was intact.

That risk needs to be repaid before the safety nets get cleaned up:
- Local cleanup: `~/workspace/brain.legacy*` removable after 1 week (~2026-05-13).
- Cloud cleanup: `xianxu/brain-backup` removable after 1 month (~2026-06-06).

## Spec

End-to-end dry-run of the bootstrap procedure on a fresh Mac. After 2026-05-07's scope decision (drop iCloud-Keychain integration in favor of sneakernet via an independent channel), the procedure under test simplifies to:

1. On a second Mac (VM, spare laptop, or partner device with permission), starting from a state with no GPG key, no `git-remote-gcrypt`, no Homebrew packages installed for any of this:
2. `git clone https://github.com/xianxu/nous && cd nous && make nous-bootstrap`. Validates substrate (Xcode CLT, Homebrew, Brewfile), GPG identity setup (pinentry config, optional Keychain probe for `brain-gpg-key`), workflow tools (`gh auth login`, openshell, mutagen), GitHub SSH-key auto-registration, fzf shell hooks.
3. **Sneakernet the GPG private key** from the primary machine: `gpg --armor --export-secret-keys <FP> > /tmp/key.asc`, transfer via independent channel (AirDrop, encrypted USB, signed message), `gpg --import /tmp/key.asc` on the new machine. Key blob is passphrase-encrypted at the GPG layer, so channel doesn't need to be E2E itself.
4. `git clone gcrypt::https://github.com/xianxu/brain.git ../brain` (or `gcrypt::ssh://...` if SSH key is registered). Verify decrypt succeeds and content is intact.
5. Make a small commit on a scratch branch; push; verify it lands as opaque ciphertext on GitHub; pull from the primary machine and confirm the commit decrypts.

## Plan

- [x] Identify a second Mac for the dry-run — used a fresh tart `tahoe-base` VM (`scratch`).
- [x] ~~Manually place a passphrase-encrypted GPG-key export in iCloud Keychain~~ — **dropped** per 2026-05-07 scope decision; sneakernet (scp + `gpg --import`) is the canonical channel now. Keychain probe in identity.sh kept as opportunistic-detection only.
- [x] Run the bootstrap procedure end-to-end on the second machine — completed via SSH-driven manual flow with several friction findings (see Log).
- [x] Note any friction; fix in scripts as appropriate — fixes landed in nous#11 (CLT polling, GitHub SSH-key auto-register, identity.sh SSH-detect + GPG_TTY auto-export, dedup-grep widening).
- [x] Push from the second machine; pull on primary; verify round-trip — done with `make new-brain ../brain-vm-test` end-to-end (encrypted commit landed on GitHub, decrypted on primary).
- [x] ~~Record the dry-run in `keys/paired-devices.md`~~ — N/A; the VM was disposable and torn down. Procedure refinements live in commits to nous instead.
- [ ] **Re-evaluate `xianxu/brain-backup` and `brain.legacy*` cleanup timing** — original deadlines stand (local: ~2026-05-13, cloud: ~2026-06-06). Tracked outside this issue; this milestone unlocks the cleanup but doesn't perform it.

## Log

### 2026-05-06 — created
Carved out of `nous#3` M3 step 3a after the cutover landed without it. Dependency: `nous#3` (the encrypted brain must exist to bootstrap to). See `nous#3` log entries 2026-05-06 for the accepted-risk reasoning.

### 2026-05-07 — VM dry-run, scope decision, close
End-to-end dry-run completed in a tart `tahoe-base` VM via SSH. Took ~1h (mostly waiting on Brewfile install + test iteration), but the harness work that came out of it (`make nous-test-bootstrap`, `make nous-test-snapshot`, `make nous-test-roundtrip`) tracked separately as nous#11 and dwarfs this issue's actual time.

**What got validated:**
- `make nous-bootstrap` from-scratch on a fresh Mac. All substrate + workflow layers green. (Test harness in nous#11 captures this as a regression test.)
- Sneakernet of GPG private key via scp from host to VM, `gpg --import` succeeds, identity.sh detects the imported key on subsequent runs and skips fresh-key generation.
- `make new-brain ../brain-vm-test` creates a private GitHub repo, configures gcrypt remote, force-pushes encrypted initial commit. After hitting the GPG_TTY trap (curses pinentry needs `GPG_TTY=$(tty)` exported), retried successfully.
- gcrypt round-trip from VM to GitHub and back to host machine — encrypted commits land as opaque ciphertext on GitHub, decrypt cleanly on the host.
- (User also tested `git clone gcrypt::ssh://git@github.com/xianxu/brain.git` from the VM — works.)

**Friction caught (all fixed in nous#11):**
- `xcode-select --install` triggered a system dialog and the script bailed with "re-run when ready"; now polls for completion (up to 20 min).
- `gh auth login` interactive in `.openshell` bootstrap; gated under `NOUS_BOOTSTRAP_SKIP_OPENSHELL=1` for non-interactive contexts.
- pinentry-mac fails over SSH (no Aqua); identity.sh now auto-detects `SSH_CONNECTION` and selects pinentry-curses.
- pinentry-curses needs `GPG_TTY=$(tty)` exported in shell rc, otherwise gpg fails with "Inappropriate ioctl for device" mid-`git push`; identity.sh now appends the export to `~/.zshrc`.
- gpg-agent.conf duplicate-block bug when re-running with a different pinentry; grep widened from `^pinentry-program.*pinentry-mac` to `^pinentry-program`.
- GitHub SSH-key flow added: `make nous-bootstrap` now auto-generates ed25519 if missing and registers via `gh ssh-key add`.

**Scope decision 2026-05-07:** the iCloud-Keychain channel was the original "recommended bootstrap" path. We're dropping it — sneakernet (passphrase-encrypted ASCII export + transfer via independent channel like AirDrop/WhatsApp) is operationally simpler, doesn't lock the user into the Apple ecosystem for the brain's recovery story, and the GPG export's passphrase encryption already protects the file at rest regardless of channel. brain#10 rescoped accordingly: M3 (move GPG to iCloud Keychain) → wontfix; M1/M2 (hardware keys, ADP) retained as general Apple-account hygiene independent of brain.

**End state:** procedure validated; nous#11 codifies the friction-fixes as test infra; cleanup of `brain.legacy*` and `xianxu/brain-backup` unblocked (track outside this issue against the wall-clock deadlines). Closing.
