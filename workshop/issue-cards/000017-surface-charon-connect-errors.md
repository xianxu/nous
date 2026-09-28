---
id: 000017
status: open
created: 2026-05-09
updated: 2026-05-09
estimate_hours: 3
---

# Surface charon's CONNECT-time errors to agents

## Problem

Charon attaches well-formed structured errors to its 407 `Proxy Authentication Required` responses on `CONNECT`:

```json
{"error":"session_disarmed","fix":"charon arm   # or click the menubar dot in Charon Security.app"}
```

But agents (and humans driving CLI tools) can't see them. The HTTP `CONNECT` method's response-body convention is "tunnel-established or not"; most clients discard non-2xx response bodies before user code sees them:

- **curl**: prints `* Ignore 99 bytes of response-body` then `curl: (56) CONNECT tunnel failed`. Exits 56 (`recv failure`).
- **Go `http.Transport`**: returns `http: ... 407 Proxy Authentication Required` as a stringified status; no body access.
- **Python `requests`**: raises `ProxyError` with status code only.
- **Most language SDKs (anthropic, openai, googleapiclient)**: bubble up as a generic "proxy rejected the connection."

Surfaced 2026-05-09 testing `nous#15`'s reauth flow. After arming/disarming experiments, an agent attempting `read my last 10 emails via the proxy` saw an opaque connection failure. Root cause was `session_disarmed`; the agent had no way to know.

This isn't unique to disarm — same shape applies to:

- `session_disarmed` — operator paused the proxy via `charon disarm`
- `unknown_account` — `X-Charon-Account` doesn't match any vault entry
- `scope_required` — pre-validation rejected because `X-Charon-Scope` requested a scope not granted on that account
- `account_not_specified` — multi-account provider but no `X-Charon-Account` header

All currently surface as 407 + JSON body, all currently get eaten by the HTTP client layer.
