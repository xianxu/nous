---
id: '000050'
status: done
created: 2026-06-08
updated: 2026-06-08
estimate_hours: 0.5
actual_hours: 0.03
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
