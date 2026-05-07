---
id: 000010
status: open
deps: [000003]
created: 2026-05-06
updated: 2026-05-06
---

# second-machine bootstrap dry-run for the encrypted brain

## Problem

`nous#3` M3 step 3a was scoped out of the cutover — the destructive rename-and-cutover landed on a single machine without first verifying the bootstrap procedure works on a fresh second machine. This was an **accepted risk** taken because the cloud + local backup channels (`xianxu/brain-backup` and `~/workspace/brain.legacy*`) were still in place and the personal-MVP rollback story was intact.

That risk needs to be repaid before the safety nets get cleaned up:
- Local cleanup: `~/workspace/brain.legacy*` removable after 1 week (~2026-05-13).
- Cloud cleanup: `xianxu/brain-backup` removable after 1 month (~2026-06-06).

If the bootstrap procedure has a gap that's only fixable from a working second machine (e.g., the iCloud-Keychain export is missing a piece, or pinentry-mac configuration on a fresh device fails for a non-obvious reason), the only way to discover that is to actually do it. Discover before the safety nets are gone.

## Spec

End-to-end dry-run of the bootstrap procedure documented in `nous/atlas/gcrypt-brain-encryption.md` (and synthesized in `brain/atlas/threat-model-shared-brain.md` `## GPG key bootstrap and passphrase storage`):

1. On a second Mac (VM, spare laptop, or partner device with permission), starting from a state with no GPG key, no `git-remote-gcrypt`, no Homebrew packages installed for any of this:
2. Sign into iCloud + open Keychain Access; locate the brain-GPG-key secure note (per `brain#10` M3 convention — but that issue hasn't shipped yet, so for the dry-run, manually placing the export in iCloud Keychain is part of the procedure under test).
3. Run `make identity` from a checked-out `nous` to install dependencies and verify the GPG-import path works.
4. `gpg --import <exported-key>` from the Keychain note.
5. Trigger pinentry-mac's "Save in Keychain" prompt by performing a test decrypt.
6. `git clone gcrypt::ssh://git@github.com/xianxu/brain.git` to a scratch path; verify decrypt succeeds and content is intact.
7. Make a small commit on a scratch branch; push; verify it lands as opaque ciphertext on GitHub; pull from the primary machine and confirm the commit decrypts.
8. Tear down the scratch checkout; revoke any temporary access if a non-personal device was used.

Done-when:
- Procedure is repeatable cold (no manual fix-ups remembered between steps).
- Any gaps discovered are fixed in `nous/atlas/gcrypt-brain-encryption.md` and `make identity` (if a tooling gap).
- A short note in `keys/paired-devices.md` records the dry-run device, even if it's torn down afterward.

## Plan

- [ ] Identify a second Mac available for the dry-run (VM acceptable; physical secondary preferred so we exercise the iCloud-Keychain channel for real, not via a synced VM that already has the keychain).
- [ ] Manually place a passphrase-encrypted GPG-key export in iCloud Keychain as a secure note (this is the `brain#10` M3 convention — doing it manually here de-risks #10 too).
- [ ] Run the bootstrap procedure end-to-end on the second machine.
- [ ] Note any friction or missing steps; fix in atlas + `make identity` script as appropriate.
- [ ] Push from the second machine; pull on primary; verify round-trip.
- [ ] Record the dry-run in `keys/paired-devices.md`.
- [ ] Re-evaluate `xianxu/brain-backup` and `brain.legacy*` cleanup timing — if dry-run was clean, original cleanup deadlines stand; if not, push them out until fixed-then-re-verified.

## Log

### 2026-05-06 — created
Carved out of `nous#3` M3 step 3a after the cutover landed without it. Dependency: `nous#3` (the encrypted brain must exist to bootstrap to). See `nous#3` log entries 2026-05-06 for the accepted-risk reasoning.
