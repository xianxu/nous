# shim(oauth) — Microsoft/Entra as the 2nd real OAuth provider Implementation Plan

> **For agentic workers:** Consult AGENTS.md Section 3 (Subagent Strategy) to determine the appropriate execution approach: use superpowers-subagent-driven-development (if subagents are suitable per AGENTS.md) or superpowers-executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add Microsoft identity platform (Entra ID) as a second real adapter behind the existing `Provider` port, factoring the per-provider seam exactly as far as Microsoft forces, so the `oauth-credential-lifecycle` `S` machine is grounded at n=2-real instead of n=1.

**Architecture:** Keep **one** generic OIDC adapter (the current `GoogleProvider`, renamed `OIDCProvider`) and inject the per-provider variations as a `dialect` value (ARCH-DRY — the callback server, token HTTP exchange, refresh, and `S` machine are shared; only the wire/payload differs). Google and Microsoft become sibling provider files (`google.go`, `microsoft.go`) over a shared core (`oidc.go`). The identity-claim extractor is the seam Microsoft forces into existence; PKCE, the `offline_access` scope, and the absent token-revoke endpoint are the other Microsoft-specific dialect dimensions. The hermetic `Fake` certifies against **both** dialects; a build-tagged conformance test grounds Refresh/CheckHealth against real Microsoft.

**Tech Stack:** Go; `crypto/rand` + `crypto/sha256` for PKCE; `net/http` + loopback `net.Listener` (existing); macOS Keychain (`security`) for the conformance refresh token; Microsoft identity platform v2.0 endpoints.

