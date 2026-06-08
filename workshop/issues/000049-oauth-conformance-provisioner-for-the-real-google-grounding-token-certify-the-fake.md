---
id: 000049
status: working
deps: []
github_issue:
target: oauth-credential-lifecycle
created: 2026-06-08
updated: 2026-06-08
estimate_hours: 1
---

# oauth conformance: provisioner for the real-Google grounding token + certify the fake

## Problem

nous#44 wired the real-Google grounding test
(`lib/provider/oauth/contract_real_test.go`, `//go:build conformance`) but it
**skips** zero-config because no refresh token is in Keychain
(`nous-oauth-conformance-google`) — the grounding is *wired but uncertified*.

Unlike gh's conformance tokens (copy-pasteable GitHub PATs), a Google refresh
token can only be obtained through the OAuth **consent flow**, and it is bound to
the **issuing client** — so it must come from **charon's own OAuth client**
(a token from Google's OAuth Playground or any other client won't refresh under
charon's client_id/secret). There is no "paste a token" path; consent is
interactive (non-headless — the documented grounding boundary). So provisioning
needs a one-shot tool that runs charon's consent flow and stores the resulting
refresh token where the test reads it.

## Spec

- A small reusable provisioner `cmd/oauth-conformance-provision` that:
  - runs `oauth.NewGoogleProvider().Auth(...)` (charon's real consent flow —
    opens the browser; operator consents with a **throwaway** account),
  - extracts `cred.RefreshToken` (issued to charon's client) and the ID-token
    email, and
  - stores the token in Keychain `nous-oauth-conformance-google` via
    `security add-generic-password -U`.
  - `openid` scope only (enough to refresh + extract email); cert is **Refresh-
    only / read-only** (never Revoke), so the account is never mutated.
- Run the conformance test and **certify** the fake against real Google; record
  the certification (date + result) in the `oauth-credential-lifecycle` target's
  Revisions (the nous#42 grounding discipline).
- A `SKILL.md` documenting the provision → certify → re-cert(~monthly) loop, so
  it's the template for the nous#48 Microsoft analogue.

## Done when

- `cmd/oauth-conformance-provision` exists and (operator-run) stores a charon-
  client refresh token in `nous-oauth-conformance-google`.
- `go test -tags conformance ./lib/provider/oauth/ -run Contract_Real -v` **passes**
  against real Google (certifying the fake hasn't drifted on Refresh/CheckHealth).
- Certification recorded in the target Revisions; `SKILL.md` shipped.

## Plan

- [x] Build `cmd/oauth-conformance-provision` (consent → refresh token → Keychain).
- [x] Operator runs it with a throwaway account (interactive consent).
- [x] Run the conformance test; certify (PASS 2026-06-08, xiantester2003@gmail.com); record in target Revisions.
- [x] Ship `SKILL.md` (provision/certify/re-cert; MS template note for #48).

## Log

### 2026-06-08

Filed from the nous#44 leftover (real-Google cert was wired but uncertified —
needs a charon-client refresh token in Keychain, obtainable only via the
interactive consent flow). Operator chose the fresh-throwaway-account route
(clean, dev's real account off the cert path, mirroring gh's #43 throwaway
accounts). The provisioner is reusable for ~monthly re-cert and the template for
the nous#48 Microsoft provisioner.
