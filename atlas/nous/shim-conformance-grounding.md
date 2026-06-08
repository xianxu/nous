# Shim conformance grounding — the freshness ledger

This page is **the index of one layer of grounding** in nous: where each shim's
in-memory fake (`shim'(X)`) is checked against the *real* external provider, and
**when that check last passed**.

Every shim (ariadne#71: `shim(X)` port + `shim'(X)` fake) ships a build-tagged
**conformance contract test** that runs the *same* contract body against the real
provider that the fake runs against in-process. A PASS **certifies** the fake
hasn't drifted from reality on the grounded surface; a FAIL means the fake (or our
seam) drifted — fix the fake, not the test. In the R/M/S framing
(`workshop/targets/oauth-credential-lifecycle.md`, and the ariadne shim
state-machine pensive): the fake is our *model* `M` of the provider's hidden
machine `R`, and the conformance test is the **bisimulation oracle** that holds
`M` to `R`. The grounding is only as trustworthy as it is *fresh* — **a stale cert
is grounding of unknown validity**, which is why the last-certified date is
load-bearing, not bookkeeping.

These tests are `//go:build conformance`, never run in normal CI (they touch real
external services + credentials), zero-config-skip when creds are absent, and are
re-run **manually ~monthly or on suspected drift**. Each "Last certified" date
below is the last *passing* run; refresh by re-running that row's command.

## Ledger

| Shim | Provider | Conformance test | Run | Grounding creds (macOS Keychain) | Last certified | Result |
|------|----------|------------------|-----|----------------------------------|----------------|--------|
| `lib/gh` | **GitHub** | `lib/gh/contract_real_test.go` | `go test -tags conformance ./lib/gh/ -run Contract_Real -v` | `nous-conformance-operator`, `nous-conformance-invitee` (two throwaway accounts; ephemeral fixture repo) | **2026-06-06** | PASS (10/10 invariants; operator=emmatest42, invitee=yingtest42) — nous#43 |
| `lib/gh` | GitLab / Bitbucket | *(planned)* | — | — | — | pending (nous#46) |
| `lib/provider/oauth` | **Google** | `lib/provider/oauth/contract_real_test.go` | `go test -tags conformance ./lib/provider/oauth/ -run Contract_Real -v` | `nous-oauth-conformance-google` (throwaway refresh token; provision via `cmd/oauth-conformance-provision`) | **2026-06-08** | PASS (Refresh + CheckHealth) — nous#49 |
| `lib/provider/oauth` | Microsoft / Entra | *(planned)* | — | — | — | pending (nous#48) |

> Keep "Last certified" current: after a successful re-cert, update the cell here
> and record the run (date + result) in the relevant shim's grounding doc /
> target Revisions.

## What each row grounds (and doesn't)

Grounding has a **boundary** — each conformance test grounds only the surface the
mechanism can actually exercise non-destructively. The fakes cover the rest
hermetically; below-the-seam translation bugs are caught only by these real runs.
Don't claim coverage the mechanism can't deliver (the nous#42 discipline).

- **GitHub** (`lib/gh`): grounds the collaborator/invitation control-plane
  invariants against a live throwaway-owned repo (created + deleted per run).
  Details + the simulation worked-example: `atlas/nous/e2e-integration-testing.md`.
- **Google OAuth** (`lib/provider/oauth`): grounds `Expired→Active` (Refresh) +
  the `CheckHealth` read. The consent leg (`Auth` — non-headless), `Revoke`
  (destructive to the grounding token), and the provider-autonomous `→Dead` edge
  are **fake-only / manual** — the grounding column of the
  `oauth-credential-lifecycle` target's transition table *is* that boundary doc.
  Provision the token with `cmd/oauth-conformance-provision` (a Google refresh
  token is bound to charon's client, so it can't be a pasted PAT like gh's); see
  its `SKILL.md` for the provision→certify→re-cert loop.

## Why this is its own page

Grounding is the load-bearing defense for the below-seam bug class (the bugs the
fake is structurally blind to). Scattering "when did we last certify X?" across
test headers and issue history makes staleness invisible. One page, one ledger,
makes the freshness of the whole grounding layer legible at a glance — and as the
shim surface grows (nous#45 wants a shim for *every* external dependency), this
index is how we'll see which fakes are still anchored to reality and which have
drifted out of certification.

*Related: `atlas/nous/e2e-integration-testing.md` (the simulation + grounding
worked examples), `workshop/targets/oauth-credential-lifecycle.md` (the oauth S
machine + per-edge grounding), the ariadne shim state-machine pensive (the R/M/S
framing this index is the freshness layer of).*
