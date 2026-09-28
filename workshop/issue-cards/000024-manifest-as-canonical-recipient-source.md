---
id: '000024'
status: done
created: 2026-05-19
updated: 2026-05-19
estimate_hours: 2
actual_hours: N/A
---

# Manifest as canonical source of recipients; sync at push, not at callsite

## Problem

`.brain/config.md`'s `recipients:` list and `.git/config`'s
`remote.origin.gcrypt-participants` are two stores of the same fact:
"who is on this brain." Today the invariant "these must agree" is
maintained by every callsite that mutates one calling the helper to
mutate the other.

```
nous brain new           → WriteManifest      + SetGcryptParticipants
nous brain recipient add → RewriteFrontmatter + SetGcryptParticipants
nous brain recipient rm  → RewriteFrontmatter + SetGcryptParticipants
nous brain clone         → (clone)            + SyncGcryptParticipantsFromManifest
brainsync.PullBrain      → (pull)             + SyncGcryptParticipantsFromManifest
lib/tui/brain/recipient_add.go → (same as cmd/nous/brain_recipient.go)
```

Two reasons this is a smell:

1. **Doubles cognitive load.** "Did I update both?" is something
   future code paths shouldn't have to think about. The 2026-05-19
   bug fixed in `dd7eb95` was exactly this kind of omission — the
   clone path was missing the gcrypt-participants sync, only
   discovered when the e2e test (filed alongside) exercised the
   multi-peer push scenario.

2. **Hides the source of truth.** Both stores look authoritative
   from outside the code. The manifest is the *intended* canonical
   record (human-readable, travels with the brain, version
   controlled), but nothing enforces the "derived" relationship of
   `gcrypt-participants`.

The drift modes that survive today's discipline:

- Operator hand-edits `.brain/config.md` → gcrypt-participants
  stale → next push encrypts to old set.
- Operator runs `git config remote.origin.gcrypt-participants ...`
  → manifest stale → audit/identity tooling reads wrong list.
- New code path mutates manifest without thinking about config.
