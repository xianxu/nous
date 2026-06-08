---
id: 000050
status: working
deps: []
github_issue:
target: oauth-credential-lifecycle
created: 2026-06-08
updated: 2026-06-08
estimate_hours: 0.5
---

# atlas: shim conformance-grounding index (per-provider cert freshness ledger)

## Problem

Each shim's fake (`shim'(X)`) is grounded against the real provider by a
build-tagged conformance contract test that, when it PASSES, *certifies* the fake
hasn't drifted. But that grounding is only as trustworthy as it is *fresh* — a
six-month-old cert is grounding of unknown validity. Today the certs are scattered
(gh in `lib/gh/contract_real_test.go` + #42/#43 history; oauth in
`lib/provider/oauth/contract_real_test.go` + #49) with no single place that
answers "which shims are grounded against which providers, and when was each last
certified?"

## Spec

- A dedicated atlas page (`atlas/nous/shim-conformance-grounding.md`) that is **the
  index of one layer of grounding**: a per-(shim, provider) table with the
  conformance test, the Keychain grounding creds, the run command, the
  documented grounding boundary, and crucially **last-certified date + result**
  (the freshness ledger). Planned rows (gitlab/bitbucket #46, Microsoft #48)
  listed as pending.
- Frame it as the bisimulation/oracle layer of the shim pattern (R/M/S): the
  conformance test is where the fake-as-model meets reality; the date is the
  freshness of that grounding.
- Note the idea in the ariadne shim pensive (`../ariadne/workshop/pensive/2026-06-08-01-pensive-shim-state-machines.md`) as eventually part of the shim architecture pattern (a standing requirement, not just a nous artifact).

## Done when

- `atlas/nous/shim-conformance-grounding.md` exists with the gh (last cert 2026-06-06) + oauth (2026-06-08) rows and the freshness-ledger framing; linked from the related atlas pages.
- The ariadne shim pensive records "conformance-grounding index with last-run dates" as a candidate element of the pattern.

## Plan

- [ ] Write `atlas/nous/shim-conformance-grounding.md` (table + framing + cadence + planned rows).
- [ ] Cross-link from `atlas/nous/e2e-integration-testing.md` (and the two contract test headers if useful).
- [ ] Append the idea to the ariadne shim pensive (pattern direction).

## Log

### 2026-06-08

Filed from operator request after the oauth fake was certified against real
Google (#49): we should have a single atlas index of every shim's
conformance/grounding test with the last-run date, as the legible "grounding
layer" of the system, and fold it into the shim architecture pattern (ariadne#71)
eventually.
