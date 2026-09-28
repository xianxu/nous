---
id: '000044'
status: done
created: 2026-06-06
updated: 2026-06-08
estimate_hours: 6
actual_hours: 1.60
---

# shim(google-oauth)+shim'(google-oauth): hermetic fake for charon's Google OAuth provider-auth flow (shim pattern instance #2)

## Problem

`lib/provider/oauth/google.go` runs the real Google OAuth flow against
`accounts.google.com/o/oauth2/auth` + `oauth2.googleapis.com/token`: opens a
browser, waits for a local redirect callback, exchanges the code for tokens,
extracts the authenticated email from the ID token, and refreshes tokens. There
is **no hermetic seam** — the auth + refresh flow can only be exercised against
real Google with a human clicking through consent. So charon's provider-auth
path (consumed by `lib/provider/proxy`, `lib/charoncli`, `lib/tui`) has no
automated coverage, exactly the gap nous#42 closed for GitHub.

This is **instance #2** of the `shim(X)`/`shim'(X)` pattern (ariadne#71). It
matters beyond coverage: it's the first instance with an **async redirect
callback** — the "a channel may appear *inside* an adapter for a push/event
service (e.g. an OAuth redirect callback)" case nous#42's spec explicitly
flagged. Proving the pattern holds here is what shows it generalizes past gh.
