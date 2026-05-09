---
id: 000015
status: working
deps: [000014]
created: 2026-05-09
updated: 2026-05-09
estimate_hours: 4
---

# `nous provider` auth health + reauth UX

## Problem

The `nous provider` (charon-origin) TUI presents stored OAuth state as if it's *current* validity. The `[x]` checkmarks for granted scopes reflect what was granted at last auth — not whether the refresh token is still valid today.

Surfaced 2026-05-09 during nous#14 M3 smoke-testing. Sequence:

1. From an unrelated brain session, an agent tried "read my last 10 emails" via the proxy. Google returned 401.
2. Operator opened `nous provider` to investigate. TUI showed both Google accounts with `[x]` for granted scopes — looked healthy.
3. Operator entered an account, tried to reapply a scope to trigger a fresh state. Got `list projects: token supplier: oauth refresh: token refresh error: invalid_grant: Token has been expired or revoked.` — a wall-of-text error.
4. After several failed scope-toggle attempts, charon's existing fallback path eventually triggered a browser reauth flow. Recovery worked, but the path through the TUI was confusing.

Three failure modes layered:

- **Local state lies**: vault has the (now-dead) refresh token + cached scope list; TUI shows "ok" because that's what's stored. The fact that Google has revoked the refresh token is only discovered when an action actually calls Google.
- **No direct reauth path**: the only way the operator found to trigger a fresh OAuth is to toggle scopes until charon's failure-retry counter triggers the browser. No explicit "this account needs reauth — press R" surface.
- **Error rendering is raw**: the `invalid_grant` message is the OAuth library's error string, not user-facing language. The operator can't tell whether to fix something local or just reauth.

## Spec

Surface token health at three nested levels, plus an explicit reauth path, plus a TUI ergonomic fix.

### Level 1: provider list

Today the provider list shows configured providers and account counts. Add a per-provider "needs reauth" annotation derived from per-account health:

```
Google    OAuth    2 accounts (1 needs reauth)
Anthropic API key  1 account
```

### Level 2: account list (within a provider)

Per-account health badge:

```
Google
  lovchatvol@gmail.com  (needs reauth)
  xianxu@gmail.com
```

Selecting an account that needs reauth lands on a "this account's refresh token is invalid. Google says: <reason>. Press R to reauthenticate (opens browser), q to skip" view, NOT the scope-toggle screen.

### Level 3: permissions screen

When viewing scopes for a healthy account, show last-checked timestamp ("validated 30s ago"). When health goes stale during a session (e.g. token expired between TUI open and now), surface "stale, press R to revalidate" inline.

### Health-check mechanism

On `nous provider` startup (and on a periodic heartbeat while open), do a lightweight refresh-token validation per account:

- Call `oauth/refresh` with the stored refresh token. If 200 → healthy, cache fresh access token + extend cache TTL. If 4xx with `invalid_grant` → mark `needs reauth`. If network error → mark `unknown`, don't penalize the user.
- Cache the result in TUI state for the session. Don't re-validate on every keystroke; revalidate only on explicit user action (`r`) or after a stale window (e.g. 5 min).

This is cheap because the operator typically has 2–3 accounts. Worst case: 3 HTTP roundtrips at TUI startup, ~200ms total.

### Direct reauth action

Add an explicit `r` keystroke in the TUI (already shown in the cheatsheet bar): triggers fresh OAuth for the current account/provider. Routes through charon's existing browser-based authorization flow. Replaces the current "toggle scopes until charon's auto-fallback fires" workaround.

### Error rendering

When `invalid_grant` (or any other Google OAuth error) bubbles to the TUI, translate to user-facing language:

```
This account's authentication has expired or been revoked.
Press R to reauthenticate (opens browser).
Press q to dismiss.

(technical: token_refresh_error: invalid_grant: Token has been
expired or revoked. — visible if you want to debug)
```

The technical message stays accessible via a debug pane (e.g. press `?`) so the operator can dig in if needed, but isn't the primary surface.

### TUI ergonomic fix: Enter on unchanged permissions

In the scope-toggle screen, pressing Enter with no scope changes currently exits to the parent menu (account list). Counter-intuitive — Enter should be the apply action, and apply with no diff should be a no-op (stay on screen). Behavior change:

- If scope set unchanged from start: Enter is no-op, brief flash "no changes."
- If scope set changed: Enter applies (current behavior).
- `q` or Esc still exits to parent.

## Done when

