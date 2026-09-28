---
id: '000015'
status: done
created: 2026-05-09
updated: 2026-05-09
estimate_hours: 4
actual_hours: 2
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
