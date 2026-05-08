---
id: 000004
status: working
deps: [nous#3]
created: 2026-05-05
updated: 2026-05-07
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

- [x] Stand up a Syncthing test between two machines on a throwaway folder; characterize: latency, conflict-file format, peer connection model. — Done via host + tart `scratch` VM peers; ~30s cold sync, ~10–15s steady-state, conflict files named `<base>.sync-conflict-<YYYYMMDD>-<HHMMSS>-<peer-id-prefix>.<ext>`.
- [x] ~~Stand up a git + auto-sync daemon prototype on the same folder; characterize the same axes.~~ — Skipped at spike time (Method B sketch). Decision flipped to git+daemon based on agent-driven-workflow analysis; daemon implementation lives in M2.
- [x] **Decide for the family case → git+daemon (revised 2026-05-07).** Original recommendation was Syncthing; flipped after weighting the four-writer reality (wife + her agent + me + my agent). Rationale + tradeoff + daemon outline in `brain/atlas/sync-substrate-decision.md`.
- [x] Write down the behavioral semantics: file-level conflict resolution only (never content-merge); first-pushed-wins; loser written as `<file>.conflict-<peer>-<YYYYMMDDTHHMMSSZ>.<ext>`; both peers see both files; manual resolve = read-decide-replace. Documented in the atlas decision doc.

### M2 — brain-sync daemon (commit-driven)

- [x] Build `cmd/brain-sync` (Go, single binary, charon-pattern CLI). Bare `brain-sync` is the foreground watcher; `brain-sync service install/uninstall/start/stop/status` for launchd integration.
- [x] RefWatcher: fsnotify on each brain's `.git/refs/heads/main` (commits-as-events, not per-file edits — rebased after the M1 substrate flip).
- [x] File-level conflict resolution algorithm in `lib/brainsync/resolve.go` (loser → `<file>.conflict-<peer>-<utc>.<ext>`, soft-reset HEAD to origin/main, commit conflict files, retry up to 5 times).
- [x] Watch loop: RefWatcher events → push (with resolve+retry on rejection); periodic `git fetch` ticker → ff-only-pull if working tree clean.
- [x] Auto-discovery of shared brains under `$HOME/workspace/` (`mode: shared` in `.brain/config.md`); explicit `--brain` flags override.
- [x] ~~Pre-tool / post-tool hooks~~ — dropped after M1 substrate flip. Commits ARE the atomic sync unit; no per-edit hooks needed.
- [x] Local two-process integration test (`make nous-test-brain-sync`): bare + two clones, two brain-sync daemons, propagation + conflict scenarios. ~15s end-to-end. No VM dependency.

### M3 — conflict-file convention + manual resolve flow

- [x] Decide on the conflict-file naming convention. **Decided:** `<file>.conflict-<peer-id>-<YYYYMMDDTHHMMSSZ>.<ext>` (our own, not Syncthing's default — slightly cleaner timestamp format and embedded peer-id is human-readable rather than the Syncthing-style ID prefix). Documented in atlas decision doc.
- [ ] Synthetic conflict test: two peers edit the same file with one offline, reconverge, verify both versions are visible and resolvable. Run inside the tart VM as the second peer (same setup as M1).
- [x] Document the manual resolve-by-hand flow as the v1 fallback (read both, decide, replace canonical, delete conflict file, sync re-converges). This is the workflow until issue #5 lands. Documented in atlas decision doc.
- [x] Atlas entry: how shared-brain sync works, where conflict files appear, how to resolve. **Done** in `brain/atlas/sync-substrate-decision.md`.

### M4 — wife/me forcing-function dogfood

- [ ] Create a `brain-shared-family` repo (gcrypt'd to me + wife) and place the Paris trip plan in it.
- [ ] Both peers sync it; co-author the plan over ≥1 week of real use.
- [ ] Log every conflict that occurs and how it was resolved (informs whether issue #5 / #7 are needed and in what shape).

## Log

### 2026-05-05

- Issue spec'd from `brain/data/life/42shots/ideas/2026-04-28-01-pensive-collaborative-brain.md`. M2 of the shared-brain project.

### 2026-05-07 — M1 closed (substrate = Syncthing)

Method A prototype between host MacBook + tart `scratch` VM peers. Both running `syncthing 2.0.16` from Homebrew. Pairing flow validated (CLI device-add on each side); shared-folder offer auto-propagates to the second peer. Three test scenarios (host→VM, VM→host, simultaneous-edit-conflict) all behave per design.

Skipped the git+daemon prototype (Method B sketch in the decision doc). The prototype's purpose was to validate Syncthing's family-case fit; it did.

### 2026-05-07 — M1 revisited: substrate flipped to git+daemon

Same day, after writing up the M1 decision, the operator surfaced the **four-writer concern**: shared brain isn't "two humans typing" — it's wife + her agent + me + my agent, plus continuous Syncthing sync between. Agents read → think for tens of seconds → write; the working tree is mutating under them via Syncthing during that window. Stale-state agent writes are the failure mode.

Under that re-framing, the comparison shifts:
- Atomic commits matter more than they did in the human-only framing — agents need a coherent state to read and write against.
- Commit messages capture intent (`agent: summarize Day 3 itinerary`); filesystem mtimes don't. Genuinely useful retrospectively.
- `git log paris.md` is the right primitive for "who changed what when, with what intent." Syncthing has no answer.
- A bad agent edit is `git revert <sha>` away; under Syncthing it's a manual restore from a conflict file (if you got lucky and the bad version became the loser).

Latency penalty (~30–60s vs ~10–15s) is acceptable for async trip-planning pace. Daemon implementation cost (Go, ~500–1500 LOC) is real but one-time, and dovetails with `nous#5`'s need for per-edit history.

**Conflict resolution rule:** strictly file-level, never content-merge. Brain content is markdown-only by convention (code lives in code repos); auto-merging prose invents content; humans are the right resolver. Loser → conflict file; canonical → first-pushed-wins.

Decision doc updated with full Revisions section; M2 plan re-spec'd around the daemon outline; M3 conflict-file convention finalized (`<file>.conflict-<peer-id>-<YYYYMMDDTHHMMSSZ>.<ext>`).

Spike actual ~1.5h (test + write + flip + rewrite). M2 is next when there's a wife-laptop or test-loop ready to exercise the daemon.

### 2026-05-07 — M2 closed (brain-sync daemon shipped)

Eight-chunk plan executed end-to-end (`workshop/plans/000004-shared-brain-sync-daemon-plan.md`). Single-session ~3h focused work — well below the 5–12h estimate, helped by the merge-base-relative diff bug being the only real surprise (the original two-dot syntax in `Resolve()` made remote-only-changes incorrectly look like our changes; switched to three-dot `A...B`).

Implementation summary:

- **`cmd/brain-sync/`**: cobra-based CLI. Bare `brain-sync` runs the foreground watcher (charon `serve`-pattern). `brain-sync service install/uninstall/start/stop/status` for launchd integration. Auto-discovers shared brains under `$HOME/workspace/` if no `--brain` flags.
- **`lib/brainsync/`**: discovery, RefWatcher (fsnotify on `.git/refs/heads/`), git ops layer, file-level resolve algorithm, watch loop (commit events + 30s fetch ticker), service manager (darwin/launchd).
- **Tests**: 18 unit/integration tests across 5 `_test.go` files. Full bare+clone scenarios for resolve and watch behavior. Plus `scripts/test-brain-sync.sh` running two real brain-sync processes against a local bare repo, exercising propagation (~3s) and conflict resolution (~10s) end-to-end.

Bugs caught + fixed during execution:
1. RefWatcher initially watched `.git/refs/heads/main` directly, but that file doesn't exist until first commit. Switched to watching the parent directory, filtering events to `main`.
2. `git push origin` requires upstream tracking; explicit `git push origin main` works for first push and steady state.
3. Resolve used `git diff A..B` (symmetric two-dot), should have used `A...B` (three-dot, merge-base-relative). Manifested as remote-only changes being mis-categorized as local changes.
4. Test script's `wait` (no args) blocked on long-running brain-sync background jobs. Fixed with explicit PID waits.
5. Auto-discovery vs misconfigured brains: a `.brain/config.md` directory without `.git/` would crash RefWatcher. Now skips with a warning.

Ready for M3/M4 (synthetic conflict tests + wife/me dogfood) once `brain-shared-family` is provisioned.
