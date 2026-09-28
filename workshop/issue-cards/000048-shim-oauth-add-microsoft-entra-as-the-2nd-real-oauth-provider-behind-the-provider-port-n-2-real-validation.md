---
id: '000048'
status: done
created: 2026-06-08
updated: 2026-06-11
estimate_hours: 5
actual_hours: 4.0
---

# shim(oauth): add Microsoft/Entra as the 2nd real OAuth provider behind the Provider port (n=2-real validation)

## Problem

nous#44 built `shim(google-oauth)` — the `Provider` port, the pure
`tokenResponse→Credential` core, the stateful `Fake`, and the explicit
consumer-POV state machine (`target: oauth-credential-lifecycle`). But it is
grounded against **one** real provider (Google). A single real backend lets
provider-specific quirks masquerade as the abstraction (the nous#44 process
finding: *build against ≥2 real providers + the fake at once*). The port and the
`S` machine are **Microsoft-ready by inspection** — `parseIDToken` is a
separable seam, `Conf` endpoints are injectable, and no `email_verified`
assumption leaks above `credentialFromToken` (M2 review confirmed) — but
"by inspection" is not "by grounding."

This is the OAuth analogue of nous#46 (gh's 2nd git-hosting provider): the
deliberate **n=2-real cross-provider validation**, filed separately to keep #44
shippable, mirroring #42→#46.
