---
id: 000051
status: open
deps: []
created: 2026-05-06
updated: 2026-06-08
estimate_hours: 1
---

# audit and harden apple security setup

## Done when

- Apple ID has at least two FIDO2 hardware security keys paired (one carry, one backup); SMS-based 2FA / trusted-device-approval no longer the only second factor.
- Advanced Data Protection enabled on the Apple ID.
- Recovery posture set up under ADP: Recovery Key (28-char string) written down on paper and stored in a safe location; Recovery Contact configured (spouse).
- Brief audit log written to this issue's `## Log` section: which keys were bought, recovery key location, any quirks encountered (so future-me can re-derive the setup if needed). Also documents the chosen brain-bootstrap channel (sneakernet) for future reference.

## Spec

General Apple-account hygiene, decoupled from the shared-brain bootstrap channel. (Originally framed as supporting an iCloud-Keychain-mediated GPG-key bootstrap; that bootstrap path was dropped 2026-05-07 in favor of sneakernet — see `nous#10` close log + threat-model `## Revisions` 2026-05-07. The hygiene axes below stand on their own merits.)

Three independent axes, all worth doing as ordinary account hygiene regardless of the shared-brain project:

1. **Hardware-key 2FA on Apple ID.** Replaces SMS-and-trusted-device-approval (vulnerable to SIM swap, social engineering) with physical-presence-required 2FA. Strongest 2FA available for Apple ID. Requires at least two keys (Apple won't let you pair only one — losing one would lock you out).
2. **Advanced Data Protection.** Extends end-to-end encryption to most iCloud categories (Backup, Drive, Photos, Notes, Reminders, Voice Memos, etc.) that were previously server-side encrypted with Apple holding keys. Removes Apple's ability to recover your data — and removes the residual recovery channel that could theoretically be coerced.
3. **Recovery posture.** ADP requires you to be your own recovery authority. Set up both a Recovery Key (you keep) and a Recovery Contact (a person you trust whose Apple ID can vouch for yours). Redundancy: any one of the two can recover; neither alone leaks your data.

These are end-of-project follow-on, **not MVP-gating**. The shared-brain bootstrap procedure works without them — the GPG private key never enters the Apple ecosystem on the new path (sneakernet via AirDrop/encrypted USB/signed message).

Out of scope (parked):

- Per-app password rotation across the entire Apple-account-using surface area. This issue is general Apple hygiene, not a full password-hygiene audit.
- Family Sharing recovery setup. Worth doing eventually; not gating this issue.
- Migrating away from Apple. Hopefully never, but if it ever happens, this configuration is reversible: turn off ADP, remove security keys.
- **iCloud Keychain promotion of the GPG private key (former M3).** Dropped 2026-05-07 — sneakernet is the chosen bootstrap channel. The GPG key never enters iCloud Keychain.

## Plan

### M1 — buy hardware keys + pair to Apple ID

- [ ] Order two FIDO2 keys. Yubico Security Key C NFC (~$30 each, USB-C + NFC) is the cheapest sufficient option. YubiKey 5C NFC (~$55 each) if you'd ever want the same key for SSH/GPG/OTP. **Decision: pick before ordering.**
- [ ] Receive keys. Take both out of packaging.
- [ ] Settings → Apple ID → Sign-In & Security → Security Keys → Add Security Keys. Pair both. Apple walks through the steps.
- [ ] Confirm that signing into iCloud on a fresh device now requires the physical key (test on a browser sign-in, e.g., signing into iCloud.com from a private window).
- [ ] Carry one key day-to-day; store one at home (or in a safe / lockbox).

### M2 — enable Advanced Data Protection + recovery posture

- [ ] Settings → [Apple ID] → iCloud → Advanced Data Protection → Turn On.
- [ ] Apple will require a recovery method first. Set up **both**:
  - **Recovery Key:** generate the 28-character key. Write it down on paper (do **not** store in the Apple ecosystem you're trying to harden — the point is it's outside). Store the paper somewhere durable (fireproof safe, safety deposit box, or at minimum, a drawer separate from the laptop).
  - **Recovery Contact:** add spouse. They'll need to confirm via their Apple ID; the role is "this person can vouch for me if I lose all my devices."
- [ ] Confirm all current Apple devices are on supported OS versions (iOS 16.2+ / macOS 13.1+). Older devices that can't update will get signed out of iCloud when ADP turns on; resolve before enabling.
- [ ] Turn ADP on. Wait for re-encryption of existing iCloud data (background, can take hours; doesn't block use).
- [ ] Verify: Settings → [Apple ID] → iCloud → Advanced Data Protection should show "On" and list both recovery methods.

### ~~M3 — promote GPG private key to iCloud Keychain~~ (wontfix 2026-05-07)

Dropped. The shared-brain bootstrap channel is sneakernet (passphrase-encrypted GPG export transferred via AirDrop/encrypted USB/signed message), not iCloud Keychain. The GPG private key is never placed in iCloud Keychain.

### M4 — write audit log

- [ ] Append to this issue's `## Log` section: which keys were ordered (model, count, where stored), Recovery Key storage location (no, don't paste the key — note the *location*), Recovery Contact's name, any quirks encountered during ADP enable. So future-me can verify the setup is still in force at a later date.

## Log

### 2026-05-06

- Issue created. Originally framed as a prerequisite for `nous#3` M3 (iCloud-Keychain GPG-bootstrap channel). Same-day reframe: end-of-project hardening, not MVP-gating. The bootstrap channel is workable on a default Apple ID because the GPG-key blob is passphrase-encrypted (Apple-ID-account compromise yields ciphertext, not a usable key). This issue upgrades the channel later. Moved to `explicitly_out` in the project file; tasks moved to bottom of the project's task list (after `nous#5`).

### 2026-05-07 — rescope after dropping iCloud-Keychain channel

Sneakernet adopted as the canonical brain-bootstrap channel (`nous#10` close, threat-model revision). M3 (move GPG to iCloud Keychain) becomes wontfix — the GPG key never enters the Apple ecosystem under the new model. M1 (hardware-key 2FA), M2 (ADP + recovery posture), and M4 (audit log) remain as general Apple-account hygiene, independent of brain.

### 2026-06-08 — migrated from brain#10 → nous#51
Moved from `brain/workshop/issues/000010` to nous to colocate with the shared-brain
security family (`nous#3`/`#8`/`#10`/`#11`). It's personal Apple-account hygiene with
no brain-repo code surface, and nous is where the security-posture issues live.
Renumbered (nous#10 was already taken). Project file `brain/data/project/shared-brain.md`
references updated `brain#10` → `nous#51`.
