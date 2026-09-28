# shim(google-oauth) Implementation Plan

> **For agentic workers:** Consult AGENTS.md Section 3 (Subagent Strategy) to determine the appropriate execution approach: use superpowers-subagent-driven-development (if subagents are suitable per AGENTS.md) or superpowers-executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Put charon's Google OAuth provider-auth flow behind a provider-neutral port with a `real` adapter (the only thing that talks to Google) and a stateful in-memory `fake`, so the auth/refresh/revoke/health surface runs hermetically in tests — instance #2 of the ariadne#71 `shim(X)`/`shim'(X)` pattern (reference: nous#42 `shim(gh)`).

**Architecture:** Mirror `lib/gh`'s layout inside the existing `lib/provider/oauth` package: a `Provider` interface (the port) = exactly the union of what charon's three consumers use (`Auth`+`Revoke` from the TUI `Authenticator`, `Refresh` from the proxy `Refresher`, `CheckHealth` from charoncli). Extract the credential-shaping logic into **pure functions** that map a `tokenResponse → *vault.Credential` (shared by both adapters — `ARCH-PURE`/`ARCH-DRY`); the real adapter gets the `tokenResponse` from HTTP, the fake mints it in-memory. `CheckHealth` becomes a shared composite over `Refresh` (it already is, in `health.go`). The async redirect callback stays an adapter-internal detail (a channel inside `waitForCallback`); the port surface is a synchronous `Auth`, and the fake short-circuits the browser leg.

**Tech Stack:** Go, `net/http`/`net/url` (real adapter), `encoding/base64`+`encoding/json` (JWT ID-token mint/parse), `//go:build conformance` build tag + macOS Keychain (dual-backend grounding, same mechanism as nous#42/#43).

---

## Core concepts

### The port surface (derived from actual consumers, not Google's API)

Three consumers, three interfaces today; the port is their union:

| Consumer | File | Interface today | Methods |
|----------|------|-----------------|---------|
| TUI scope-apply / reauth | `lib/tui/scopes.go:46`, `model.go` | `Authenticator` | `Auth`, `Revoke` |
| proxy token refresh | `lib/provider/proxy/proxy.go:36` | `Refresher` | `Refresh` |
| charoncli health badge | `lib/charoncli/charoncli.go:366` | (concrete) | `CheckHealth` |
| charoncli GCP token supply | `lib/charoncli/gcp.go:187` | (concrete `*GoogleProvider`) | `Refresh` |

