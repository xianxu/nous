---
name: oauth-conformance-provision
description: Provision the Google refresh token that grounds the OAuth shim's fake against real Google, and run the certification. Use when the conformance test skips ("no Google conformance refresh token"), on ~monthly re-cert, or when porting this to a new OAuth provider (nous#48 Microsoft).
---

# OAuth conformance: provision + certify

The OAuth shim's fake (`lib/provider/oauth/fake.go`) is grounded against real
Google by a build-tagged contract test
(`lib/provider/oauth/contract_real_test.go`). That test needs a **real Google
refresh token** in the macOS Keychain; without it, it skips. This tool obtains
that token and stores it.

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

## Porting to a new provider (nous#48 Microsoft)

This is the template. A Microsoft provisioner differs only in the per-provider
seam: the `offline_access` scope (not `openid` alone for a refresh token), the
tenant-scoped endpoints, and the identity-claim extractor — none of which change
the provision→certify→record loop. Reuse this structure; vary the `Conf` and the
Keychain service const.