- `nous provider` TUI startup performs the per-account refresh-token health check; results visible at provider list (`Google OAuth 2 accounts (1 needs reauth)`) and account list (per-account `(needs reauth)` badge).
- Selecting a `needs reauth` account lands directly on the reauth action prompt, not the scope-toggle screen.
- Pressing `r` from anywhere within an account view triggers the browser-based reauth flow (whatever charon already uses internally for auto-fallback).
- `invalid_grant` and similar OAuth errors are translated to "press R to reauthenticate" prose with the raw message hidden behind a debug toggle.
- In the scope-toggle screen, Enter with no diff is a no-op (does not exit). Enter with a diff still applies.

## Estimate

Range: **3–6 hr**. Best guess: **~4 hr**.

| Component | Total |
|---|---|
| Health-check on TUI startup (refresh-token call per account) | 0.75–1.5 |
| Wire health state into provider list + account list rendering | 0.5–1 |
| Direct reauth keystroke + flow surfacing | 0.75–1.5 |
| Error rendering (translate OAuth errors → user-facing) | 0.5–1 |
| Permissions-screen Enter no-op when unchanged | 0.25–0.5 |
| **+30% design buffer** | +0.5–1 |
| **Total** | **~3.25–6.5** |

Familiarity ×1: charon's TUI is bubbletea + lipgloss; pattern is known. The OAuth flow primitives already exist in `lib/provider/oauth/`; surfacing them is plumbing, not greenfield.

## Plan

### M1 — Health-check primitive in lib/provider/oauth

- [x] Added `(*GoogleProvider).CheckHealth(*vault.Credential) HealthState` returning `Healthy | NeedsReauth | Unknown`. Implementation calls `g.Refresh` and pattern-matches OAuth2 RFC 6749 §5.2 error codes (invalid_grant / invalid_token / unauthorized_client) to NeedsReauth; transient failures (network, 5xx) map to Unknown so transient infrastructure doesn't penalize the operator.
- [x] Probe-only — does NOT persist successful refreshes back to vault. Callers wanting to keep the new token call `g.Refresh` directly. Avoids unexpected vault writes on TUI event loops.
- [x] `FriendlyError(err)` helper translates raw OAuth errors to user-facing prose, preserving the raw string as the second return value for debug surfaces.
- [x] Live-tested against operator's actual vault: both Google accounts returned `healthy` (operator reauthed earlier). Unit tests cover the classifier + FriendlyError per error class.

### M2 — TUI health surfacing

- [x] Provider list (`providerPickerModel.AnnotateHealth`): appends `(N needs reauth)` to Google summary when any accounts fail, no-op when all healthy. `Google OAuth 2 accounts (1 needs reauth)`.
- [x] Account list (`pickerModel.AnnotateHealth`): per-account `(needs reauth)` badge inline; needs-reauth rows visually muted so healthy ones stand out as default targets.
- [x] Synchronous (1 refresh-token network roundtrip per Google account) at TUI entry. Personal-proxy scale (~3 accounts max) → ~600ms worst case at startup. Async goroutine + tea.Msg deferred until this is a real bottleneck.
- [x] Cross-import discipline preserved: `tui.AccountHealth` is string-typed (not aliased to `oauth.HealthState`); adapter lives in `lib/charoncli`'s AuthCmd. (Note: lib/tui already imports lib/provider/oauth for scopes catalog work, so the strict "tui as domain-neutral leaf" framing was already broken — the string-typed enum is still cleaner than aliasing.)

### M3 — Direct reauth path

- [x] `r` keystroke in account picker triggers fresh OAuth via `auth.Auth(email, scopes, scopes, forceFresh=true)` — preserves the existing scope grant set, just gets new tokens.
- [x] Existing `r revoke` moved to `R revoke` (capital). The more-common operation gets the lowercase letter; revoke is a destructive action and should require deliberate keystroke.
- [x] Cheatsheet: `↑↓ nav   enter open   r reauth   R revoke   esc back   q quit`.
- [x] After successful reauth: vault.Set writes new credential, `notifyProxyCacheClear` fires so the proxy uses the new tokens immediately, picker re-renders with refreshed health badge.
- [x] Errors translate via `oauth.FriendlyError` into the picker's `statusMsg`.
- [ ] **Deferred**: selecting a `needs-reauth` account via Enter to land on a "press R to reauthenticate" view. Today selecting drills into the scope view, where the friendly error then surfaces if any apply hits the bad token. Operator's reauth path: press `r` from the account list directly. Worth a follow-up if the scope-screen detour proves friction.

