---
id: 000048
status: working
deps: [nous#44]
github_issue:
target: oauth-credential-lifecycle
created: 2026-06-08
updated: 2026-06-08
estimate_hours: 5
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

## Spec

Add Microsoft identity platform (Entra ID) as a second `real` adapter behind the
**same** `Provider` port + `Conf`, satisfying the **same** `S`
(`oauth-credential-lifecycle`). The state machine is the invariant; only the wire
+ payload differ. Concretely vary (the per-provider seam, not the machine):

- **Endpoints** — tenant-scoped `login.microsoftonline.com/{tenant}/oauth2/v2.0/
  {authorize,token}`; the tenant dimension folds into `Conf.AuthURL`/`TokenURL`.
- **Refresh-token rotation** — Microsoft **always** rotates (single-use refresh
  tokens). `applyRefresh` already handles always-rotate from the same branch;
  the fake's `SetRotateRefreshTokens(true)` already models it — confirm against
  real MS.
- **Identity-claim extraction** — MS has **no reliable top-level `email`** and
  **no `email_verified`**; the stable identity is `preferred_username` / `upn`.
  This forces `parseIDToken` (or `credentialFromToken`) to take a per-provider
  identity extractor instead of the hardcoded Google email+verified logic. This
  is the litmus the nous#44 design anticipated — the point where the lifecycle/
  payload seam stops being "by inspection."
- **`offline_access` scope** (not `access_type=offline`) to obtain a refresh
  token; incremental-consent model differs → `buildAuthURL` gains a per-provider
  dialect (or a small strategy).
- **Revoke** — MS has no RFC 7009 token-revoke endpoint; revocation is
  `revokeSignInSessions` via Graph or the account portal. Port method generalizes;
  implementation + grounding boundary differ.
- **MS app registration** — obfuscated client id/secret (or public-client/PKCE),
  a test Entra account, and a Keychain-stored MS refresh token for grounding.

Build **Google-real + Microsoft-real + the fake at once** so quirks can't
masquerade as the abstraction. Whatever port/`Conf`/`S` change MS forces *is* the
cross-provider validation finding — record it.

## Done when

- A Microsoft `real` adapter implements the `Provider` port (same `Conf` shape),
  satisfies the same `S`, and the `Fake` certifies to both Google and Microsoft.
- The per-provider seam is factored as far as MS *actually* forces (likely: an
  injected identity extractor + `buildAuthURL`/revoke dialect) — and **no shared
  cross-service framework** (ariadne#71 rule); each provider stays its own `Conf`.
- A dual-backend contract grounds Refresh/CheckHealth against real Microsoft
  (Keychain refresh token), with the MS consent + revoke boundary documented.
- The port/`S` adjustments MS forced are recorded back to
  `oauth-credential-lifecycle` (a `## Revisions` note) and ariadne#71 (the
  cross-provider evidence the abstraction holds at n=2-real).

## Plan

Durable plan: `workshop/plans/000048-shim-oauth-microsoft-entra-plan.md` (designed via
superpowers-writing-plans; fresh-eyes plan review: APPROVED). Decision (operator,
2026-06-08): public-client + **PKCE**, single-tenant Entra app (`xian@xldigit.com`'s
`xldigit.com` tenant); certify against real Microsoft this session. Architecture: one
generic `OIDCProvider` + injected per-provider `dialect` (ARCH-DRY); Google/Microsoft
as sibling provider files over a shared OIDC core; identity-extractor is the seam MS
forces; PKCE + `offline_access` scope + no-token-revoke + always-rotate are the other
MS dialect dimensions.

- [x] M1 — Factor the per-provider seam (identity extractor + `dialect`, rename to `OIDCProvider`) + Microsoft adapter (PKCE, `preferred_username`, `offline_access`, `ErrRevokeUnsupported`) + dialect-aware `Fake` certifying the `S` contract under both Google and Microsoft dialects (hermetic n=2). All hermetic tests green; Google behavior unchanged.
- [x] M2 — Real-Microsoft grounding: generalize the conformance provisioner (`-provider microsoft`), `TestContract_RealMicrosoft` with rotated-token persistence (MS single-use), certify against real MS this session; record the n=2 findings → `oauth-credential-lifecycle` Revisions + ariadne#71.

## Log

### 2026-06-08
- 2026-06-08: closed M1 — go test ./lib/provider/oauth/ green: one OIDCProvider+dialect; Microsoft adapter (PKCE/preferred_username/offline_access/ErrRevokeUnsupported); dialect-aware Fake certifies the S contract under BOTH Google & Microsoft dialects (TestContract_Fake + TestContract_FakeMicrosoft); MS wire grounded vs httptest (verifier+no client_secret+rotation); Google behavior unchanged (existing tests pass).
- 2026-06-08: M1 boundary review = **FIX-THEN-SHIP** (info; no Critical/correctness). Fixed both Important: atlas drift (`atlas/nous/oauth-health.md` `*GoogleProvider`→`*OIDCProvider`) + removed a stray tracked `oauth-conformance-provision` binary (+.gitignore). gofmt'd oidc.go/token.go/token_test.go. Lessons recorded (rename sweeps must include atlas/docs; no build artifacts in `git add -A`). Deferred minors in lessons.md.

### 2026-06-11
- 2026-06-11: **Real-Microsoft grounding CERTIFIED.** Operator stood up a single-tenant public-client Entra app (client `a49b2de2…`, tenant `6daae80a…`, account `xian@xldigit.com`, PKCE/no-secret). Provisioner minted+stored a real refresh token; `go test -tags conformance ./lib/provider/oauth/ -run Contract_RealMicrosoft` **PASSED twice in a row** — confirming the `rtCapture` rotated-token write-back makes grounding repeatable despite MS single-use rotation. The fake matches real Microsoft on Refresh + CheckHealth. n=2-real grounded (not just designed). Recorded in the target Revisions; cross-provider evidence in ariadne#71.

Filed from the nous#44 M3 scope decision (operator: 2nd OAuth provider lands as a
separate follow-up, symmetric to nous#46 for gh, not bundled into #44). The
nous#44 design kept the port/S Microsoft-ready by inspection (separable
`parseIDToken` seam, injectable `Conf` endpoints, `email_verified` guard below
the seam); this issue is the grounding that turns "by inspection" into evidence,
and the identity-claim extractor is the seam MS will force into existence. State
machine + framing: `target: oauth-credential-lifecycle` +
`ariadne/workshop/pensive/2026-06-08-01-pensive-shim-state-machines.md`.

Precise seam to factor (from the nous#44 M3 SHIP review): `credentialFromToken`
(`lib/provider/oauth/token.go`) currently hardcodes **both** `Provider: "google"`
**and** the email-verified guard. The MS adapter parameterizes it by provider-id +
an injected identity-claim extractor (Google: `email`+`email_verified`; MS:
`preferred_username`/`upn`, no verified claim) — that parameterization *is* the
"one-function swap" the target promises, not a copy of `credentialFromToken`.
Also: the manual real-Google cert (nous#44) should assert the *consent exchange*
yields a credential (not just that Refresh works), since the `email_verified`
guard sits on the ungrounded `Auth`/`exchangeCode` path.
