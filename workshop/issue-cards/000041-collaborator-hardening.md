---
id: '000041'
status: done
created: 2026-06-02
updated: 2026-06-02
estimate_hours: 9
actual_hours: 8
---

# collaborator-lifecycle hardening (codex review findings)

## Problem

A read-only codex design review of `workshop/targets/collaborator-state-machine.md`
(against the implementing code + threat model) surfaced 12 findings. #1 was a live
resurrection bug, already fixed (`f2cb9de`). The rest are real drift/edge gaps in
the per-brain collaborator lifecycle and are in scope for THIS testing round.
(Cross-brain ban list stays nous#37 — not here.)
