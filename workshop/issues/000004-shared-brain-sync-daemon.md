---
id: 000004
status: open
deps: [nous#3]
created: 2026-05-05
updated: 2026-05-05
estimate_hours: 8
---

# shared-brain sync daemon

## Done when

- A `brain-shared-family` repo (or equivalent shared subtree) syncs near-real-time between two machines without manual `git pull` / `git push`.
- Divergent edits land as conflict files on disk (one canonical version + one loser version, clearly named), discoverable by both peers.
- Both agent edits and hand edits are covered: a file modified in nvim or any other editor reaches the other peer without explicit user action.
- The trip-planning forcing function works end-to-end: `data/life/travel/2026-08-01-paris.md` (or its shared-brain equivalent) is co-authored by me and my wife with conflicts resolvable by reading both files and merging by hand.

## Spec

Source: `brain/data/life/42shots/ideas/2026-04-28-01-pensive-collaborative-brain.md` §Sync mechanism, §Build order step 2.

After issue #3 lands, brain repos are encrypted at rest. This issue makes them *collaborative*: a shared subtree where two (or more) people edit through their own agents and hand-tools, and edits propagate without ceremony. The forcing function is concrete — wife and I planning a summer Paris trip together. If sharing requires manual git ops, sharing stops happening.

Two viable substrates from the pensive; the choice is largely taste:

- **Git + auto-sync daemon.** Pre-tool hook → `git pull --rebase --autostash`; post-tool hook or filesystem watcher → commit + push. Hand-edits covered by a file-watcher daemon. Preserves git history end to end.
- **Syncthing on shared subtrees.** Peer-to-peer, no central server, E2E TLS by device-cert authentication, conflict files (`foo.sync-conflict-<date>-<peer>.md`) when both sides edit before reconciling. Designed exactly for "two laptops, one shared folder, no server." Loses git history on the synced subtree unless paired with a periodic snapshot job.

For the family case, Syncthing is closer to the desired feel; for repos where audit trail matters more (later: `brain-shared-42shots`), git + daemon may earn its keep. The behavioral semantics — server (or first-pushed) wins, loser written as a conflict file — should be the same so the resolution flow is uniform across substrates.

This issue ships the family-case substrate. It does not ship semantic merge (issue #5) or locking (issue #7); both build on top of the conflict-file convention this issue establishes.

## Estimate

Range: **5–12 hr**. Best guess: **~8 hr**.

*Produced via `brain/data/life/42shots/velocity/estimate-logic-v2.1.md` against `baseline-v2.1.md`. Method A primitives + Method B sketch on M1 (substrate spike).*

| Milestone / component | Primitive | Design (×0.5) | Impl (×1.5 familiarity) | Total |
|---|---|---|---|---|
| M1 — Syncthing prototype + characterize | Real-tool discovery + setup | 0.15–0.5 | 0.75–1.5 | 0.9–2 |
| M1 — git+daemon prototype + characterize | Greenfield (small) | 0.25–0.75 | 0.45–1.2 | 0.7–2 |
| M1 — Method B substrate decision (~5 unresolved decisions × 0.15 hr) | sketch | 0.5–1 | 0.15–0.3 | 0.65–1.3 |
| M2 — file-watcher daemon (fsnotify-based) | Greenfield Go single-concern | 0.25–1 | 0.45–1.2 | 0.7–2.2 |
| M2 — pre/post-tool hooks integration | Cross-cutting refactor | 0.1–0.5 | 0.3–0.75 | 0.4–1.25 |
| M3 — conflict-file convention + atlas | Atlas/docs | 0.05–0.2 | 0.075–0.3 | 0.125–0.5 |
| M3 — synthetic conflict test | Verification | 0.1–0.3 | 0.45–1.5 | 0.55–1.8 |
| M4 — provision brain-shared-family + dogfood (focused work only) | Setup + per-conflict resolve | 0.2–0.5 | 1–3 | 1.2–3.5 |
| **Subtotal** | | 1.6–4.75 | 3.6–9.75 | 5.25–14.5 |
| **+30% design buffer** | | +0.5–1.4 | n/a | +0.5–1.4 |
| **Total** | | | | **~5.75–16 hr** |

Rounded down to **5–12 hr** because the upper-bound stack assumes substrate decision is hard — likely it isn't (Syncthing is the obvious answer for the family case; the spike mostly characterizes rather than decides). M4 dogfood is wall-clock-heavy (~2 weeks per `done_when`) but focused-hour cost is small (~1–3 hr of per-conflict resolution). Familiarity ×1.5 across impl: fsnotify, Syncthing, and brain-runtime hook integration are all novel-but-bounded.

## Plan

### M1 — substrate decision spike

- [ ] Stand up a Syncthing test between two machines on a throwaway folder; characterize: latency, conflict-file format, what happens when one peer is offline, whether device-cert exchange is workable as a one-time setup.
- [ ] Stand up a git + auto-sync daemon prototype on the same folder; characterize the same axes.
- [ ] Decide for the family case (likely Syncthing). Document the tradeoff in `atlas/` so future repos with different needs (audit trail, larger contributor set) can pick differently with eyes open.
- [ ] Write down the *behavioral semantics* both substrates must implement: who-wins on divergence, conflict-file naming, where conflict files appear, how peers discover them.

### M2 — pre-tool / post-tool sync hooks

- [ ] Pre-tool hook: before any agent tool that writes to a shared brain path, ensure the working copy is fresh (pull or sync).
- [ ] Post-tool hook: after writes, push or sync.
- [ ] File-watcher daemon to cover hand edits (nvim, finder, anything not going through the agent).
- [ ] Wire into the nous agent runtime so shared-brain paths trigger the sync flow, private brain paths do not.

### M3 — conflict-file convention + manual resolve flow

- [ ] Decide on the conflict-file naming convention (Syncthing default vs. our own); document.
- [ ] Synthetic conflict test: two peers edit the same file offline, reconverge, verify both versions are visible and resolvable.
- [ ] Document the manual resolve-by-hand flow as the v1 fallback (read both, merge in editor, save, sync clears the conflict). This is the workflow until issue #5 lands.
- [ ] Atlas entry: how shared-brain sync works, where conflict files appear, how to resolve.

### M4 — wife/me forcing-function dogfood

- [ ] Create a `brain-shared-family` repo (gcrypt'd to me + wife) and place the Paris trip plan in it.
- [ ] Both peers sync it; co-author the plan over ≥1 week of real use.
- [ ] Log every conflict that occurs and how it was resolved (informs whether issue #5 / #7 are needed and in what shape).

## Log

### 2026-05-05

- Issue spec'd from `brain/data/life/42shots/ideas/2026-04-28-01-pensive-collaborative-brain.md`. M2 of the shared-brain project.
