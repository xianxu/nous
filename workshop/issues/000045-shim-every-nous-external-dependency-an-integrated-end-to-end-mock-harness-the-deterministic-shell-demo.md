---
id: 000045
status: open
deps: [nous#42, nous#44]
github_issue:
created: 2026-06-06
updated: 2026-06-06
estimate_hours:
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

## Spec

### Inventory (2026-06-06) — external services nous integrates

| Service | Where | Shim status |
|---|---|---|
| GitHub (control plane) | `lib/gh` | ✅ nous#42 (port + fake + certified) |
| Google OAuth | `lib/provider/oauth/google.go` | → nous#44 |
| AI-provider proxy upstreams (Anthropic, Google generative, …) | `lib/provider/proxy` | **this issue** |
| Gmail API | `lib/gmail`, `cmd/gmail` | **this issue** |
| gpg / gpg-agent | `lib/agent`, `lib/identity`, `lib/brain` | assess (today: real gpg on tmpdir GNUPGHOME — may already suffice) |
| git (data plane) | everywhere | real git on `file://` tmpdir — git-is-git, no shim |
| OS execs (security/Keychain, launchctl, open, notify) | various | environment, not services — thin seams at most, out of scope |

### Work

1. **Shim the remaining services** following the convention (port + `real`
   adapter + stateful `fake`, `New(Conf)`/`NewFake(Conf)`, dual-backend contract
   grounding where a real backend is reachable):
   - **provider-proxy upstreams** — the AI APIs charon proxies (response shapes,
     rate-limit headers, error/SSE streaming shapes). The fake is a process-level
     upstream the proxy talks to unchanged.
   - **Gmail** — the API surface `lib/gmail`/`cmd/gmail` uses.
   - **gpg/gpg-agent** — decide: keep real-on-tmpdir (current, works) or add a
     fake; document the call.
2. **Integrated end-to-end mock harness** — one hermetic test that wires ALL the
   fakes together and runs a representative cross-service flow with zero external
   dependence, e.g.: provision/onboard a shared brain (gh fake) → authenticate a
   provider (oauth fake) → proxy an AI request (provider-upstream fake) →
   notify/share (gmail fake), asserting the flow round-trips. This *is* the
   deterministic shell extended to cover nous's whole external surface.

### Likely promotion to a project

"Shim all externals + the harness" is multi-service and multi-milestone. When
started, promote to a `project` (brain) with a sub-issue per service shim + one
for the harness; nous#42 and nous#44 become the first two entries. Keep this
issue as the umbrella/spec until then.

## Done when

- Every external **service** in the inventory has a `shim(X)`/`shim'(X)` pair
  (or a documented decision not to, e.g. git/gpg).
- An integrated e2e mock harness runs a representative multi-service nous flow
  fully hermetically (no network/accounts/VM) in CI.
- The shim(X)/shim'(X) convention is demonstrably the default across nous's
  external surface — the evidence ariadne#71 needs to promote it to architecture.

## Plan

- [ ] Confirm/extend the inventory; pick the harness's representative flow.
- [ ] Shim provider-proxy upstreams (process-level fake the proxy talks to).
- [ ] Shim Gmail to the used surface.
- [ ] Decide + document gpg/gpg-agent (real-on-tmpdir vs fake).
- [ ] Build the integrated end-to-end mock harness; wire all fakes; assert the cross-service flow.
- [ ] Promote to a brain `project` with per-service sub-issues when work begins.

## Log

### 2026-06-06

Filed at operator's request alongside nous#44: beyond proving the pattern twice,
demonstrate the **full** external surface is mockable via one integrated e2e
harness — the deterministic-shell demo. `deps:` on nous#42 (gh) + nous#44 (oauth)
since those instances are the foundation. ariadne#71 now `deps:` on this too, so
the pattern is promoted to architecture only after the whole surface is proven,
not just two instances.
