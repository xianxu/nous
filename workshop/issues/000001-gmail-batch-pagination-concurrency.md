---
id: 000001
status: working
deps: []
created: 2026-04-28
updated: 2026-04-28
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

## Spec

Make `lib/gmail` capable of a 10k-thread backfill in one invocation without
hitting 429s and without N round-trips. Three additions, narrowly scoped:

1. **HTTP batch.** New helper `apiBatch(account, scope, requests)` that POSTs a
   `multipart/mixed` body to `https://gmail.googleapis.com/batch/gmail/v1` and
   parses the multipart response back into per-request results. Cap each batch
   at 100 sub-requests (Gmail's hard limit). Use this in `SearchThreads` to
   replace the goroutine fan-out for the metadata fetches; chunk by 100.

2. **Pagination.** `SearchThreads` accepts a target count that may exceed one
   page. `threads.list` caps at 500 results per page, so any `--max-results
   > 500` requires a `pageToken` loop. Loop until target reached or no more
   pages. Use `maxResults=500` per page (the cap) to minimize round-trips.

3. **Bounded concurrency + retry with backoff.** Even with batching, multiple
   batches run in parallel (e.g. backfill of 5000 threads = 50 batches). Cap
   parallel in-flight batches with a semaphore. **Default 8** — empirically
   validated against Gmail's per-user concurrent cap; runs at ~25 req/s with
   ~0.3s metadata calls, well under the 250 units/s quota. Tunable up to ~16–20
   for power users; configurable by caller. On `429` or `5xx` from Gmail (whole
   batch or sub-request), retry the affected request with exponential backoff
   + jitter, capped at a few attempts. The semaphore is load-bearing — it
   prevents the cap from being tripped in the first place; backoff is
   defensive for the residual.

Out of scope (deferred to #000002): on-disk storage, resume-on-failure,
`messages.list` / `messages.get`, incremental sync via `historyId`.

### API shape

`SearchThreads` signature stays the same for the small-N case. For backfill,
add a sibling that takes a target count and returns a stream/slice:

```go
// existing — unchanged externally
func SearchThreads(account, query string, maxResults int) ([]ThreadSummary, error)

// new
type BackfillOptions struct {
    Query       string
    Target      int  // total threads desired across pages
    Concurrency int  // parallel batches; 0 = default 8
}
func BackfillThreads(account string, opts BackfillOptions) ([]ThreadSummary, error)
```

Internally both should share the same batched-metadata path. `SearchThreads` is
just `BackfillThreads` with `Target = maxResults`.

### CLI

`cmd/gmail/main.go` gains a flag on `search` (or a new subcommand) to drive the
backfill path. Defer the exact UX to implementation time — the question is
whether `--max-results` should auto-paginate past one page or whether backfill
is a separate verb.

## Plan

- [x] Add `pageToken` loop in `threads.list` (landed by sibling agent 2026-04-28)
- [x] Add semaphore-bounded concurrency over `threads.get` calls
      (concurrency=8, landed by sibling agent 2026-04-28)
- [ ] Sketch `apiBatch` in `lib/gmail/batch.go`: multipart writer for request,
      multipart reader for response, per-sub-request error surfacing
- [ ] Unit-test `apiBatch` against a fake server that echoes a fixed multipart
      response (no Charon dependency in tests)
- [ ] Refactor `SearchThreads` metadata fan-out to use `apiBatch` in chunks of 100
      (replaces the current N-goroutine loop, even though it's bounded now)
- [ ] Add exponential-backoff retry on 429 / 5xx, both at the batch level and
      per sub-request inside a batch response
- [ ] Decide: keep auto-paginating `SearchThreads`, or split into
      `BackfillThreads` + dedicated CLI verb (current code uses the former —
      may be sufficient)
- [ ] Manual verify: backfill 1k threads from a real account through Charon,
      compare result count and sample thread metadata against pre-batch behavior
- [ ] Update `cmd/gmail/SKILL.md` if CLI surface changes
- [ ] Update `atlas/` if a gmail entry exists; create one if not

## Log

### 2026-04-28 — Gmail rate-limit findings (from sibling diagnostic thread)

The binding constraint on the current tool is **per-user concurrent requests**,
not per-second throughput. Concrete observations:

| Limit                          | Cap                            | What it bites                                           |
|--------------------------------|--------------------------------|---------------------------------------------------------|
| Per-user concurrent requests   | undocumented (~10–20 observed) | bursty parallel callers — *what we hit*                 |
| Per-user quota units / second  | 250 / s                        | sustained throughput; `threads.get` = 5 units → ~50/s   |
| Per-user quota units / day     | 1,000,000,000                  | effectively unbounded for personal use                  |
| Per-project quota / minute     | 1,200,000                      | shared across all users; irrelevant here                |

- Unbounded version fans out 1000 goroutines on `threads.get` → 429 with
  "Too many concurrent requests for user".
- Bounded to 8 → same 1000 calls succeed, ~25 req/s observed (≈0.3s/call),
  10× headroom under the 250/s quota.
- Could likely raise to 16–20 for faster runs; 8 is a safe default.
- `threads.list` is a separate axis: 500 results per page is a hard cap, so
  pagination is *structurally* required for `--max-results > 500`, independent
  of the concurrency story.

Implication: the patch does two structurally different things —
**bound concurrency** (concurrency cap) and **paginate the list call**
(page-size cap). Both are independently necessary.

### 2026-04-28 — Pagination + concurrency landed (sibling agent)

`lib/gmail/gmail.go` `SearchThreads` rewritten:
- Pagination loop with `maxResults > 500` chunked into 500/page calls,
  trimmed to exactly `maxResults` at the end.
- `sem := make(chan struct{}, 8)` around the per-thread `threads.get` goroutine,
  matching the empirically-validated default.

What didn't land yet (still open):
- **HTTP batch** for `threads.get` metadata fetches. The current code is N
  bounded goroutines, so 1000 threads still mean 1000 individual HTTP
  round-trips through Charon (now serialized 8 at a time). Batching collapses
  this to ⌈N/100⌉ requests — the bigger latency win.
- **Retry/backoff on 429 / 5xx**. With concurrency=8 we don't trip the cap in
  practice, but residual transient failures still surface as hard errors.
- **`BackfillThreads` separate API or CLI verb**. The sibling agent went with
  auto-paginating `SearchThreads` — may be sufficient; revisit when batch lands.

Status moved from `open` → `working`.
