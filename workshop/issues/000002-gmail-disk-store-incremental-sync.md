---
id: 000002
status: open
deps: [000001]
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

## Spec

### Framing: threads for cold backfill, messages for incremental

The two sync modes use different Gmail API resources by design:

- **Cold backfill** uses `threads.list` + batched `threads.get(format=full)`.
  Round-trip win dominates (one batched call returns N messages of a
  conversation). Threads do over-fetch: `threads.list?q=after:T` returns the
  whole thread if any message matches, including old messages from years prior.
  We accept this — the disk store dedupes on message ID, so over-fetch costs
  bandwidth, not storage. For a cold mailbox this trade is correct: thread
  batching wins more than time-scoped messages would save.

- **Incremental sync** uses `users.history.list` + batched `messages.get`.
  History entries are message-scoped: a new reply produces one `messagesAdded`
  for that one message, attached to the existing thread in our store via
  `threadId`. No re-fetch of unchanged messages, and we get label/delete events
  for free. This is the only path that mirrors Gmail's mutable state correctly
  — time-based queries (`after:T`) miss edits and re-fetch entire threads on
  any new reply.

The disk store being message-keyed makes both paths land in the same place:
backfill writes messages out of thread responses; incremental writes messages
out of history responses. Same rows, different producers.

### Storage model

- Canonical unit: **message**, keyed by Gmail message ID.
- Store layout under `~/.cache/nous/gmail/<account>/`:
  - `messages/<shard>/<message_id>.json` — parsed message (headers, body,
    thread_id, label_ids, internal_date). Sharded 2-char prefix to avoid
    100k+ files in one dir.
  - `raw/<shard>/<message_id>.eml` — optional, only if `format=raw` was used;
    the canonical RFC822 bytes for archival.
  - `index.json` — single-file index: `{messageId → {threadId, labels, date,
    subject, sender}}` for fast search/list without re-parsing every message.
  - `cursor.json` — `{historyId, lastSyncAt, accountEmail}`. The cursor is the
    contract with `users.history.list`.

Open question for design time: SQLite instead of JSON files? Faster index
queries, atomic updates, but adds a dependency. Defer until we feel the pain.

### Sync flow

Two modes, sharing the same store:

1. **Backfill** (cold start or gap-fill):
   - Use #000001's batched `threads.list` + `threads.get(format=full)` path.
   - Materialize each thread's messages into the store.
   - On first message of the run, capture `historyId` from the `threads.list`
     response (or from a cheap `getProfile` call) and write it to `cursor.json`
     **before** the bulk fetch starts — so any new mail arriving mid-backfill is
     picked up by the next incremental run.
   - Resumable: skip messages already present in `index.json`.

2. **Incremental** (warm sync):
   - Read `cursor.json`, call `users.history.list?startHistoryId=…`.
   - For each `messagesAdded` entry, batched `messages.get(format=full)`.
   - For `messagesDeleted` / `labelsAdded` / `labelsRemoved`, update the index
     in place (no re-fetch needed).
   - Update `cursor.json` to the new `historyId` only after all writes succeed.
   - If the API returns 404 (`historyId` too old, >7 days for some accounts),
     fall back to backfill of the affected window.

### New library surface

```go
// lib/gmail/store.go
type Store struct { /* path, index, cursor */ }
func OpenStore(account string) (*Store, error)
func (s *Store) Backfill(query string, opts BackfillOptions) error
func (s *Store) IncrementalSync() error
func (s *Store) Lookup(messageID string) (*Message, error)
func (s *Store) Search(query StoreQuery) ([]MessageSummary, error)
```

Plus `messages.list` / `messages.get` paths in `lib/gmail/gmail.go` (currently
the lib only speaks threads).

### CLI

- `gmail sync --account <email>` — incremental, default verb.
- `gmail backfill --account <email> --query <q>` — bulk pull into store.
- `gmail get <message_id> --account <email>` — read from store, fall back to
  API on miss.
- Existing `gmail search` / `gmail thread` continue to work; should be
  store-aware (read locally first if cursor present).

## Plan

- [ ] Decide storage format (JSON files vs SQLite) — start JSON, revisit if
      slow on 10k+ messages
- [ ] Implement `Store` open/close + index load/save with atomic rename
- [ ] Add `messages.list` / `messages.get` (single + batched) to `lib/gmail`
- [ ] `Store.Backfill` reusing #000001's batched threads path, materializing
      messages with skip-if-present
- [ ] Capture `historyId` cursor before bulk fetch starts
- [ ] `users.history.list` wrapper + `Store.IncrementalSync`
- [ ] 404 fallback path (cursor expired → backfill window)
- [ ] CLI wiring: `gmail sync`, `gmail backfill`, store-aware `gmail get`
- [ ] Test: backfill 100 threads → modify mailbox externally → incremental
      sync picks up the change, no full re-download
- [ ] Test: simulate cursor expiration, verify fallback
- [ ] Atlas entry on the storage layout

## Log
