---
id: 000037
status: open
deps: [nous#12]
github_issue:
created: 2026-06-01
updated: 2026-06-01
estimate_hours:
---

# system-wide identity revocation

## Problem

There is no clean way to revoke a person from the whole nous/brain system.
Today's tools are partial and per-brain:
- `nous brain recipient remove <brain> <fp>` — one brain, manifest + re-key only.
- `nous brain leave` — self only (removes own GitHub collaborator + revokes).
- `nous brain invite` — add side of collaborator management; there is no
  "remove this collaborator everywhere" verb.

Worse, a local `gpg --delete-keys` is *silently undone*: a fingerprint a brain
was granted to lives in **three remote places** — the manifest (`recipients:`),
the `keys` branch (`<login>.asc` / `<fp>.asc`), and `verified.yaml` — and
brain-sync actively pulls from them (`ImportAllPubkeys` re-imports the pubkey,
`AutoAdmitFromKeysBranch` re-admits it). So deleting locally just leaves the
recipient "(unknown)" until the next sync/invite resurrects it. Observed live
during nous#36 dogfood setup (2026-06-01): removing a test key locally, then
inviting the same GitHub user, re-associated the old fingerprint.

Without system-wide revocation, durable multi-day dogfooding (nous#12) is
painful — you can't cleanly retire a test identity and the only reliable reset
is `gh repo delete && recreate`.

## Spec

**Two layers, not one** (the load-bearing mental model):
- **GitHub identity = transport/ACL.** Collaborator access gates whether you can
  *fetch/push* the encrypted bytes + the keys branch. It does not gate
  readability.
- **GPG key = crypto.** The recipient list (manifest → gcrypt-participants) is
  fingerprints; only the matching secret key can *decrypt*.

A "person" is the pair `(github-login, gpg-fp)`, stitched by `<login>.asc` on
the keys branch (nous#26). A real revocation must hit **both** layers.

### The verb (not a new noun)

Add `nous identity revoke <fp|login>` — cross-brain fan-out under the existing
`identity` cluster (resist a third `collaborator` noun; the op is *about an
identity, applied across brains*). Resolves `(login, fp)`, then for the
revocation it performs:

1. **Per local brain that lists `fp` as a recipient:**
   - remove `fp` from the manifest `recipients:`
   - re-key push (gcrypt re-encrypts to the remaining set)
   - strip `<login>.asc` + `<fp>.asc` from the `keys` branch
   - remove the entry from `verified.yaml`
   - remove the GitHub collaborator (`gh api -X DELETE …/collaborators/<login>`)
2. **Local cleanup:** delete the pubkey from the keyring + the peer sidecar
   (`~/.config/nous/peers/<fp>.json`) if present.
3. **Record the ban** (see below).
4. **Print honestly:** forward-only (past blobs they hold / already in the
   remote object store stay decryptable with their key — true revocation =
   "assume leaked"); and the list of N brains actually covered.

### The ban list — the scope-limit fix

Revocation can only re-key brains **checked out in this workspace**; a brain not
present locally keeps the recipient. Fix with an operator-local persistent ban
list (e.g. `~/.config/nous/revoked.yaml`: fingerprints + logins + when/why).

The ban list is enforced **lazily** at the two points where a banned key would
otherwise re-enter:
- **On clone/sync:** if a banned `fp` is a recipient of a brain encountered
  later, nous re-keys it out (operator has push access by virtue of cloning) and
  warns — so the ban catches up to brains that weren't local at revoke time.
- **In auto-admit:** `AutoAdmitFromKeysBranch` consults the ban list first and
  refuses to re-admit a banned key even if its pubkey is still on a keys branch.
  This closes the resurrection loop directly.

Open question: ban list is operator-local (machine policy), so it doesn't
propagate to a *co-operator* — acceptable for the single-operator model; revisit
if multi-operator admin lands.

## Done when

- `nous identity revoke <fp|login>` removes the identity from every local brain
  (manifest + keys branch + verified.yaml + gcrypt re-key + GitHub collaborator)
  and the local keyring/sidecar, idempotently/resumably.
- A banned identity is recorded in `~/.config/nous/revoked.yaml` and is NOT
  re-admitted by auto-admit, and IS re-keyed out of a brain cloned/synced later.
- The command prints the forward-only caveat + the covered-brain count.
- E2E: extend `scripts/brain-vm-e2e.sh` — admit a peer, revoke them, assert
  they're gone from manifest+keys-branch+verified, a fresh clone of a
  not-previously-local brain re-keys them out, and auto-admit won't bring them
  back.

## Plan

- [ ] M1 — ban-list store + read/write (`lib/identity` or `lib/brain`); gate
  `AutoAdmitFromKeysBranch` on it (stops resurrection — smallest high-value slice).
- [ ] M2 — keys-branch removal + verified.yaml removal helpers (`lib/brain`);
  this is the "Recipient revocation" item the e2e atlas lists as not-yet-covered.
- [ ] M3 — `nous identity revoke` verb: cross-brain fan-out (manifest remove +
  re-key + keys-branch strip + verified remove + gh collaborator remove + local
  cleanup + ban record). Idempotent/resumable.
- [ ] M4 — lazy enforcement on clone/sync (re-key banned recipients out) + the
  forward-only/coverage messaging.
- [ ] M5 — e2e coverage in `scripts/brain-vm-e2e.sh` + atlas + threat-model note
  (revocation cost section already exists; link the mechanism).

## Notes

- Distinct from **nous#7** (lock primitive — concurrent-edit brake) and
  **nous#32** (leave a shared brain — *self*-revoke). This is *revoke-others,
  everywhere*. It's the realization of the "per-recipient revocation cost" the
  threat model already describes
  (`brain/atlas/threat-model-shared-brain.md`).
- Sizeable (cross-brain fan-out + two-layer revoke + ban-list enforcement +
  honest crypto caveats). Not a quick add — a proper milestone-structured issue.

## Existing `recipient remove` is partial (concrete bugs, 2026-06-01)

Traced what `nous brain recipient remove` (and the TUI "remove collaborator"
action, `lib/tui/brain/detail.go:128` → same lib path) actually clears:
manifest (`WithoutRecipient`+`RewriteFrontmatter`) → re-key push → `RevokePubkey`.
What it **leaves**, causing silent resurrection on the next invite/sync:

1. **`RevokePubkey` deletes only `<FP>.asc`** (`lib/brain/peerkeys.go:81`), but the
   nous#26 path publishes `<login>.asc`, and `AutoAdmitFromKeysBranch` lists every
   `.asc` and derives the fp from contents — so the login-keyed file survives and
   auto-admit re-admits. **Fix:** RevokePubkey must delete the `<login>.asc` too
   (resolve login via verified.yaml / the file's content).
2. **`verified.yaml` is never cleared on remove** — the `login→fp` stays
   "verified," so a later re-publish is auto-admitted with no fresh ceremony.
   **Fix:** remove must delete the verified.yaml entry (or invalidate it).
3. **GitHub collaborator is not removed** — `recipient remove`/TUI don't touch
   repo access (only `nous brain leave` does, self-only). The TUI's "remove
   collaborator" label is misleading; it's "remove recipient + re-key."

Leaks #1/#2 are bugs in the *existing per-brain* remove and could be fixed
standalone (M2 below) ahead of the cross-brain fan-out. #3 is the collaborator
layer (M3).

## Log

### 2026-06-01

Created from the nous#36 dogfood-setup discussion. Surfaced because removing a
test identity (yingtest42 / `…DD4F88C4`) locally kept getting undone by the
keys-branch auto-import + auto-admit. Design settled: a `nous identity revoke`
verb (not a `collaborator` noun) + an operator-local ban list that gates
auto-admit and re-keys lazily on clone/sync. For day-to-day dogfooding now, the
pragmatic reset stays `gh repo delete && recreate` until this lands.
