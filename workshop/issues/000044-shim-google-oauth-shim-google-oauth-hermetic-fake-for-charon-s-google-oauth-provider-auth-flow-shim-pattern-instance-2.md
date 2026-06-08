---
id: 000044
status: working
deps: []
github_issue:
created: 2026-06-06
updated: 2026-06-07
estimate_hours: 6
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

## Spec

Follow the nous#42 convention (`shim(gh)` is the reference instance):

- **`shim(google-oauth)`** — a provider-neutral OAuth port (owned by nous),
  surface = what charon actually uses: build the auth URL, **await the callback
  code**, exchange code→tokens, refresh, extract email from the ID token. A
  future non-Google OIDC provider should fit the same port.
  - `real` adapter = today's `google.go` (httpClient to Google + browser-open +
    local callback server).
  - `fake` adapter = in-memory: issues codes/access/refresh/ID tokens, models
    expiry + refresh rotation + ID-token email, and **short-circuits the
    redirect** (no browser — hands back a code directly). Fault injection:
    consent-denied, refresh-fails, expired/revoked token, email-not-verified.
  - `New(Conf)` / `NewFake(Conf)`; `Conf` opaque (client id/secret, endpoints,
    scopes).
- **Grounding caveat (differs from gh):** the dual-backend contract test can
  ground the **token + refresh** endpoints semi-automatically (with a
  pre-obtained refresh token in Keychain, like nous#43's account model), but the
  **interactive consent/browser leg cannot be headless**. Decide the honest
  grounding boundary: contract-test the token/refresh/userinfo surface against
  real Google; leave the consent click as a documented manual step. Record what
  is and isn't grounded (the nous#42 discipline: don't claim coverage the
  mechanism can't deliver).

## Done when

- `shim(google-oauth)` port + `real` adapter (the only thing that talks to
  Google) + stateful `fake`; charon's oauth consumers migrated onto the port.
- Hermetic tests for the auth/refresh flow through the fake (incl. the async
  callback short-circuit + fault cases).
- Dual-backend contract test grounding the token/refresh surface against real
  Google (boundary documented); fake certified to the fidelity charon exercises.

## Plan

- [ ] Inventory charon's actual Google-OAuth surface (auth, callback, exchange, refresh, email-from-ID-token) + consumers.
- [ ] Define the port + `Conf`; settle the async-callback shape (channel/await inside the adapter).
- [ ] `real` adapter: move `google.go` behind the port unchanged.
- [ ] `fake` adapter: in-memory token/refresh/ID-token model + callback short-circuit + fault knobs.
- [ ] Migrate consumers; hermetic flow tests.
- [ ] Dual-backend contract test (token/refresh vs real Google); document the consent-leg grounding boundary.

## Log

### 2026-06-06

Filed as instance #2 of the shim(X) pattern (ariadne#71, which now `deps:` on
this). gh (nous#42) proved the pattern + grounding; this proves it generalizes,
and stresses the async-callback case + a harder grounding boundary (interactive
consent can't be headless). Lives in nous (the gateway to external services).
