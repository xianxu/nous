# OAuth refresh-token health surface

Per `nous#15`, charon's stored OAuth credentials can become stale (Google revokes the refresh token, account password is reset, OAuth client verification status changes, etc.) without charon detecting it until an HTTPS request actually hits the proxy. The TUI surfaces refresh-token validity at session boundaries so the operator can proactively reauth instead of discovering invalid_grant mid-action.

## Layers

```
lib/provider/oauth/health.go           HealthState enum (Healthy / NeedsReauth / Unknown)
                                        + (*GoogleProvider).CheckHealth(*Credential) HealthState
                                        + FriendlyError(err) for user-facing prose
lib/tui/health.go                       AccountHealth string-typed enum + AccountHealthChecker fn type
                                        (kept domain-neutral so the adapter lives in the caller)
lib/charoncli/charoncli.go (AuthCmd)    builds adapter from oauth.HealthState → tui.AccountHealth,
                                        passes via tui.Run's healthCheck parameter
lib/tui/{provider_picker, picker}.go    AnnotateHealth() methods iterate accounts, stamp results
lib/tui/picker.go (View)                renders "(needs reauth)" badge inline; mutes the row
lib/tui/scopes.go (viewApplyError)      uses oauth.FriendlyError for translated error rendering
```

## Mechanics

**Probe-only**: `CheckHealth` calls `g.Refresh` and classifies the outcome — Healthy on success, NeedsReauth on RFC 6749 §5.2 error codes (invalid_grant / invalid_token / unauthorized_client), Unknown for transient network failures. Does NOT persist the refreshed credential; that's the caller's job. Pure probe so health-checks at TUI boundaries don't trigger surprise vault writes.

**Health surfacing pattern**: pickers expose an `AnnotateHealth(vault, checker)` post-construction method. Synchronous (1 refresh roundtrip per Google account) at TUI startup. Personal-proxy scale (~3 accounts max) → ~600ms worst case. If this becomes a startup bottleneck, future work moves it to a goroutine that updates via tea.Msg.

**Direct reauth**: `r` keystroke in the account picker emits `reauthRequestedMsg{email}`. Top-level model dispatches `auth.Auth(email, scopes, scopes, forceFresh=true)` (preserving granted scope set, getting fresh tokens), persists the new credential via `vault.Set`, fires `notifyProxyCacheClear` so the proxy uses new tokens immediately, refreshes the picker so the badge clears. Existing `r revoke` keystroke moved to `R` (capital) — the more common operation gets the lowercase letter.

## Cross-import note

`lib/tui` already imports `lib/provider/oauth` (for the scopes catalog), so the strict "tui as domain-neutral leaf" framing is moot. `tui.AccountHealth` is still string-typed (not aliased to `oauth.HealthState`) for API stability — adapter logic is one place (lib/charoncli's AuthCmd), and string strings have natural debug-print behavior.

## What can't be prevented

Worth noting in the design context (per nous#15's spec): refresh tokens aren't "kept alive" by charon. OAuth model: refresh tokens are exchanged for access tokens but not renewed by use. Lifetime is determined by Google's side (revocation, 6mo unused, password reset, OAuth client verification, suspicious activity, 200-token cap). Charon's arm/disarm doesn't affect this; arm/disarm only blocks proxy CONNECTs.

Implication: prevention isn't possible at charon's layer; the surface invests in **detection at session boundaries + one-keystroke recovery**. Background refresh-warming jobs wouldn't help — exercising a refresh token doesn't extend its lifetime.

## Cross-refs

- `nous/workshop/issues/000015-provider-auth-health-reauth-ux.md` — issue with full design rationale + milestone breakdown
- `nous/atlas/nous/lib-layout.md` — domain organization of lib/* (oauth lives under provider/)
- `nous/atlas/charon/index.md` — credential-proxy + provider-auth surface overview