### M4 — Error rendering + Enter no-op

- [x] `viewApplyError` now renders `oauth.FriendlyError(applyErr)`'s user-facing message; raw error appears on a debug line below only when it differs (avoids double-printing for cases the translator didn't change).
- [x] Permissions-screen Enter with no scope diff is a no-op now: sets `applyStatus = "no changes — press q or esc to exit"` and stays on screen. Was: emitted `scopesQuitMsg` (exited to parent picker — counter-intuitive since Enter is the apply action everywhere else).
- [x] Test renamed and updated: `TestEnterNoChangeQuits` → `TestEnterNoChangeIsNoOp`. Asserts nil cmd, non-empty applyStatus, unchanged state.

### M5 — Verification

- [x] Synthetic tests in `lib/tui/health_test.go` (5 tests):
  - `TestPicker_AnnotateHealth_StampsBadgeOnUnhealthyAccount` — stubs the checker, asserts each item gets the right health.
  - `TestPicker_View_RendersNeedsReauthBadge` — asserts rendered string contains `(needs reauth)`.
  - `TestPicker_AnnotateHealth_NilCheckerIsNoOp` — passing nil keeps items in `Unchecked` state.
  - `TestProviderPicker_AnnotateHealth_AppendsCountToGoogleSummary` — asserts `(1 needs reauth)` lands in the Google row's summary string.
  - `TestProviderPicker_AnnotateHealth_NoBadgeWhenAllHealthy` — no false positives.
- [ ] **Manual e2e test (operator)**: revoke a token via `myaccount.google.com → Security → Third-party apps`, then `~/workspace/nous/bin/nous provider`. Expected: `Google OAuth N accounts (1 needs reauth)` at top, per-account `(needs reauth)` badge muted, press `r` on the bad account, browser opens, complete OAuth, verify badge clears + status reads "reauthenticated <email>". Punted to post-merge dogfood; the synthetic suite proves the wiring.

## Notes

- **Out of scope**: token rotation policy (auto-rotate every N days regardless of validity), refresh-token-expiration-warnings (e.g. "this token expires in 7 days"), Google's session-timeout settings. All worth considering eventually but not load-bearing for the immediate UX gap.
- **Why filed as its own issue, not folded into nous#14**: nous#14 is about CLI/TUI structure (cobra surface, lib organization, service unification). This is about the *auth state model* itself — different concern, different files (lib/provider/oauth, lib/charoncli's TUI views), no dependency on nous#14's M4-M5 work.
- **Not in shared-brain mvp_scope**: `nous provider` is the credential proxy surface; brain-shared-family uses gcrypt+SSH for git, not OAuth. Shared-brain done-when isn't gated on this. Tracked separately as a charon-side UX improvement.

### Why we can't prevent refresh-token death (only detect it)

Worth surfacing the OAuth model so anyone implementing this doesn't waste time looking for a "keep tokens warm" path:

- **Refresh tokens are not refreshed by use.** Google issues one at initial auth; charon stores it and exchanges it on-demand for new short-lived access tokens. Frequent use exercises the refresh token but doesn't extend its lifetime — there's no notion of "renewing" it.
- **Lifetime is determined by Google's side**: explicit user revocation (Google Account → Security → Third-party apps), 6 months unused (n/a for active accounts), password reset on the user, OAuth client verification-status changes, suspicious-activity flags, or hitting Google's per-user-per-client refresh-token cap (~200 active).
- **Charon's arm/disarm doesn't affect this**: disarmed = refuse incoming proxy CONNECTs; it doesn't reach into the OAuth flow or invalidate stored tokens. Re-arming after disarmed period uses the same stored refresh token, which is still subject to Google-side validity.

Implication: the design has to assume tokens can die externally between sessions. **Active health-check at TUI startup** is the right primitive (detect early); there's no upstream prevention to invest in. No background refresh-warming job; that wouldn't help.

## Log

### 2026-05-09 — created
Surfaced during nous#14 M3 smoke-testing. Operator opened `nous provider` after Google API returned 401 in an unrelated session; the TUI's local-state checkmarks suggested healthy auth, but action triggered `invalid_grant`. Recovery worked (charon's existing auto-fallback to browser reauth) but the path was confusing. Operator outlined the four UX improvements + asked whether direct reauth-flow trigger is achievable (yes — charon already has the code, just needs surfacing). Filed for follow-up.
