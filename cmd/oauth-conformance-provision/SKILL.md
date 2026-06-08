---
name: oauth-conformance-provision
description: Provision the Google or Microsoft refresh token that grounds the OAuth shim's fake against the real provider, and run the certification. Use when a conformance test skips ("no Google/Microsoft conformance refresh token"), on re-cert, or when standing up the Microsoft (Entra) grounding (nous#48).
---

# OAuth conformance: provision + certify

The OAuth shim's fake (`lib/provider/oauth/fake.go`) is grounded against the real
providers by build-tagged contract tests
(`lib/provider/oauth/contract_real_test.go`): `Contract_RealGoogle` and
`Contract_RealMicrosoft`. Each test needs a **real refresh token** in the macOS
Keychain; without it, it skips. This tool obtains that token and stores it, for
either provider (`-provider google|microsoft`).

## Why this tool exists (vs pasting a token)

A Google refresh token is **bound to the OAuth client that issued it**. The
conformance test calls `GoogleProvider.Refresh`, which uses charon's embedded
client_id/secret — so the token must be issued to **that** client. A token from
Google's OAuth Playground or any other client will not refresh under charon's
client and the cert would fail spuriously. There is no "paste a PAT" path the way
gh has (gh conformance uses copy-pasteable GitHub PATs); Google requires the
interactive **consent flow**. Hence a tool that runs charon's own consent.

The cert is **Refresh-only** (read-only) — it never calls `Revoke` — so the
account is never mutated. Use a **throwaway** Google account to keep the
developer's real account off the cert path (mirrors gh's #43 throwaway accounts).

## Provision

```sh
go run ./cmd/oauth-conformance-provision [-account throwaway@gmail.com]
```

- Opens a browser. Consent with the throwaway account.
- Stores the refresh token in Keychain service `nous-oauth-conformance-google`
  (the single source of truth is `oauth.ConformanceKeychainService`; the test
  reads the same const).
- `-account` is an optional login hint; `-service` overrides the Keychain
  service (rarely needed).

If charon's OAuth client is in Google "testing" mode, the throwaway account must
be an allowed test user, or consent will be refused.

## Certify

```sh
go test -tags conformance ./lib/provider/oauth/ -run Contract_Real -v
```

This runs the **same** contract body (`runOAuthContract`) the fake runs, against
real Google — certifying the fake hasn't drifted on the **Refresh** and
**CheckHealth** surface. A PASS is the certification.

**Grounding boundary** (see `workshop/targets/oauth-credential-lifecycle.md`):
only `Refresh`/`CheckHealth` are grounded here. The consent leg (`Auth`), `Revoke`
(destructive), and the provider-autonomous `→Dead` edge are fake-only/manual — by
construction, not omission. If the cert FAILS, the **fake** has drifted from real
Google: fix `fake.go` / the pure core, not the test, then re-certify.

## Record the certification

Per the nous#42 grounding discipline, record each successful cert (date + result)
in the `oauth-credential-lifecycle` target's `## Revisions`. Re-cert ~monthly or
on suspected drift (re-run the two commands above; the token may need refreshing
if Google has rotated/expired it — just re-run the provisioner).

## Microsoft / Entra (nous#48 — n=2-real grounding)

Microsoft is a **public client + PKCE** (no secret), so unlike Google there is
nothing embedded — you supply your own Entra app's IDs. One-time Azure setup:

1. Azure portal → **App registrations** → **New registration**.
2. **Supported account types:** *Accounts in this organizational directory only*
   (single tenant) is enough for grounding.
3. **Authentication** → **Add a platform** → **Mobile and desktop applications**
   → redirect URI **`http://localhost`** (the desktop platform wildcards the
   loopback port, matching charon's random callback port).
4. **Authentication** → **Allow public client flows** → **Yes**. No client secret.
5. **API permissions** (delegated, all user-consentable — no admin consent):
   `openid`, `profile`, `offline_access`.
6. From **Overview**, copy the **Application (client) ID** and **Directory
   (tenant) ID**.

Provision (consent with a throwaway Entra account):

```sh
MICROSOFT_CLIENT_ID=<app-id> MICROSOFT_TENANT_ID=<tenant-id> \
  go run ./cmd/oauth-conformance-provision -provider microsoft
```

Stores the token in Keychain `nous-oauth-conformance-microsoft`
(`oauth.ConformanceKeychainServiceMicrosoft`). Certify (the test reads the same
env for client/tenant):

```sh
MICROSOFT_CLIENT_ID=<app-id> MICROSOFT_TENANT_ID=<tenant-id> \
  go test -tags conformance ./lib/provider/oauth/ -run Contract_RealMicrosoft -v
```

**Single-use rotation:** Microsoft rotates the refresh token on *every* Refresh.
The test wraps the provider in `rtCapture` and **persists the final rotated token
back to Keychain**, so re-runs work. (Google's token is reusable and the Google
test deliberately does NOT persist — probe semantics.) Re-provision only if the
stored token goes stale (>90 days idle).

**Microsoft grounding boundary:** `Refresh`/`CheckHealth` grounded; the consent
leg is interactive; **`Revoke` is `ErrRevokeUnsupported`** — Microsoft has no
per-token revoke endpoint at all (only Graph `revokeSignInSessions`, which is
global across every app), so it's ungroundable by mechanism, not just by
destructiveness.

## The shape generalizes

The provision→certify→record loop is identical across providers; only the
per-provider seam (scopes, endpoints, identity extractor, client type) varies —
the `dialect` in `lib/provider/oauth/`. A third provider reuses this structure:
add a `New<Provider>Provider` + a Keychain-service const, pass `-provider <name>`.