So the port is exactly: `Auth`, `Refresh`, `Revoke`, `CheckHealth`. Nothing more (no verbatim copy of Google's API — same discipline as nous#42, surface = what consumers use).

### The consumer-POV state machine (S) — the explicit spec the fake executes

Per the shim state-machine finding (`ariadne/workshop/pensive/2026-06-08-01-pensive-shim-state-machines.md`), the fake is an *executable model of the provider's hidden state machine*, and S — the lifecycle our code branches on — is authored as a **`target` artifact** (`workshop/targets/oauth-credential-lifecycle.md`, provider-neutral) that both adapters and the contract test reference. S is the neutral invariant that makes the port generalize to a 2nd provider (Microsoft); the wire + identity-claim extraction are the per-provider variant.

| From | Operation / event | To | Kind |
|------|-------------------|-----|------|
| `NoGrant` | `Auth` (consent→code→exchange) | `Active` | consumer-driven (async-callback sub-machine inside) |
| `NoGrant`/`Dead` | `Auth` consent denied | `NoGrant` | consumer-driven |
| `Active` | clock tick (access TTL elapses) | `Expired` | **provider-autonomous** (time) |
| `Expired` | `Refresh` ok | `Active` | consumer-driven (RT may rotate) |
| `Expired`/`Active` | refresh-token killed underneath us | `Dead` | **provider-autonomous** (materialized on next `Refresh`) |
| `Dead` | `Auth` again | `Active` | consumer-driven |
| any | `Revoke` | `NoGrant` | consumer-driven |

`CheckHealth` is a **read** (a non-persisting probe of `Expired→Active`), not a transition. The **provider-autonomous** edges are the ones we can't drive and only observe late — and they are exactly what the fake's fault API models (see M2). Microsoft satisfies this same S; only the wire (endpoints, `offline_access` scope), error-code→edge mapping, identity-claim extraction (`preferred_username`/`upn`, no `email_verified`), and revoke mechanism differ — keep `parseIDToken` a separable pure seam so the MS adapter is a one-function swap, not a refactor.

### Pure entities (the conceptual core)

| Name | Lives in | Status |
|------|----------|--------|
| `credentialFromToken` | `lib/provider/oauth/token.go` | new |
| `applyRefresh` | `lib/provider/oauth/token.go` | new |
| `parseIDToken` | `lib/provider/oauth/token.go` | new (extracted) |
| `mintIDToken` | `lib/provider/oauth/token.go` | new |
| `mergeScopes` | `lib/provider/oauth/token.go` | new (moved) |
| `buildAuthURL` | `lib/provider/oauth/token.go` | new (extracted) |
| `isReauthRequired` | `lib/provider/oauth/health.go` | unchanged (already pure) |
| `checkHealth` | `lib/provider/oauth/health.go` | new (extracted composite) |
| `HealthState` | `lib/provider/oauth/health.go` | unchanged |

- **`credentialFromToken(tok tokenResponse, now time.Time) (*vault.Credential, error)`** — pure map from a token-endpoint response to a fresh credential (the body of today's `exchangeCode` *after* the HTTP call: extract email from ID token, split scopes, compute expiry from `ExpiresIn`). Takes a clock so expiry is deterministic.
  - **DRY rationale:** real `exchangeCode` and fake `Auth` both need to turn a `tokenResponse` into a `*vault.Credential` identically. Today only `exchangeCode` has it; the fake would copy it. This is the single source of truth for "token response → credential."
  - **Future extensions:** a non-Google OIDC provider reuses this verbatim (email claim is standard OIDC).

- **`applyRefresh(old *vault.Credential, tok tokenResponse, now time.Time) *vault.Credential`** — pure map for the refresh path: new access token + expiry, refresh-token rotation (keep old unless `tok.RefreshToken` set), scope update if `tok.Scope` present (else default to `old.Scopes`), and **field/sidecar preservation**. It must carry across **`Type`, `Provider`, `Account`** *and* the four sidecars `GCP`/`AIStudio`/`AdminKey`/`Catalog` — exactly the set today's `Refresh` copies (`google.go:186-203`). `TestRefresh_PreservesSidecars` (`google_test.go:179`) asserts `out.Type == vault.TypeOAuth`, so dropping `Type` would break it.
  - **DRY rationale:** real `Refresh` and fake `Refresh` must apply rotation + sidecar preservation identically. Extracting it guarantees the fake can't drift from the real sidecar-preservation contract (the exact class of bug nous#42 grounding exists to catch below the seam — here we make it pure so it's caught above the seam too).

- **`parseIDToken(idToken string) (email string, verified bool, err error)`** — extract `email` + `email_verified` claims from the JWT payload (no signature check; token came from Google over HTTPS). Widens today's `parseIDTokenEmail` to also return `email_verified`.
  - **Note (hardening, `Root Cause`):** today's flow accepts any email claim, including `email_verified:false` — an identity-spoofing smell. `credentialFromToken` will reject `verified==false`. Real Google always returns `email_verified:true` for the consent flow, so production is unaffected; the fake's `EmailNotVerified` knob exercises the new guard.

- **`mintIDToken(email string, verified bool) string`** — pure inverse of `parseIDToken`: build an unsigned `header.payload.` JWT (base64url payload `{"email":...,"email_verified":...}`). Used by the fake to produce structurally-valid ID tokens that flow through the *same* `parseIDToken` real uses — so the fake exercises real parsing, not a bypass.

- **`mergeScopes` / `buildAuthURL`** — moved/extracted unchanged from `google.go`; pure, shared by both adapters (the fake records the built auth URL for assertions).

- **`checkHealth(refresh func(*vault.Credential) (*vault.Credential, error), cred *vault.Credential) HealthState`** — today's `CheckHealth` body, lifted to take the refresh function instead of the receiver. `nil`/no-refresh-token → `NeedsReauth`; refresh ok → `Healthy`; `isReauthRequired(err)` → `NeedsReauth`; else → `Unknown`. Both adapters' `CheckHealth` method delegates here.
  - **DRY rationale:** `CheckHealth` is already provider-neutral (only inspects error strings + calls `Refresh`). Sharing it means the fake's `FailRefresh("invalid_grant")` automatically drives the `NeedsReauth` path with zero duplicated classification logic.

### Integration points (where pure meets the world)

| Name | Lives in | Status | Wraps |
|------|----------|--------|-------|
| `Provider` | `lib/provider/oauth/port.go` | new | the port interface |
| `Conf` | `lib/provider/oauth/port.go` | new | construction config |
| `GoogleProvider` (real adapter) | `lib/provider/oauth/google.go` | modified | Google HTTP + browser + callback server |
| `waitForCallback` | `lib/provider/oauth/google.go` | unchanged | local redirect HTTP server (async channel) |
| `Fake` | `lib/provider/oauth/fake.go` | new | in-memory token issuer + consent short-circuit |

- **`Provider`** — `interface { Auth(account string, scopes, existingScopes []string, forceFresh bool) (*vault.Credential, error); Refresh(*vault.Credential) (*vault.Credential, error); Revoke(refreshToken string) error; CheckHealth(*vault.Credential) HealthState }`.
  - **Injected into:** `proxy.Refresher` map, `tui.Authenticator`, `charoncli.tokenSupplierFromVault`. Consumers depend on this (or the narrower existing interfaces it satisfies), so tests inject `NewFake`.

- **`Conf`** — opaque, service-specific: `ClientID, ClientSecret, AuthURL, TokenURL, RevokeURL string; DefaultScopes []string; Output io.Writer`. `New(Conf) *GoogleProvider` (real); `NewFake(Conf) *Fake` (fake). `NewGoogleProvider()` stays as a thin wrapper that builds the default Conf (XOR-decode the obfuscated client id/secret + Google endpoint defaults) and calls `New` — so the four production call sites are untouched.
  - **One cross-service convention:** `New(Conf)`/`NewFake(Conf)`, not the fields (same note as `lib/gh/client.go:20`).

- **`GoogleProvider` (real adapter)** — today's code, refactored to (a) carry endpoints from `Conf` instead of package consts/vars, (b) delegate credential-shaping to the pure helpers. The **only** thing that opens a browser, runs a callback server, and POSTs to Google. Behavior-preserving in M1.
  - **`waitForCallback`** — the async redirect channel (`codeCh`/`errCh` + `select`) stays exactly here. This is the "channel inside an adapter for a push/event service" case nous#42 flagged. The port hides it; M2 grounds it with a hermetic listener test.

- **`Fake`** — stateful in-memory issuer. Holds: seeded consent identity (`authEmail`, `verified`), an account table keyed by refresh token (`account`, `scopes`, sidecars), an injectable clock, and fault knobs. Implements `Provider` by minting `tokenResponse`s and running them through the **same** pure helpers the real adapter uses.
  - **Injected into:** consumer tests (proxy refresh, gcp token supply, tui scope-apply) — the deterministic-shell extension the user wants.
  - **Seeding API:** `SetAuthEmail(email string, verified bool)`, `SeedAccount(account string, scopes []string) (refreshToken string)`, `SetClock(func() time.Time)`. Mirrors gh's `AddUser`/`CreateRepo` seeding style.
  - **Fault API = S's provider-autonomous transitions (named, not ad-hoc):** `RevokeGrant(account string)` (RT killed → next `Refresh`/`CheckHealth` → `Dead`), `Transient(bool)` (5xx/network → `Unknown`, *not* `Dead`), `DowngradeScope(account string, scopes []string)` (refresh returns a reduced scope set), `DenyConsent(bool)` (consent leg → `access_denied`), `WrongAccount(email string)` (consent resolves to a different email than requested). Unverified email is set via `SetAuthEmail(_, false)` (a payload concern below the per-provider seam, not an S edge). Each knob fires one labeled edge of S — that's what makes the fake a model rather than a mock.

### Grounding boundary (decided — the "harder than gh" bit)

| Surface | Grounded against real Google? | How |
|---------|-------------------------------|-----|
| `Refresh` | **Yes**, automatic | conformance test refreshes a Keychain-stored test-account refresh token (nous#43 account model); asserts a fresh non-empty access token + future expiry |
| `CheckHealth` | **Yes**, automatic | falls out of grounded `Refresh` (it *is* `Refresh` + classification) |
| `Auth` (consent → code → exchange) | **No** — documented manual | interactive consent/browser leg cannot be headless; the token-exchange leg needs a fresh consent code. Manual step recorded. |
| `Revoke` | **No** — documented manual | destructive: revoking the Keychain refresh token would break every subsequent conformance run. Manual/throwaway only. |

Below-seam real-adapter bugs (wrong endpoint string, wrong form params, callback parsing) are caught by **(a)** the M2 hermetic `waitForCallback` test (pins the callback-code/error extraction) and **(b)** the conformance `Refresh` run. The fake is structurally blind to those — same division of labor nous#42 documented; record it, don't claim coverage the fake can't deliver.

---

## Chunk 1: Milestone M1 — port + pure core + real adapter on the seam

> **Review boundary M1.** Behavior-preserving refactor: the port + pure helpers exist, the real adapter routes through them, every existing test stays green. No fake yet.

### Task 1.1: Pure token-shaping helpers (`token.go`)

**Files:**
- Create: `lib/provider/oauth/token.go`
- Create: `lib/provider/oauth/token_test.go`
- Modify: `lib/provider/oauth/google.go` (remove the moved/extracted funcs, leave references)
- Modify: `lib/provider/oauth/google_test.go` — **two existing tests break and must be updated in this task** (else M1's "all green" gate trips):
  - `TestBuildAuthURL_LoginHint` (`google_test.go:72`) calls `gp.buildAuthURL(...)` as a method; once `buildAuthURL` is a free function, rewrite it to call the free function passing `clientID`/`authURL`.
  - `TestParseIDTokenEmail` (`google_test.go:14`) calls `parseIDTokenEmail(...)`; either keep `parseIDTokenEmail` as a thin 1-return wrapper over `parseIDToken`, or update the test to the new 3-return signature.

- [ ] **Step 1: Write failing tests for the pure helpers**

```go
// token_test.go
package oauth

import (
	"testing"
	"time"

	"github.com/xianxu/nous/lib/provider/vault"
)

func TestParseIDToken_EmailAndVerified(t *testing.T) {
	tok := mintIDToken("a@b.com", true)
	email, verified, err := parseIDToken(tok)
	if err != nil || email != "a@b.com" || !verified {
		t.Fatalf("got (%q,%v,%v)", email, verified, err)
	}
}

func TestParseIDToken_Unverified(t *testing.T) {
	_, verified, err := parseIDToken(mintIDToken("a@b.com", false))
	if err != nil || verified {
		t.Fatalf("expected verified=false, got verified=%v err=%v", verified, err)
	}
}

func TestParseIDToken_NoEmail(t *testing.T) {
	if _, _, err := parseIDToken(""); err == nil {
		t.Fatal("expected error for empty id token")
	}
}

func TestCredentialFromToken_RejectsUnverified(t *testing.T) {
	now := time.Unix(1000, 0)
	tok := tokenResponse{AccessToken: "at", RefreshToken: "rt", IDToken: mintIDToken("a@b.com", false), ExpiresIn: 3600, Scope: "openid"}
	if _, err := credentialFromToken(tok, now); err == nil {
		t.Fatal("expected rejection of unverified email")
	}
}

func TestCredentialFromToken_Shape(t *testing.T) {
	now := time.Unix(1000, 0)
	tok := tokenResponse{AccessToken: "at", RefreshToken: "rt", IDToken: mintIDToken("a@b.com", true), ExpiresIn: 3600, Scope: "openid email"}
	c, err := credentialFromToken(tok, now)
	if err != nil {
		t.Fatal(err)
	}
	if c.Account != "a@b.com" || c.AccessToken != "at" || c.RefreshToken != "rt" {
		t.Fatalf("bad cred: %+v", c)
	}
	if !c.Expiry.Equal(now.Add(3600 * time.Second)) {
		t.Fatalf("bad expiry: %v", c.Expiry)
	}
}

func TestApplyRefresh_RotationAndSidecars(t *testing.T) {
	now := time.Unix(2000, 0)
	old := &vault.Credential{Type: vault.TypeOAuth, Provider: "google", Account: "a@b.com", RefreshToken: "old", Scopes: []string{"openid"}, GCP: &vault.GCPData{ProjectID: "p"}}
	// no new refresh token in response → keep old
	got := applyRefresh(old, tokenResponse{AccessToken: "new", ExpiresIn: 3600}, now)
	if got.RefreshToken != "old" || got.AccessToken != "new" || got.GCP == nil || got.GCP.ProjectID != "p" {
		t.Fatalf("rotation/sidecar wrong: %+v", got)
	}
	// identity fields preserved (TestRefresh_PreservesSidecars asserts Type)
	if got.Type != vault.TypeOAuth || got.Provider != "google" || got.Account != "a@b.com" {
		t.Fatalf("identity not preserved: %+v", got)
	}
	// no tok.Scope → default to old scopes
	if len(got.Scopes) != 1 || got.Scopes[0] != "openid" {
		t.Fatalf("scopes should default to old: %+v", got.Scopes)
	}
	// rotation: response carries a new refresh token → adopt it
	got2 := applyRefresh(old, tokenResponse{AccessToken: "new", RefreshToken: "rotated", ExpiresIn: 3600}, now)
	if got2.RefreshToken != "rotated" {
		t.Fatalf("expected rotated token, got %q", got2.RefreshToken)
	}
}
```

> Note: confirm `vault.GCPData`'s real field name before pinning `ProjectID` (read `lib/provider/vault`); adjust the literal to the actual field.

- [ ] **Step 2: Run, verify FAIL** (`go test ./lib/provider/oauth/ -run 'TestParseIDToken|TestCredentialFromToken|TestApplyRefresh' -v`) — expect "undefined: mintIDToken/parseIDToken/credentialFromToken/applyRefresh".

- [ ] **Step 3: Implement `token.go`** — move `mergeScopes`, `buildAuthURL` (drop the receiver; pass `clientID`, `authURL`, endpoints as args), `tokenResponse` (move the struct here), extract `parseIDToken` (widen `parseIDTokenEmail` to also read `email_verified`), add `mintIDToken`, `credentialFromToken` (rejects unverified), `applyRefresh` (rotation + sidecar preservation, taking `now`).

- [ ] **Step 4: Run, verify PASS.**

- [ ] **Step 5: Commit** — `#44 M1: extract pure token-shaping helpers (ARCH-PURE/ARCH-DRY)`

### Task 1.2: Port interface + Conf + `New` (`port.go`)

**Files:**
- Create: `lib/provider/oauth/port.go`
- Modify: `lib/provider/oauth/google.go` (`GoogleProvider` carries `Conf` fields; `NewGoogleProvider` → wrapper over `New`)

- [ ] **Step 1: Write a compile-time assertion test**

```go
// port_test.go
package oauth
var _ Provider = (*GoogleProvider)(nil)
```

- [ ] **Step 2: Run, verify FAIL** (undefined `Provider`).

- [ ] **Step 3: Implement `port.go`** — `Provider` interface (4 methods), `Conf` struct, `New(Conf) *GoogleProvider`, and a `defaultGoogleConf()` that XOR-decodes the obfuscated client id/secret and sets the Google endpoint + scope defaults. **`RevokeURL` must default to `https://oauth2.googleapis.com/revoke`** (today's hardcoded literal at `google.go:259`) or the four production call sites silently lose revoke. Refactor `GoogleProvider` to hold `clientID, clientSecret, authURL, tokenURL, revokeURL string; defaultScopes []string; Output io.Writer`. `NewGoogleProvider()` returns `New(defaultGoogleConf())`.

- [ ] **Step 4: Route real adapter through pure helpers** — `exchangeCode` becomes: POST to `g.tokenURL` → decode `tokenResponse` → `credentialFromToken(tok, time.Now())`. `Refresh` becomes: POST → decode → `applyRefresh(cred, tok, time.Now())`. `Auth` builds the URL via the shared `buildAuthURL`. `Revoke`/`waitForCallback` unchanged except endpoint comes from `Conf`.

- [ ] **Step 5: Run the whole package + dependents** (`go test ./lib/provider/oauth/... ./lib/provider/proxy/... ./lib/charoncli/... ./lib/tui/...`) — expect PASS (behavior-preserving). Existing `google_test.go` may reference `googleTokenURL`; update it to set `Conf.TokenURL` (or a test constructor) instead.

- [ ] **Step 6: Commit** — `#44 M1: Provider port + Conf; real adapter routes through pure core`

### Task 1.3: `CheckHealth` as a shared composite

**Files:**
- Modify: `lib/provider/oauth/health.go`
- Modify: `lib/provider/oauth/health_test.go` (should still pass against `*GoogleProvider`)

- [ ] **Step 1:** Extract `checkHealth(refresh func(*vault.Credential)(*vault.Credential,error), cred *vault.Credential) HealthState` from the current method body. `GoogleProvider.CheckHealth` becomes `return checkHealth(g.Refresh, cred)`.
- [ ] **Step 2: Run** `go test ./lib/provider/oauth/...` — expect PASS (`health_test.go` unchanged behavior).
- [ ] **Step 3: Commit** — `#44 M1: CheckHealth → shared composite over Refresh (ARCH-DRY)`

**M1 close:** `sdlc milestone-close` (fresh-context review of the behavior-preserving refactor). Fix Critical/Important before M2.

---

## Chunk 2: Milestone M2 — the fake + hermetic flow tests + grounded callback

> **Review boundary M2.** The `shim'` deliverable: a stateful fake exercised through the same pure core, plus a hermetic test for the real adapter's async callback (the below-seam leg the fake can't see).

### Task 2.0: Author the consumer-POV state machine (S) as a `target`

**Files:**
- Create: `workshop/targets/oauth-credential-lifecycle.md` (`type: target`)

- [ ] **Step 1:** Write S explicitly (the table from "Core concepts"): states (`NoGrant`/`Active`/`Expired`/`Dead`), the consumer-driven vs provider-autonomous edges, the operation/observation that fires each, and per-edge grounding status. Provider-neutral — note Google vs Microsoft variance lives in the wire/payload, not S. This is the spec the fake (2.1) executes and the contract (3.2) bisimulates; reference it from both adapters and the contract via `target: oauth-credential-lifecycle`.
- [ ] **Step 2: Commit** — `#44 M2: explicit consumer-POV state-machine target (ariadne#71 finding)`

### Task 2.1: `Fake` adapter

**Files:**
- Create: `lib/provider/oauth/fake.go`
- Create: `lib/provider/oauth/fake_test.go`

- [ ] **Step 1: Write the failing happy-path flow test**

```go
// fake_test.go
func TestFake_AuthRefreshRevokeRoundTrip(t *testing.T) {
	f := NewFake(Conf{ClientID: "cid", DefaultScopes: []string{"openid"}})
	f.SetAuthEmail("user@example.com", true)
	f.SetClock(func() time.Time { return time.Unix(1000, 0) })

	cred, err := f.Auth("", []string{"https://www.googleapis.com/auth/gmail.readonly"}, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if cred.Account != "user@example.com" || cred.RefreshToken == "" {
		t.Fatalf("bad cred: %+v", cred)
	}

	refreshed, err := f.Refresh(cred)
	if err != nil || refreshed.AccessToken == cred.AccessToken {
		t.Fatalf("refresh should rotate access token: %v", err)
	}
	if got := f.CheckHealth(refreshed); got != HealthHealthy {
		t.Fatalf("expected Healthy, got %v", got)
	}
	if err := f.Revoke(refreshed.RefreshToken); err != nil {
		t.Fatal(err)
	}
	// after revoke, the token is gone → refresh fails, health is NeedsReauth
	if _, err := f.Refresh(refreshed); err == nil {
		t.Fatal("expected refresh to fail after revoke")
	}
	if got := f.CheckHealth(refreshed); got != HealthNeedsReauth {
		t.Fatalf("expected NeedsReauth after revoke, got %v", got)
	}
}
```

- [ ] **Step 2: Run, verify FAIL.**

- [ ] **Step 3: Implement `fake.go`** — `Fake` struct (mutex-guarded account table keyed by refresh token, `authEmail`/`verified`, clock, fault flags) + `NewFake(Conf) *Fake`. `Auth`: merge scopes, build (and record) the auth URL, run the consent short-circuit (below), mint a `tokenResponse` (`mintIDToken(authEmail, verified)`, fresh access/refresh tokens, `ExpiresIn`), `credentialFromToken`, store the account. `Refresh`: look up by refresh token (unknown/revoked → `invalid_grant` error), mint a rotated `tokenResponse`, `applyRefresh`. `Revoke`: delete from table (unknown → `ErrAlreadyRevoked`). `CheckHealth`: `checkHealth(f.Refresh, cred)`. Plus the seeding/fault API.

- [ ] **Step 4: Run, verify PASS.**

- [ ] **Step 5: Commit** — `#44 M2: stateful Fake OAuth adapter over the shared pure core`

### Task 2.2: Fault-injection + async-callback short-circuit tests

**Files:**
- Modify: `lib/provider/oauth/fake_test.go`

- [ ] **Step 1: Write failing fault tests** — table-driven, each asserting one provider-autonomous edge of S:
  - `DenyConsent(true)` → `Auth` returns an `access_denied` error (the redirect `?error=` leg; `NoGrant→NoGrant`). Assert the auth URL was still built/recorded.
  - `SetAuthEmail(_, false)` (unverified) → `Auth` rejects via `credentialFromToken` (payload guard, below the seam).
  - `RevokeGrant(account)` → `Refresh` returns `invalid_grant` (`Active/Expired→Dead`); `CheckHealth` → `NeedsReauth`.
  - `Transient(true)` → `Refresh` returns a 5xx/network-shaped error; `CheckHealth` → `HealthUnknown` (stays `Expired`, *not* `Dead` — the key Unknown-vs-NeedsReauth distinction).
  - `DowngradeScope(account, fewer)` → `Refresh` ok but returns the reduced scope set; assert the refreshed cred's `Scopes` shrank.
  - `WrongAccount(other)` → `Auth` succeeds but the credential's `Account` differs from the requested one (the "requested X, authenticated as Y" path).
  - Expired credential: `SetClock` past `Expiry`; assert `cred.IsExpired()` true, then `Refresh` rotates and the new cred is not expired (`Active→Expired→Active`).
  - **Async short-circuit assertion:** after `Auth`, assert `f.LastAuthURL()` contains `response_type=code`, the redirect URI, and the requested scope — i.e. the fake built the real authorization request even though it skipped the browser. Documents that the consent leg is modeled, not faked away.
- [ ] **Step 2: Run, verify FAIL → implement knobs → PASS.**
- [ ] **Step 3: Commit** — `#44 M2: fault injection + consent short-circuit assertions`

### Task 2.3: Hermetic test for the real adapter's async callback (below-seam grounding)

**Files:**
- Modify: `lib/provider/oauth/google_test.go`

- [ ] **Step 1: Write a failing test** that exercises `waitForCallback` directly without a browser:

```go
func TestWaitForCallback_Code(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	go func() {
		// simulate Google redirecting the browser back with a code
		http.Get(fmt.Sprintf("http://%s/?code=abc123", ln.Addr().String()))
	}()
	code, err := waitForCallback(ln)
	if err != nil || code != "abc123" {
		t.Fatalf("got (%q,%v)", code, err)
	}
}

func TestWaitForCallback_Error(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	go func() { http.Get(fmt.Sprintf("http://%s/?error=access_denied", ln.Addr().String())) }()
	if _, err := waitForCallback(ln); err == nil {
		t.Fatal("expected error for access_denied callback")
	}
}
```

- [ ] **Step 2: Run, verify PASS** (the code already exists; this *pins* the callback-extraction contract the fake is blind to). If it flakes on the goroutine race, add a tiny retry/poll on connect.
- [ ] **Step 3: Optionally add a token-exchange test** driving the full real `Auth`→`exchangeCode` against an `httptest` server set as `Conf.TokenURL`, returning a minted ID token — grounds the real adapter's HTTP form params + response parsing hermetically.
- [ ] **Step 4: Commit** — `#44 M2: hermetic callback + token-exchange tests for the real adapter`

**M2 close:** `sdlc milestone-close`. Fix Critical/Important before M3.

---

## Chunk 3: Milestone M3 — consumer migration + dual-backend grounding + docs

> **Review boundary M3.** Wire the port through the consumers (unlocking hermetic consumer tests — the user's e2e-mock goal), ground the token surface against real Google, and document the boundary + atlas.

### Task 3.1: Migrate consumers onto the port

**Files:**
- Modify: `lib/charoncli/gcp.go:187` (`tokenSupplierFromVault` param `*oauth.GoogleProvider` → `oauth.Provider`)
- Verify (no change expected): `lib/provider/proxy/serve.go:57` (`Refreshers` map already takes the `Refresher` interface the port satisfies), `lib/tui` (`Authenticator` already satisfied).

- [ ] **Step 1:** Widen the `tokenSupplierFromVault` signature; build (`go build ./...`).
- [ ] **Step 2: Add a hermetic consumer test** proving the fake plugs in — e.g. in `lib/charoncli`, a test that seeds a `vault` with an expired credential + a `NewFake`-seeded account and asserts `tokenSupplierFromVault` refreshes + persists. (If `vault.Store` needs a fake, check for an existing test vault; reuse it — `ARCH-DRY`.)
- [ ] **Step 3: Run** `go test ./lib/charoncli/... ./lib/provider/proxy/...` — PASS.
- [ ] **Step 4: Commit** — `#44 M3: migrate charon's oauth consumers onto the Provider port`

### Task 3.2: Dual-backend contract test

**Files:**
- Create: `lib/provider/oauth/contract_test.go` (fake side, always runs)
- Create: `lib/provider/oauth/contract_real_test.go` (`//go:build conformance`)

- [ ] **Step 1:** Write a shared `runOAuthContract(t, p Provider, seedRefreshToken string)` body as an **S transition-coverage table** (bisimulating S against whichever backend is passed): for each *consumer-driven* edge groundable without consent, assert the transition. Minimum rows: `Expired→Active` (`Refresh` returns non-empty access token + future expiry, preserves `Account` + sidecars), `CheckHealth(valid)==HealthHealthy` (the read), `CheckHealth(nil)==HealthNeedsReauth`. Provider-autonomous edges (`→Dead`, scope downgrade) and the consent leg are fake-only / manual (see grounding boundary) — mark each row with its grounding status so the table doubles as the boundary doc.
- [ ] **Step 2:** `contract_test.go` — `TestContract_Fake`: `NewFake`, `SeedAccount`, run the body. Always green.
- [ ] **Step 3:** `contract_real_test.go` (build-tagged) — read the test-account refresh token from Keychain via a small `security find-generic-password` helper. (Note: gh's `keychainSecret` helper is private to `package gh`'s build-tagged test file and **not importable** — copy the ~5-line `exec.Command("security", ...)` helper into this file; small, acceptable DRY cost across a package boundary.) `t.Skip` if absent → zero-config like nous#42. `New(defaultGoogleConf())`, run the body against real Google's token endpoint. **Do not** call `Revoke` (destructive) or the consent leg (non-headless).
- [ ] **Step 4: Run** the fake side (`go test ./lib/provider/oauth/`) and, once, the real side (`go test -tags conformance ./lib/provider/oauth/` with the Keychain token present) — record the certification in the issue Log (date + which invariants passed), nous#42 discipline.
- [ ] **Step 5: Commit** — `#44 M3: dual-backend contract test (fake always; real Google build-tagged)`

### Task 3.3: Document the grounding boundary + atlas

**Files:**
- Modify: the issue `## Log` (grounded-vs-manual table + certification record)
- Create/modify: `atlas/` entry for the oauth shim (+ link in `atlas/index.md`); reference nous#42's shim entry for the pattern
- Modify: `workshop/lessons.md` if the review surfaced any rule

- [ ] **Step 1:** Write the grounding-boundary note (the table from "Core concepts") into the Log + a short atlas sketch: the port surface, the real/fake split, and the explicit "consent + Revoke are manual; Refresh/CheckHealth are grounded" boundary — don't claim coverage the fake can't deliver.
- [ ] **Step 2: Commit** — `#44 M3: document oauth-shim grounding boundary + atlas`

**M3 close → issue close:** `sdlc milestone-close` then `sdlc close --issue 44 --milestone M3 --verified '<evidence>'` (measured `--actual`).

---

## Open questions / risks

1. **Keychain test-account refresh token (Task 3.2).** Needs a pre-obtained Google refresh token for a throwaway test account stored in Keychain (nous#43 model). If that account doesn't exist yet, the conformance test `t.Skip`s (still zero-config-correct); provisioning it is a one-time manual step, recorded in the Log — not a blocker for the hermetic deliverable.
2. **`email_verified` hardening (Task 1.1).** Rejecting unverified emails changes real behavior. It's a security improvement and production-safe (Google returns verified for consent), but if a consumer relies on accepting unverified emails, surface it at M1 review. Confirm no existing test asserts the old accept-unverified behavior.
3. **`vault` field names.** The test literals (`vault.GCPData`, sidecar fields) are written from the digest; confirm exact names against `lib/provider/vault` when implementing.
4. **2nd OAuth provider (Microsoft) — design-aware here, adapter is a follow-up.** Per the ≥2-real-providers process finding, the port + S must *fit* Microsoft (kept honest by the separable `parseIDToken` seam, parameterized `Conf` endpoints, no `email_verified` in the shared core). Building the **Microsoft `real` adapter** is out of #44's scope — it's symmetric to the gh 2nd-provider work in #46 and belongs either in a follow-up ticket or under nous#45's full-surface harness. **Decision to confirm with operator before M3 close:** separate ticket vs fold into #45. Until then, #44 ships Google-real + fake, with the seams proven Microsoft-ready by inspection, not by a built MS adapter.
```
---

## Revisions

### 2026-06-08 — M1 close (review verdict FIX-THEN-SHIP → addressed)

The M1 boundary review (`Review-Window: 0f12c7e..HEAD`) returned FIX-THEN-SHIP
(no Critical; two Important). Deltas applied before crossing into M2:

- **`buildAuthURL` extracted to a free function** (`token.go`), per the
  Core-concepts table and Task 1.1 Step 3 — it had remained a `*GoogleProvider`
  method, which would have blocked the M2 `Fake` from reusing it (ARCH-DRY).
  Signature: `buildAuthURL(authURL, clientID, redirectURI string, scopes []string, loginHint string, forceFresh bool)`. `TestBuildAuthURL_LoginHint` updated to call it directly.
- **`mergeScopes` moved to `token.go`** (was the Minor table/code mismatch);
  the Pure-entities table is now accurate for both.
- **Boundary-label nuance:** M1 is behavior-preserving **except the deliberate
  `email_verified==false` rejection** in `credentialFromToken` on the
  `Auth`/`exchangeCode` path (Task 1.1 / risk #2). Production-safe — Google
  returns `email_verified:true` for the consent flow; no existing test asserted
  accept-unverified (`TestParseIDTokenEmail` exercises the unchanged
  `parseIDTokenEmail` wrapper).
- **Atlas deferral made explicit (not silently skipped):** the new `Provider`
  port/`Conf` surface is documented at **M3 Task 3.3**, alongside the fake +
  grounding boundary, so the oauth-shim atlas entry is written once as a whole
  rather than churned across M1→M3. The port isn't live consumer surface until
  the M3 migration. `sdlc milestone-close M1` ran with `--no-atlas` + rationale.
