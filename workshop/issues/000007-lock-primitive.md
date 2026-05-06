---
id: 000007
status: open
deps: [nous#4]
created: 2026-05-05
updated: 2026-05-05
estimate_hours: 4
---

# lock primitive

## Done when

- A lock file convention exists at `.brain/locks/<path>.lock` and is respected by the sync daemon (issue #4) and editor / agent integrations.
- Manual locks work: a human can mark a file as "I'm editing this" and other peers / agents see the indicator and back off.
- Conflict-induced locks work: when issue #5's `/brain-resolve` is mid-resolution on a file, peers don't race to also resolve it.
- Locks are optimistic and self-healing: writing a lock then pushing/syncing claims it; expired or stale locks are detectable and overrideable with confirmation.

## Spec

Source: `brain/data/life/42shots/ideas/2026-04-28-01-pensive-collaborative-brain.md` §Locking.

This is a **deferred** primitive. The pensive's own analysis: "for two people who talk daily, [the lock primitive] obviates entirely." If issue #4 (sync) plus issue #5 (semantic merge) handle the real-world conflict rate gracefully, locks may not need to ship. The right time to start this issue is *after* dogfooding #4/#5 in the wife-trip-planning forcing function reveals friction that locks would cure.

The pensive's design — kept here so the issue is ready to execute when the moment comes:

- **Default is optimistic, no lock.** Locks are a brake that engages when the cost of losing work spikes.
- **One mechanism, two triggers.** Same on-disk representation (`.brain/locks/<path>.lock`), synced through the same channel as content. Two ways to acquire:
  - *Conflict trigger:* when `/brain-resolve` starts work on a file, it auto-acquires the lock. Stays locked until resolution commits, then releases. Prevents the meta-failure of two peers' agents simultaneously merging the same conflict.
  - *Manual trigger with TTL:* a human says "I'm drafting this for an hour." Status indicates `[locked by self until 14:30]`. Auto-extends on activity, expires on inactivity, manual override possible (with confirmation) for stale locks.
- **Optimistic acquisition:** write the lock file, push/sync; if it lands first, you have it. Race-loss looks like the same conflict-file mechanism every other write uses, so there's nothing new to handle.

Out of scope: rich lock metadata (who, why, expected duration as separate fields). Lock content is just enough text for a human to understand who holds it and until when.

## Estimate

Range: **3–7 hr**. Best guess: **~4 hr**.

*Produced via `brain/data/life/42shots/velocity/estimate-logic-v2.1.md` against `baseline-v2.1.md`. Method A only.*

| Milestone | Primitive | Design (×0.5) | Impl (×1.0 familiarity) | Total |
|---|---|---|---|---|
| M1 — lock format + helpers | Smaller Go + frontmatter | 0.1–0.4 | 0.3–0.6 | 0.4–1 |
| M1 — synthetic race test | Verification | 0.1–0.3 | 0.3–0.8 | 0.4–1.1 |
| M2 — sync daemon respect | Cross-cutting refactor | 0.1–0.5 | 0.2–0.5 | 0.3–1 |
| M2 — agent dispatcher integration | Cross-cutting refactor | 0.1–0.5 | 0.2–0.5 | 0.3–1 |
| M2 — editor (parley.nvim) status line | Lua/Neovim feature | 0.5–1.5 | 0.5–1.5 | 1–3 |
| M3 — TTL + auto-acquire on conflict | Smaller Go + state machine | 0.15–0.5 | 0.3–0.6 | 0.45–1.1 |
| M3 — heartbeat from editor / agent | Smaller Go | 0–0.15 | 0.2–0.5 | 0.2–0.65 |
| **Subtotal** | | 1.05–3.85 | 2–4.5 | 3.05–8.35 |
| **+30% design buffer** | | +0.3–1.2 | n/a | +0.3–1.2 |
| **Total** | | | | **~3.5–9.5 hr** |

Rounded down to **3–7 hr** because the upper-band assumes editor integration (M2 nvim status line) is full Lua-feature work; in practice it's likely a status-line predicate, not a new pane. M2's Lua/Neovim primitive dominates the range. Heavy reminder: this issue is *deferred* — pensive's own analysis is that locks may not need to ship at all if `#5`'s semantic merge handles real conflict rates.

## Plan

### M1 — lock format + helpers

- [ ] Decide the on-disk format (a small markdown frontmatter blob is probably right: `holder`, `acquired`, `ttl_until`, optional `reason`).
- [ ] Read/write helpers in the nous library; cleanly handle missing-lock, stale-lock, self-held-lock, peer-held-lock.
- [ ] Synthetic test: two peers race a manual lock acquisition; observe optimistic behavior produces a clean winner via the conflict-file mechanism.

### M2 — sync daemon + agent / editor respect

- [ ] Sync daemon: when a write would land on a peer-locked file, surface the lock (don't silently overwrite). Local writes are not blocked — the human can override their own discipline if they want.
- [ ] Agent dispatcher: before tool-writes to a path, check the lock; if peer-held, defer or route to a human prompt.
- [ ] Editor integration (parley.nvim or whichever editor surfaces brain): show the lock state in the status line.

### M3 — TTL + auto-acquire on conflict

- [ ] Auto-acquire the lock at the start of `/brain-resolve` work; release on commit-or-abort.
- [ ] TTL handling: locks with `ttl_until` in the past are stale; offer override-with-confirmation rather than silent removal.
- [ ] Manual extend on activity (heartbeat from editor / agent).
- [ ] Document the full lifecycle in `atlas/`.

## Log

### 2026-05-05

- Issue spec'd from `brain/data/life/42shots/ideas/2026-04-28-01-pensive-collaborative-brain.md`. M5 of the shared-brain project, marked deferred. Not in MVP per the pensive's own observation that locks may obviate entirely with daily verbal coordination + good semantic merge (#5).
- Reorder note: originally the pensive listed lock as build-step 3 ahead of semantic merge (step 4). Flipped here because semantic merge is the experiment that tells us whether locks are needed at all.
