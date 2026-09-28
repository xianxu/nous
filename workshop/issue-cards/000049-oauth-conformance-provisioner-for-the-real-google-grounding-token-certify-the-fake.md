---
id: '000049'
status: done
created: 2026-06-08
updated: 2026-06-08
estimate_hours: 1
actual_hours: 0.11
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
