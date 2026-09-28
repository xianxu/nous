---
id: '000038'
status: done
created: 2026-06-01
updated: 2026-06-02
estimate_hours: 2
actual_hours: 2
---

# recipient remove: clear all per-brain revoke state

## Problem

`nous brain recipient remove` (and the TUI "remove collaborator" action,
`lib/tui/brain/detail.go:128`) does not fully revoke a recipient *from the one
brain it operates on* — it leaves enough state that the next invite/sync
silently resurrects them (observed in the nous#36 dogfood, 2026-06-01; details
in nous#37). Three concrete leaks:

1. **`RevokePubkey` deletes only `<FP>.asc`** (`lib/brain/peerkeys.go`), but the
   nous#26 path also publishes `<login>.asc`; `AutoAdmitFromKeysBranch` reads
   every `.asc` and derives the fp from contents → the login-keyed file survives
   and auto-admit re-admits.
2. **`verified.yaml` entry is never cleared** → the `login→fp` stays "verified",
   so a later re-publish is auto-admitted with no fresh ceremony.
3. **GitHub collaborator is not removed** → they keep repo (transport) access;
   the TUI's "remove collaborator" label is a lie (only `nous brain leave`
   revokes collaborator, self-only).

This is the **per-brain** completeness fix. The cross-brain fan-out, ban list,
and `nous identity revoke` verb stay in **nous#37** — this issue just makes a
single-brain remove actually stick (which is what unblocks clean dogfood resets,
nous#12). #3 is easy precisely because there's no fan-out here.
