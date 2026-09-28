---
id: 000002
status: open
created: 2026-04-28
updated: 2026-04-28
---

# Gmail: on-disk message store + incremental sync

## Problem

Once #000001 lands, backfill is fast but still stateless — each invocation
re-downloads everything. For periodic sync ("pull new mail every N minutes")
and resumable backfill, we need a local store and a cursor so subsequent runs
only fetch deltas. Gmail's incremental API (`users.history.list`) is
message-keyed, so the store should be too — even though backfill pulls threads
(more efficient by round-trip), the canonical unit on disk is the message.
