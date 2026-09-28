---
id: '000001'
status: done
created: 2026-04-28
updated: 2026-04-28
actual_hours: N/A
---

# Gmail: HTTP batch + pagination + bounded concurrency

## Problem

`lib/gmail/gmail.go` issues one HTTP request per thread metadata fetch via unbounded
goroutines (`SearchThreads`, line 75). **Already in practice this gets blocked by
Google for calling too fast.** The binding constraint is Gmail's per-user
**concurrent request** cap (undocumented, ~10–20 in practice) — error message
"Too many concurrent requests for user" — *not* the per-second quota
(250 units/s, ~50 `threads.get`/s). Empirically: unbounded fan-out at 1000
goroutines triggers it; bounded to 8 the same 1000 calls succeed.

The upcoming backfill use case (1k–10k threads per account, multiple accounts)
makes this dramatically worse. Two other gaps make the current code unfit for
backfill: `maxResults` is single-page (no `pageToken` loop, and `threads.list`
caps at 500 per page), and there is no concurrency cap, so nothing slows the
client down when Google pushes back.