**Architectural decisions worth citing in `## Log`:**
- **ARCH-DRY** — one adapter + injected `dialect`, not two parallel `GoogleProvider`/`MicrosoftProvider` structs. The only Google-specific things today are: the hardcoded `Provider:"google"` + email-verified guard in `credentialFromToken`, the auth-URL params in `buildAuthURL`, the RFC-7009 revoke, and `requiredGoogleScopes`. Each becomes a `dialect` member; everything else (Auth callback server, `exchangeCode`, `Refresh`, `applyRefresh`, `checkHealth`) stays shared.
- **ARCH-PURE** — the seam is split into pure parts (`decodeIDClaims`, `googleIdentity`, `microsoftIdentity`, `codeChallenge`, `mint*IDToken`, the `authParams` closures) that are unit-tested without IO, and a thin IO shell (the real adapter's HTTP + browser + `crypto/rand` verifier generation). PKCE's only impure piece is the random verifier; the challenge derivation is pure and tested against the RFC 7636 vector.
- **Not a cross-service framework** (ariadne#71) — `dialect` lives inside package `oauth` and varies only OIDC providers; each provider is still built from its own `Conf`. No abstraction spans `gh`/`oauth`.

---

## Core concepts

### Pure entities

| Name | Lives in | Status |
|------|----------|--------|
| `dialect` | `lib/provider/oauth/oidc.go` | new |
| `decodeIDClaims` | `lib/provider/oauth/token.go` | new |
| `googleIdentity` | `lib/provider/oauth/google.go` | new |
| `microsoftIdentity` | `lib/provider/oauth/microsoft.go` | new |
| `credentialFromToken` | `lib/provider/oauth/token.go` | modified |
| `buildAuthURL` | `lib/provider/oauth/token.go` | modified |
| `parseIDToken` | `lib/provider/oauth/token.go` | deleted |
| `mintIDToken` → `mintGoogleIDToken` | `lib/provider/oauth/google.go` | modified |
| `mintMicrosoftIDToken` | `lib/provider/oauth/microsoft.go` | new |
| `codeChallenge` | `lib/provider/oauth/pkce.go` | new |
| `googleDialect` | `lib/provider/oauth/google.go` | new |
| `microsoftDialect` | `lib/provider/oauth/microsoft.go` | new |

- **dialect** — the per-provider seam behind the `Provider` port: `providerID`, `requiredScopes`, `usePKCE`, `authParams(forceFresh) url.Values`, `extractID(idToken) (account, err)`, `mintID(account, verified) string`, `revoke(g *OIDCProvider, refreshToken string) error`.
  - **Relationships:** 1:1 with a provider; held by both `OIDCProvider` (real) and `Fake`. `extractID`/`mintID`/`authParams`/`requiredScopes`/`providerID` are shared by real+fake; `usePKCE`/`revoke` are real-adapter-only (the fake doesn't do PKCE wire or HTTP revoke).
  - **DRY rationale:** Eliminates the only Google-hardcoded points; without it, Microsoft means a second copy of Auth/Refresh/exchange.
  - **Future extensions:** A 3rd provider (GitLab/Okta) adds one `dialect` value + one `New*Provider`, no core change. Widening axes: a `userInfoURL` for providers whose id_token lacks the identity claim; a `scopeRewrite` for providers that rewrite scope strings (Google's `email`→URL behavior could migrate here later).

- **decodeIDClaims** — provider-neutral JWT-payload decode: split `header.payload.sig`, base64url-decode payload, `json.Unmarshal` into a caller-supplied struct. No signature check (token came straight from the issuer over HTTPS — unchanged trust assumption from `parseIDToken`).
  - **DRY rationale:** Both `googleIdentity` and `microsoftIdentity` decode the same JWT shape; only the claims they read differ. One decode, per-provider claim reads. Replaces `parseIDToken`'s decode body.

- **googleIdentity / microsoftIdentity** — the `extractID` implementations. Google: read `email`, reject `email_verified==false` (the guard **moves here** from `credentialFromToken` — it was always a Google-layer payload concern, invariant 4; Microsoft has no `email_verified`). Microsoft: read `preferred_username`, fall back to `upn`; no verified guard.
  - **Relationships:** Each is the `extractID` of its dialect.

- **credentialFromToken** (modified) — gains `providerID string` + `extractID func(string)(string,error)` params; drops the hardcoded `"google"` and the inline email-verified logic (now inside `googleIdentity`). Everything else (scope split, expiry) unchanged.

- **buildAuthURL** (modified) — signature changes from `(…, loginHint, forceFresh)` to `(…, loginHint string, extra url.Values)`. The provider-specific params (`access_type`/`prompt`/`include_granted_scopes` for Google; `prompt` for Microsoft) come from `dialect.authParams(forceFresh)`; PKCE params (`code_challenge`,`code_challenge_method`) are layered on by the real `Auth` per-call (they depend on a per-call verifier, not static dialect).

- **codeChallenge** (new, pure) — `base64url(sha256(verifier))`. Unit-tested against the RFC 7636 Appendix-B vector. The random `codeVerifier()` generator (impure, `crypto/rand`) lives beside it but is part of the thin shell.

### Integration points

| Name | Lives in | Status | Wraps |
|------|----------|--------|-------|
| `OIDCProvider` (was `GoogleProvider`) | `lib/provider/oauth/oidc.go` | modified | Google/MS token+revoke HTTP, browser, loopback callback |
| `NewMicrosoftProvider` | `lib/provider/oauth/microsoft.go` | new | MS v2.0 endpoints + public-client/PKCE construction |
| `microsoftRevoke` | `lib/provider/oauth/microsoft.go` | new | (honest no-op: MS has no per-token revoke) |
| `Fake` (dialect-aware) | `lib/provider/oauth/fake.go` | modified | in-memory issuer for both dialects |
| `rtCapture` | `lib/provider/oauth/contract_real_test.go` | new | Provider decorator capturing rotated refresh tokens |
| `TestContract_RealMicrosoft` | `lib/provider/oauth/contract_real_test.go` | new | real Microsoft token endpoint (build-tagged) |
| conformance provisioner (`-provider`) | `cmd/oauth-conformance-provision/main.go` | modified | interactive consent → Keychain |

- **OIDCProvider** — the renamed generic real adapter. Holds `dialect`. `Auth` generates a PKCE verifier when `dialect.usePKCE`, threads it through `exchangeCode`; `exchangeCode`/`Refresh` omit `client_secret` when empty (public client). `Revoke` delegates to `dialect.revoke`.
  - **Injected into:** consumers depend on the `Provider` port, not this type. Renamed type touches only comments in `lib/tui/*`, `lib/provider/proxy/*` and the constructor return type (no code spells `*oauth.GoogleProvider` as a field/param outside the package — verified by grep).

- **microsoftRevoke** — returns a clear `ErrRevokeUnsupported`-style error documenting that Microsoft has no RFC-7009 per-token revoke (only Graph `revokeSignInSessions`, which is global across all apps + needs elevated perms). The port method exists; the grounding boundary differs. This is one of the recorded n=2 findings.
  - **Injected into:** `microsoftDialect.revoke`.

- **Fake** — gains a `dialect` field. `Auth` mints the id-token via `dialect.mintID` and shapes via `credentialFromToken(tok, dialect.providerID, dialect.extractID, now)`; scope-merge uses `dialect.requiredScopes`; `SeedAccount`/auth set `Provider: dialect.providerID`. `NewFake(conf)` stays Google-dialect (back-compat); `newFake(conf, dialect)` is the internal dual-dialect constructor.
  - **Test surface:** `contract_test.go` runs `runOAuthContract` against the fake under **both** dialects.

- **rtCapture** — test-only `Provider` decorator: records the most recent non-empty refresh token returned by `Refresh`, and implements `CheckHealth` as `checkHealth(c.Refresh, cred)` so the probe's internal rotation is captured too. Microsoft rotates on **every** refresh (single-use), so after `runOAuthContract` the seeded Keychain token is dead and the only live token is the last rotation — the harness persists `rtCapture.last` back to Keychain. (Google's contract mustn't persist; Microsoft's must — a recorded n=2 finding.)

---

## Chunk 1: Milestone M1 — factor the seam + Microsoft adapter + hermetic dual-dialect fake

**M1 review boundary.** Autonomous. Done = `go test ./lib/provider/oauth/...` green; the `Fake` certifies `runOAuthContract` under both Google and Microsoft dialects; `microsoft_test.go` asserts the MS real-adapter wire (PKCE challenge, `offline_access`, no client_secret, rotation) against an `httptest` server; Google behavior unchanged.

### Task 1: Pure PKCE challenge

**Files:**
- Create: `lib/provider/oauth/pkce.go`
- Test: `lib/provider/oauth/pkce_test.go`

- [ ] **Step 1: Write the failing test** — RFC 7636 Appendix-B vector.

```go
package oauth

import "testing"

func TestCodeChallenge_RFC7636Vector(t *testing.T) {
	// RFC 7636 Appendix B canonical vector.
	verifier := "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	want := "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"
	if got := codeChallenge(verifier); got != want {
		t.Errorf("codeChallenge = %q, want %q", got, want)
	}
}

func TestCodeVerifier_Unique43Plus(t *testing.T) {
	a, b := codeVerifier(), codeVerifier()
	if a == b {
		t.Error("codeVerifier returned identical values")
	}
	if len(a) < 43 {
		t.Errorf("codeVerifier len = %d, want >= 43 (RFC 7636 §4.1)", len(a))
	}
}
```

- [ ] **Step 2: Run, expect FAIL** — `go test ./lib/provider/oauth/ -run 'CodeChallenge|CodeVerifier' -v` → undefined: codeChallenge.

- [ ] **Step 3: Implement.**

```go
package oauth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
)

// codeVerifier mints a PKCE code_verifier (RFC 7636 §4.1): 32 random bytes,
// base64url without padding → 43 chars, well within the 43–128 range. Impure
// (crypto/rand) — the thin IO shell of the otherwise-pure PKCE pair.
func codeVerifier() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// codeChallenge derives the S256 PKCE code_challenge from a verifier
// (RFC 7636 §4.2): base64url(sha256(verifier)), no padding. Pure.
func codeChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
```

- [ ] **Step 4: Run, expect PASS.**

- [ ] **Step 5: Commit** — `#48 M1: PKCE code_verifier/challenge (RFC 7636) pure pair`.

### Task 2: Generalize the JWT decode + identity extractor seam

**Files:**
- Modify: `lib/provider/oauth/token.go` (replace `parseIDToken` with `decodeIDClaims`; change `credentialFromToken` signature; change `buildAuthURL` signature)
- Modify: `lib/provider/oauth/token_test.go`

- [ ] **Step 1: Write/adjust failing tests** in `token_test.go` for the new shapes:

```go
func TestDecodeIDClaims_Google(t *testing.T) {
	var c struct {
		Email         string `json:"email"`
		EmailVerified any    `json:"email_verified"`
	}
	if err := decodeIDClaims(mintGoogleIDToken("a@b.com", true), &c); err != nil {
		t.Fatalf("decodeIDClaims: %v", err)
	}
	if c.Email != "a@b.com" {
		t.Errorf("email = %q", c.Email)
	}
}

func TestCredentialFromToken_UsesExtractorAndProviderID(t *testing.T) {
	now := time.Now()
	tok := tokenResponse{AccessToken: "at", RefreshToken: "rt", IDToken: mintGoogleIDToken("a@b.com", true), ExpiresIn: 3600, Scope: "openid email"}
	c, err := credentialFromToken(tok, "google", googleIdentity, now)
	if err != nil {
		t.Fatalf("credentialFromToken: %v", err)
	}
	if c.Provider != "google" || c.Account != "a@b.com" {
		t.Errorf("got provider=%q account=%q", c.Provider, c.Account)
	}
}

func TestBuildAuthURL_LayersExtraParams(t *testing.T) {
	u := buildAuthURL("https://x/auth", "cid", "http://localhost:1", []string{"openid"}, "hint@x", url.Values{"prompt": {"consent"}})
	if !strings.Contains(u, "prompt=consent") || !strings.Contains(u, "login_hint=hint%40x") {
		t.Errorf("auth url missing params: %s", u)
	}
}
```

- [ ] **Step 2: Run, expect FAIL** (undefined `decodeIDClaims`, signature mismatch).

- [ ] **Step 3: Implement in `token.go`:**
  - Add `decodeIDClaims(idToken string, into any) error` (the split+base64url+unmarshal body lifted out of `parseIDToken`; error on empty token / != 3 parts / bad base64 / bad json).
  - Delete `parseIDToken` (its email/verified logic moves to `googleIdentity` in Task 3).
  - `credentialFromToken(tok tokenResponse, providerID string, extractID func(string) (string, error), now time.Time)`: `account, err := extractID(tok.IDToken)`; on err wrap `"failed to identify account: %w"`; set `Provider: providerID, Account: account`; scope split + expiry unchanged. Update the doc comment: the provider id + the identity extractor are the per-provider concerns; the email-verified guard now lives in the Google extractor (record this in the target's Revisions in M2).
  - `buildAuthURL(authURL, clientID, redirectURI string, scopes []string, loginHint string, extra url.Values)`: build the base params (`client_id`,`redirect_uri`,`response_type=code`,`scope`), copy `extra` in, then `login_hint`. Remove the `access_type`/`prompt`/`include_granted_scopes` hardcoding (those move to `googleDialect.authParams`).

- [ ] **Step 3b: Retire the existing `token_test.go` tests that hit the deleted/changed surface** (same "none may be skipped" discipline as Task 3 Step 5):
  - `token_test.go:11,18` — `parseIDToken(...)` happy/unverified → rewrite onto `googleIdentity` (2-value; unverified now errors *inside* googleIdentity).
  - `token_test.go:26` — the `parseIDToken` invalid-format/bad-json error-path table → **migrate onto `decodeIDClaims`** (a new `TestDecodeIDClaims_Errors`: empty token, != 3 parts, bad base64, bad json) so the decode error coverage moves with the code instead of being dropped (the happy-path `TestDecodeIDClaims_Google` alone doesn't cover it).
  - `token_test.go:35,43` — `credentialFromToken(tok, now)` (old 2-arg) → new 4-arg `credentialFromToken(tok, "google", googleIdentity, now)`.
- [ ] **Step 4:** Tests in this file fail to compile until Task 3 (they reference `mintGoogleIDToken`, `googleIdentity`). That's expected — Tasks 2–4 land as one compile unit. Run the full package test after Task 4.

- [ ] **Step 5: Commit** with Task 3+4 (one behavior-preserving refactor commit) — `#48 M1: factor identity-extractor + auth-URL seam out of the Google-hardcoded core (ARCH-DRY)`.

### Task 3: Google dialect + rename adapter to OIDCProvider

**Files:**
- Create: `lib/provider/oauth/oidc.go` (the generic adapter + `dialect` type — move the shared core out of `google.go`)
- Modify: `lib/provider/oauth/google.go` (keep only Google-specific: endpoints, obfuscated creds, scopes, `googleIdentity`, `mintGoogleIDToken`, `googleDialect`, `googleRevoke`, `defaultGoogleConf`, `NewGoogleProvider`)
- Modify: `lib/provider/oauth/port.go` (`New` → Google dialect; `var _ Provider = (*OIDCProvider)(nil)`)
- Modify: `lib/provider/oauth/google_test.go`, `health.go` (receiver rename), comments in `lib/tui/*.go`, `lib/provider/proxy/*.go`

- [ ] **Step 1:** Define in `oidc.go`:
  - `type dialect struct { providerID string; requiredScopes []string; usePKCE bool; authParams func(forceFresh bool) url.Values; extractID func(idToken string) (string, error); mintID func(account string, verified bool) string; revoke func(g *OIDCProvider, refreshToken string) error }`
  - `type OIDCProvider struct { … }` (the current `GoogleProvider` fields + `dialect dialect`).
  - Move `Auth`, `exchangeCode`, `Refresh`, `Revoke`, `waitForCallback`, `openBrowser`, `out` here as `*OIDCProvider` methods. Edits:
    - `Auth`: scope-merge uses `g.dialect.requiredScopes`; `extra := g.dialect.authParams(forceFresh)`; if `g.dialect.usePKCE { v := codeVerifier(); extra.Set("code_challenge", codeChallenge(v)); extra.Set("code_challenge_method", "S256") }` then pass `v` to `exchangeCode`; status message uses `g.dialect.providerID`.
    - `exchangeCode(code, redirectURI, verifier string)`: build `data`; `if g.clientSecret != "" { data.Set("client_secret", g.clientSecret) }`; `if verifier != "" { data.Set("code_verifier", verifier) }`; shape via `credentialFromToken(tok, g.dialect.providerID, g.dialect.extractID, time.Now())`.
    - `Refresh`: `if g.clientSecret != "" { data.Set("client_secret", …) }` (public-client safe); rest unchanged (`applyRefresh`).
    - `Revoke(rt)`: `return g.dialect.revoke(g, rt)`.
  - `newProvider(conf Conf, d dialect) *OIDCProvider` (sets fields from conf + dialect, Output default).
- [ ] **Step 2:** In `google.go` keep Google specifics; add:
  - `googleIdentity(idToken string) (string, error)` — `decodeIDClaims` into `{email string, email_verified any}`; empty email → `"no email claim in ID token"`; then the verified guard (string|bool tolerant via the existing type switch): **absent or `false` → reject** with `"id token email %q is not verified"` (absent maps to the bool zero-value `false`, preserving today's production behavior — invariant 4, security-relevant; do NOT relax to "absent = trusted"); verified → return email. (This is the guard relocated from `credentialFromToken`; the relocation is the only behavior change and it's a *tightening into one place*, not a loosening.)
  - rename `mintIDToken` → `mintGoogleIDToken` (unchanged body).
  - `googleRevoke(g *OIDCProvider, refreshToken string) error` — the current RFC-7009 form-POST body (uses `g.revokeURL`); keep `ErrAlreadyRevoked`.
  - `var googleDialect = dialect{ providerID: "google", requiredScopes: requiredGoogleScopes, usePKCE: false, authParams: googleAuthParams, extractID: googleIdentity, mintID: mintGoogleIDToken, revoke: googleRevoke }`
  - `func googleAuthParams(forceFresh bool) url.Values` — `{access_type:offline, prompt:consent}` + `include_granted_scopes` = `false` if forceFresh else `true` (the exact current semantics).
- [ ] **Step 3:** `port.go`: `New(conf Conf) *OIDCProvider { return newProvider(conf, googleDialect) }`; `var _ Provider = (*OIDCProvider)(nil)`. `NewGoogleProvider` unchanged (returns `New(conf)`).
- [ ] **Step 4:** `health.go`: rename receiver `(g *GoogleProvider) CheckHealth` → `(g *OIDCProvider)`. Update the stale `*GoogleProvider` comment mentions (grep `GoogleProvider` across the tree): `lib/tui/{scopes,health,model}.go`, `lib/provider/proxy/{serve,proxy}.go`, **and `lib/charoncli/gcp.go:188`** → "OIDCProvider"/"the OIDC adapter". (All call sites of `NewGoogleProvider` are `:=` type-inferred — no concrete-type field/param to change.)
- [ ] **Step 5: Full `google_test.go` migration** (enumerated — these all break at the Task 4 compile gate; none may be skipped):
  - **`TestParseIDTokenEmail` (lines 16–72)** → rename `TestGoogleIdentity`; call site `got, _, err := parseIDToken(tt.token)` → `got, err := googleIdentity(tt.token)` (two values). The error-path cases (empty/invalid-format/missing-email/empty-email/invalid-base64/invalid-json) still hold (now surfaced via `decodeIDClaims`+the email check). **Update the "valid token" fixture** (line 25) from `{"email":"test@gmail.com","sub":"123"}` to include `"email_verified":true` — under the relocated guard, absent `email_verified` now *correctly* errors, so the happy fixture must carry the verified claim. Optionally add a case `{"email":"x@y.com","email_verified":false}` → `wantErr: true` to pin the guard at the extractor.
  - **`TestBuildAuthURL_LoginHint` (lines 74–104)** → split: the two `login_hint` sub-tests migrate to the new arity `buildAuthURL(authURL, clientID, redir, scopes, "" /or hint/, googleAuthParams(false))`. The two `include_granted_scopes` sub-tests (lines 91–103) **move** to a new `TestGoogleAuthParams` (Task 3 Step 2 adds `googleAuthParams`): assert `googleAuthParams(true)["include_granted_scopes"]==["false"]` and `googleAuthParams(false)==["true"]`, plus `access_type=offline`+`prompt=consent` present. (The semantics moved out of `buildAuthURL` into the dialect — the coverage must move with them, not be dropped.)
  - **`exchangeCode` call sites** at `google_test.go:277` and `:298` → add the new 3rd arg: `gp.exchangeCode("the-code", "http://localhost:1234", "")` (empty verifier = Google, no PKCE).
  - **`mintIDToken` call sites** at `google_test.go:272` and `:293` → `mintGoogleIDToken`.
  - `New(Conf{...})` (lines 152/276/297) unchanged (still Google dialect). `TestMergeScopes`, `TestRequiredScopesIncluded` unchanged.
- [ ] **Step 6: Run** `go test ./lib/provider/oauth/...` → all green (behavior-preserving). Also `go build ./...`.
- [ ] **Step 7: Commit** — `#48 M1: one generic OIDCProvider + injected dialect; Google becomes a dialect (ARCH-DRY)`.

### Task 4: Microsoft dialect + adapter + hermetic wire tests

**Files:**
- Create: `lib/provider/oauth/microsoft.go`
- Create: `lib/provider/oauth/microsoft_test.go`

- [ ] **Step 1: Write failing wire tests** (`microsoft_test.go`) against an `httptest` token server — mirror `google_test.go`'s real-adapter tests. **Note on where PKCE is asserted:** the `code_challenge` only ever appears on the *real* `Auth`'s per-call URL (it's layered on in `Auth`, not in `buildAuthURL` and not in the fake — see Notes). The real `Auth` opens a browser, so it isn't hermetically driveable. Therefore the PKCE *challenge derivation* is covered by `codeChallenge`'s unit test (Task 1), and the PKCE *verifier on the wire* is covered by the exchange test below. The auth-URL test asserts only the dialect's static params:
  - `TestMicrosoft_AuthParams_OfflineAccessPrompt`: assert `microsoftAuthParams(false)["prompt"]==["consent"]` and that `requiredMicrosoftScopes` contains `offline_access`+`openid`+`profile`. (Pure; no URL/browser.)
  - `TestMicrosoft_Exchange_SendsChallengeFreeVerifierNoSecret`: stand up `httptest.Server` returning a token JSON with an MS id_token (`mintMicrosoftIDToken`), point `tokenURL` at it, call `exchangeCode("code","redir","the-verifier")`, assert the server received `code_verifier=the-verifier` and **no** `client_secret` form field, and the credential has `Provider:"microsoft"`, `Account:` the preferred_username. (This is the hermetic proof the public-client + PKCE exchange wire is correct.)
  - `TestMicrosoft_Refresh_RotatesNoSecret`: server returns a new refresh token; assert `applyRefresh` rotates and no `client_secret` sent.
  - `TestMicrosoftIdentity_PreferredUsernameThenUPN`: `microsoftIdentity(mintMicrosoftIDToken("u@x.com", false))` → `"u@x.com"`, no verified guard; a token with only `upn` falls back to upn; empty → err.
  - `TestMicrosoftRevoke_Unsupported`: `microsoftRevoke(p, "rt")` returns `ErrRevokeUnsupported`.

- [ ] **Step 2: Run, expect FAIL** (undefined).

- [ ] **Step 3: Implement `microsoft.go`:**
  - Endpoints: `func microsoftAuthURL(tenant), microsoftTokenURL(tenant) string` → `https://login.microsoftonline.com/{tenant}/oauth2/v2.0/{authorize,token}`.
  - `var requiredMicrosoftScopes = []string{"openid", "profile", "offline_access"}` (openid+profile → `preferred_username`; offline_access → refresh token).
  - `microsoftIdentity(idToken string) (string, error)` — decode `{preferred_username, upn}`; return preferred_username else upn; empty → `"no preferred_username/upn claim in ID token"`. **No** verified guard (MS has no `email_verified`).
  - `mintMicrosoftIDToken(account string, _ bool) string` — JWT with `{"preferred_username": account}` (verified arg ignored; signature matches `dialect.mintID`).
  - `func microsoftAuthParams(forceFresh bool) url.Values` → `{"prompt": {"consent"}}` (offline_access rides in scope; MS does incremental consent natively — no `include_granted_scopes`).
  - `var ErrRevokeUnsupported = errors.New("microsoft has no per-token revoke endpoint (RFC 7009); revoke via Graph revokeSignInSessions or the account portal")`; `func microsoftRevoke(_ *OIDCProvider, _ string) error { return ErrRevokeUnsupported }`.
  - `var microsoftDialect = dialect{ providerID: "microsoft", requiredScopes: requiredMicrosoftScopes, usePKCE: true, authParams: microsoftAuthParams, extractID: microsoftIdentity, mintID: mintMicrosoftIDToken, revoke: microsoftRevoke }`.
  - `func NewMicrosoftProvider(clientID, tenant string) *OIDCProvider` → `newProvider(Conf{ClientID: clientID, AuthURL: microsoftAuthURL(tenant), TokenURL: microsoftTokenURL(tenant), DefaultScopes: nil}, microsoftDialect)` (no ClientSecret → public client; no RevokeURL).

- [ ] **Step 4: Run, expect PASS.**

- [ ] **Step 5: Commit** — `#48 M1: Microsoft/Entra dialect + adapter (PKCE, preferred_username, offline_access, no token-revoke)`.

### Task 5: Dialect-aware Fake + dual-dialect contract certification

**Files:**
- Modify: `lib/provider/oauth/fake.go`
- Modify: `lib/provider/oauth/contract_test.go`
- Modify: `lib/provider/oauth/fake_test.go`

- [ ] **Step 1: Write failing test** in `contract_test.go`:

```go
// TestContract_FakeMicrosoft runs the same S-machine contract against the
// in-memory fake under the Microsoft dialect — the hermetic half of n=2.
func TestContract_FakeMicrosoft(t *testing.T) {
	f := newFake(Conf{ClientID: "cid", AuthURL: "https://login.microsoftonline.com/t/oauth2/v2.0/authorize"}, microsoftDialect)
	cred := f.SeedAccount("user@xldigit.com", []string{"openid", "profile", "offline_access"})
	if cred.Provider != "microsoft" {
		t.Fatalf("seed provider = %q, want microsoft", cred.Provider)
	}
	runOAuthContract(t, f, cred)
}
```

And in `fake_test.go`, an assertion that the **Microsoft** fake has **no** unverified-email guard (the guard is Google-only):

```go
func TestFakeMicrosoft_NoVerifiedGuard(t *testing.T) {
	f := newFake(Conf{ClientID: "cid"}, microsoftDialect)
	f.SetAuthEmail("u@xldigit.com", false) // "unverified" is meaningless for MS
	cred, err := f.Auth("", []string{"openid"}, nil, false)
	if err != nil {
		t.Fatalf("MS Auth should not apply a verified-email guard: %v", err)
	}
	if cred.Account != "u@xldigit.com" {
		t.Errorf("account = %q", cred.Account)
	}
}
```

- [ ] **Step 2: Run, expect FAIL** (undefined `newFake`).

- [ ] **Step 3: Implement** in `fake.go`:
  - Add `dialect dialect` field. `newFake(conf Conf, d dialect) *Fake` sets it; `NewFake(conf Conf) *Fake { return newFake(conf, googleDialect) }` (back-compat).
  - `Auth`: scope-merge uses `f.dialect.requiredScopes`; `buildAuthURL(f.conf.AuthURL, f.conf.ClientID, redirectURI, allScopes, account, f.dialect.authParams(forceFresh))`; mint id-token via `f.dialect.mintID(email, f.verified)`; shape via `credentialFromToken(tok, f.dialect.providerID, f.dialect.extractID, f.now())`.
  - `SeedAccount`: `Provider: f.dialect.providerID`.

- [ ] **Step 4: Run** `go test ./lib/provider/oauth/...` → all green (both `TestContract_Fake` and `TestContract_FakeMicrosoft`).

- [ ] **Step 5: Commit** — `#48 M1: dialect-aware Fake; certify the S contract against both Google and Microsoft dialects (hermetic n=2)`.

### Task 6: M1 close

- [ ] **Step 1:** `go test ./... && go build ./...` green.
- [ ] **Step 2:** `sdlc milestone-close --issue 48 --milestone M1` (auto-dispatches the fresh-context boundary review; fix Critical/Important; log the `Review-Verdict:` outcome in `## Log`).

---

## Chunk 2: Milestone M2 — real-Microsoft grounding + record the n=2 findings

**M2 review boundary.** Operator-collaborative. Done = the conformance provisioner mints a real MS refresh token into Keychain; `TestContract_RealMicrosoft` PASSES against real Microsoft (Refresh + CheckHealth) with rotated-token persistence; the port/`S` adjustments Microsoft forced are recorded to `oauth-credential-lifecycle` (Revisions) and ariadne#71.

### Task 7: Generalize the conformance provisioner

**Files:**
- Modify: `cmd/oauth-conformance-provision/main.go`
- Modify: `cmd/oauth-conformance-provision/SKILL.md`
- Modify: `lib/provider/oauth/port.go` (add `ConformanceKeychainServiceMicrosoft`)

- [ ] **Step 1:** Add `const ConformanceKeychainServiceMicrosoft = "nous-oauth-conformance-microsoft"` in `port.go`.
- [ ] **Step 2:** In the provisioner add flags: `-provider google|microsoft` (default google), `-client-id`, `-tenant` (MS, from env `MICROSOFT_CLIENT_ID`/`MICROSOFT_TENANT_ID` if unset). Wire the **default** of the existing `-service` flag per provider: google → `ConformanceKeychainService`, microsoft → `ConformanceKeychainServiceMicrosoft` (resolve the default after parsing `-provider`, since flag defaults are static). For `microsoft`: build `oauth.NewMicrosoftProvider(clientID, tenant)`, request scopes `openid profile offline_access`. Keep the Google path identical. `keychainStoreArgs`'s arg derivation is unchanged (service is still passed in) — `main_test.go` stays green as-is.
- [ ] **Step 3:** Update `SKILL.md`: document the MS path — create the Entra app (the recipe from the plan), set `MICROSOFT_CLIENT_ID`/`MICROSOFT_TENANT_ID`, run `go run ./cmd/oauth-conformance-provision -provider microsoft`, consent with the throwaway account. Note MS rotates the refresh token on every use (so the conformance test persists the rotation back; re-provision only if the token goes stale beyond the 90-day window).
- [ ] **Step 4: Run** `go build ./... && go test ./cmd/oauth-conformance-provision/` green.
- [ ] **Step 5: Commit** — `#48 M2: conformance provisioner gains -provider microsoft (public-client/PKCE)`.

### Task 8: Microsoft grounding contract + rotated-token persistence

**Files:**
- Modify: `lib/provider/oauth/contract_real_test.go`

- [ ] **Step 1:** Add `rtCapture` — a `Provider` decorator with one field `last string` capturing the latest non-empty refresh token returned by `Refresh`; implement its `CheckHealth` as `checkHealth(c.Refresh, cred)` so the probe's internal rotation is captured too; `Auth`/`Revoke` delegate to the inner provider.
- [ ] **Step 2:** Add `TestContract_RealMicrosoft` (build tag `conformance`):
  - Read `MICROSOFT_CLIENT_ID`/`MICROSOFT_TENANT_ID` (else `t.Skip`).
  - Read refresh token from `$OAUTH_MICROSOFT_REFRESH_TOKEN` or, reusing the existing `keychainSecret` helper (contract_real_test.go:80), `keychainSecret(ConformanceKeychainServiceMicrosoft)`; else `t.Skip` with the provisioner hint.
  - `c := &rtCapture{inner: NewMicrosoftProvider(clientID, tenant)}`.
  - Seed `cred` (Expired) with the Keychain RT, `Provider:"microsoft"`.
  - `defer` persisting `c.last` back to Keychain via `security add-generic-password -U` **iff** non-empty (so re-runs work despite single-use rotation). Log clearly when it persists.
  - Add an MS-specific assertion **before** the shared body: one `c.Refresh(cred)` rotates the token (`fresh.RefreshToken != cred.RefreshToken` and non-empty) — the always-rotate invariant Google's contract can't assert. Re-seed `cred.RefreshToken = fresh.RefreshToken` so the shared body runs against the live (rotated) token, not the now-dead seed.
  - Run `runOAuthContract(t, c, cred)`.
  - Extend the file's GROUNDING BOUNDARY doc-comment for Microsoft: grounded = Refresh + CheckHealth (with rotation persistence); not grounded = consent leg (interactive), Revoke (no per-token endpoint at all on MS), `→Dead` (can't kill on demand).
- [ ] **Step 3: Run hermetic** `go test ./lib/provider/oauth/...` (the `conformance`-tagged file is excluded) → still green; `go vet -tags conformance ./lib/provider/oauth/` compiles.
- [ ] **Step 4: Operator certification (this session):** operator creates the Entra app, exports `MICROSOFT_CLIENT_ID`/`MICROSOFT_TENANT_ID`, runs `go run ./cmd/oauth-conformance-provision -provider microsoft` (browser consent with throwaway account), then I run `go test -tags conformance ./lib/provider/oauth/ -run Contract_RealMicrosoft -v` → expect PASS. Re-run once to confirm the rotated-token persistence makes it repeatable.
- [ ] **Step 5: Commit** — `#48 M2: ground Refresh/CheckHealth against real Microsoft; persist rotated RT (single-use) for repeatable certification`.

### Task 9: Record the n=2 findings

**Files:**
- Modify: `workshop/targets/oauth-credential-lifecycle.md` (append a `## Revisions` entry)
- Modify: ariadne `workshop/issues/000071-*.md` (locate in the peer ariadne repo; append the cross-provider evidence)

- [ ] **Step 1:** Append to the target's Revisions: the seam Microsoft forced = the **identity extractor** (`extractID` in `dialect`); the email-verified guard **moved** from shared `credentialFromToken` into `googleIdentity` (Google-only — invariant 4 now reads "Google-layer, inside the Google extractor"); **PKCE** + `offline_access`-scope + **no token-revoke endpoint** are the other MS dialect dimensions; **grounding must persist the rotated refresh token** for MS (single-use) where Google mustn't; `Revoke` is `ErrRevokeUnsupported` on MS. Update invariant 5's parenthetical to reflect the realized seam list, and the transition table's grounding column for MS where it differs (Revoke ungroundable on MS by mechanism, not just by destructiveness).
- [ ] **Step 2:** Append to ariadne#71: n=2-real for the OAuth shim is achieved; the abstraction held — one port + one `S` + per-provider `dialect`, no shared cross-service framework; the concrete cross-provider variations recorded above are the evidence the seam was real, not cosmetic.
- [ ] **Step 3: Commit** — `#48 M2: record n=2 cross-provider findings → oauth-credential-lifecycle Revisions + ariadne#71`.

### Task 10: M2 close → issue close

- [ ] **Step 1:** `go test ./... && go build ./...` green; conformance PASS captured.
- [ ] **Step 2:** `sdlc milestone-close --issue 48 --milestone M2` (fresh-context review; fix + log verdict).
- [ ] **Step 3:** `sdlc close --issue 48 --verified '<evidence: hermetic dual-dialect contract green; real-MS conformance PASS, repeatable via rotated-RT persistence; findings recorded>'` (let close compute `--actual`; atlas: the target Revisions + this plan cover the new surface — judge whether an `atlas/` entry is also warranted or `--no-atlas` with the why).

---

## Notes for the executor

- **Tasks 2–4 land as one compile unit** (the signature changes ripple); run the full package test after Task 4, not between 2 and 3.
- **Rename blast radius** (`GoogleProvider`→`OIDCProvider`) is only: the struct + its methods (oidc.go/health.go), the `var _` line, `google_test.go`, and comments in `lib/tui/*` + `lib/provider/proxy/*`. No external code holds the concrete type as a field/param (grep-verified) — they use the `Provider` port or `:=` off the constructor.
- **Public-client safety:** every token POST (`exchangeCode`, `Refresh`) must guard `client_secret` with `if g.clientSecret != ""`. Google keeps sending it (secret set); MS omits it (secret empty).
- **The fake never does PKCE or HTTP revoke** — those are real-adapter wire/security concerns, not `S` edges. The fake models states/transitions; PKCE is grounded by `codeChallenge`'s unit test + `microsoft_test.go`'s httptest exchange assertion.
