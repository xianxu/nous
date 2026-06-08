---
id: 000044
status: working
deps: []
target: oauth-credential-lifecycle
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

Durable design: `workshop/plans/000044-shim-google-oauth-plan.md` (port surface =
union of charon's three real consumer interfaces; pure `tokenResponse→Credential`
core shared by both adapters; async callback stays adapter-internal).

- [x] M1 — Port (`Provider` interface) + `Conf`/`New`/`NewFake` + pure token-shaping core (`credentialFromToken`/`applyRefresh`/`parseIDToken`/`mintIDToken`); real adapter routed through it, behavior-preserving (`ARCH-PURE`/`ARCH-DRY`); `CheckHealth` → shared composite.
- [x] M2 — Explicit consumer-POV state machine (S) as a `target` (ariadne#71 finding); stateful `Fake` adapter executing S over the shared pure core, with fault knobs = S's named provider-autonomous transitions; hermetic flow tests (Auth short-circuit, Refresh, Revoke, CheckHealth, fault cases) + below-seam hermetic `waitForCallback`/token-exchange tests for the real adapter.
- [x] M3 — Migrate charon's oauth consumers onto the port; dual-backend contract test (Refresh/CheckHealth grounded vs real Google via Keychain; consent + Revoke documented as manual); atlas + grounding-boundary doc.

## Log

### Milestone review verdicts (fresh-context boundary reviews)

- M2 — **FIX-THEN-SHIP** (window 0f12c7e..HEAD). Two untested fault knobs + orphaned wrapper → fixed in b96d70d. Review ran during `sdlc milestone-close M2`.


- 2026-06-08: closed M3 — Migrated charon's oauth consumer onto the Provider port (tokenSupplierFromVault takes oauth.Provider); hermetic consumer test proves NewFake runs charon's GCP token path (refresh+persist) with no Google/browser. Dual-backend contract: TestContract_Fake (always) + TestContract_RealGoogle (//go:build conformance, Keychain-grounded, zero-config-skip). Atlas: e2e-integration-testing.md credential-lifecycle-simulation section + lib-layout.md oauth line + grounding boundary documented. go test ./lib/provider/oauth/... ./lib/charoncli/... ./lib/provider/proxy/... ./lib/tui/... green; go build ./... green. (Pre-existing lib/brain e2e FAILs are environmental: gpg-agent not running in sandbox — unrelated to this port refactor.) Real-Google certification is a one-time manual step pending a throwaway test-account Keychain token (plan open-q #1).; review verdict: SHIP
- 2026-06-08: closed M2 — Explicit S target (oauth-credential-lifecycle) + stateful Fake executing S over the shared pure core (model, not mock — mints tokenResponses through credentialFromToken/applyRefresh/parseIDToken); 10 hermetic fake tests (round-trip + 6 named provider-autonomous fault edges + expiry + consent-leg-modeled); below-seam real-adapter tests (waitForCallback code/error, exchangeCode HTTP grant-params + verified guard). go test -race ./lib/provider/oauth/ clean. Atlas deferred to M3 (full port+fake+grounding entry written together; S target already captures the state-machine surface in workshop/targets/).; review verdict: FIX-THEN-SHIP
- 2026-06-08: closed M1 — Behavior-preserving refactor: pure token-shaping core (token.go), Provider port + Conf/New (port.go), real adapter routed through pure core, CheckHealth->shared composite. go test ./lib/provider/oauth/ green; go build ./... green; proxy/charoncli/tui tests green. Existing tests pass unchanged except TestRefresh_PreservesSidecars updated to inject TokenURL via Conf. Atlas deferred to M3 (pure internal refactor, no operator-facing surface yet).; review verdict: FIX-THEN-SHIP
### 2026-06-06

Filed as instance #2 of the shim(X) pattern (ariadne#71, which now `deps:` on
this). gh (nous#42) proved the pattern + grounding; this proves it generalizes,
and stresses the async-callback case + a harder grounding boundary (interactive
consent can't be headless). Lives in nous (the gateway to external services).
