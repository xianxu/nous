---
id: 000045
status: open
created: 2026-06-06
updated: 2026-06-06
estimate_hours:
github_issue:
---

# shim every nous external dependency + an integrated end-to-end mock harness (the deterministic-shell demo)

## Problem

nous#42 (gh) and nous#44 (Google OAuth) prove the `shim(X)`/`shim'(X)` pattern
*per service*. But nous touches several external services, and the real payoff —
the one the auto-mocking + deterministic-shell vision is about
(`brain/docs/vision/2026-05-19-01-pensive-auto-mocking-external-services.md`,
`2026-06-05-01-pensive-simulation-tests-from-product-description.md`) — is being
able to run a **full nous flow end-to-end with EVERY external faked**: no
network, no real accounts, no VM. That requires shimming the remaining services
and wiring them into one integrated harness.

This is the umbrella that turns "we did the pattern twice" into "the whole
external surface is mockable, demonstrated." It is also the concrete precursor to
the simulation-testing project (personas/workloads over the shims + virtual time).
