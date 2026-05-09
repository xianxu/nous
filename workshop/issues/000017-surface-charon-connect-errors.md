---
id: 000017
status: open
deps: [000014]
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

## Spec

Three approaches that get the error to the agent. Likely M1 + M2 ship together; M3 is the durable agent-facing mechanism.

### M1: `nous proxy last-errors` — log-introspection CLI

```sh
$ nous proxy last-errors --peer-pid 12345
2026-05-09T16:18:21Z  CONNECT gmail.googleapis.com:443  407  session_disarmed  fix: charon arm
2026-05-09T16:15:52Z  CONNECT gmail.googleapis.com:443  407  session_disarmed  fix: charon arm
```

Reads `~/Library/Logs/charon.log` (already structured JSON), filters by peer PID or last-N, formats human-readably. Cheap surface (~30 lines of code) — useful for both humans and agents post-failure.

For agents: when a tool returns a generic "proxy rejected the connection," call `nous proxy last-errors --peer-pid $$` to learn the actual cause. Add to the charon skill's instructions.

### M2: friendlier `charon run` wrapper

When the wrapped command exits with a "looks like proxy rejected" signature (exit 56 from curl, specific Go errors, etc.), `charon run` post-mortems by querying its own logs and surfacing the most recent CONNECT failure for this peer:

```sh
$ charon run -- curl https://gmail.googleapis.com/gmail/v1/users/me/profile
charon: proxying through 127.0.0.1:8230
curl: (56) CONNECT tunnel failed, response 407

charon: detected CONNECT failure. Most recent rejection for this peer:
  407 session_disarmed
  Fix: charon arm   # or click the menubar dot in Charon Security.app
```

Agents using `charon run` (which is the recommended wrapping path per `charon instructions`) automatically get the structured error — no skill change needed.

### M3: `X-Charon-Probe-Connect` pre-flight (optional)

Agents that aren't using `charon run` (e.g., raw HTTPS through a different SDK that sets the proxy via env) can pre-flight before the real call:

```
GET http://127.0.0.1:8230/probe/connect
Headers:
  X-Charon-Account: lovchatvol@gmail.com
  X-Charon-Scope:   gmail.readonly
  X-Charon-Target:  gmail.googleapis.com
```

Returns the same `{error, fix}` JSON body but as a regular HTTP response (not via CONNECT). Healthy path returns 200 + `{ok: true}`. Agents pre-flight before doing anything expensive.

This is the durable agent-facing primitive; M1/M2 are operator/wrapper-facing.

## Done when

- `nous proxy last-errors` (or equivalent) returns the most recent CONNECT-rejection details, filterable by peer PID. Reads from charon's existing audit log; no new state.
- `charon run` post-mortems CONNECT failures and prints the structured error after the wrapped command exits.
- `charon instructions` (and the embedded skill guide) document both paths.
- Optional: `/probe/connect` endpoint for SDK-driven pre-flight.

## Estimate

~3 hr. Mostly composition over existing pieces (charon's audit log already captures the right info; the wrapper around `charon run` is straightforward; the probe endpoint is a sibling of charon's existing healthz).

## Plan — sketch

### M1 — `nous proxy last-errors` CLI

- [ ] Read `~/Library/Logs/charon.log` (JSON Lines), filter to entries where `status >= 400` (or just `error != ""`).
- [ ] Filter by `--peer-pid` (default: any), `--last N` (default: 5), `--format json|text`.
- [ ] Format: timestamp + method + host + status + error + fix-hint.
- [ ] Surface as `nous proxy last-errors`. (Lib lives in `lib/provider/proxy/`.)

### M2 — `charon run` post-mortem

- [ ] After the wrapped command exits, if exit code is in {56, 7, 22} (curl) or matches Go's `http.ProxyConnectError` shape, query the audit log for the most recent rejection from this PID.
- [ ] Print the structured error to stderr after the command's normal output.
- [ ] No-op when the wrapped command succeeded.

### M3 (optional) — `/probe/connect` endpoint

- [ ] Add a non-CONNECT path on charon's HTTP listener that runs the same auth/scope/account validation as the real CONNECT, but returns the result as a regular HTTP response.
- [ ] Document in `charon instructions` as an opt-in for SDK callers that can't use `charon run`.

## Notes

- **Why this is its own issue, not folded into nous#15**: refresh-token health (`#15`) is one specific cause of CONNECT failures; this issue is the broader "structured CONNECT errors don't reach agents" surface. Different ergonomic concern even though both surface in the same downstream agent failure.
- **Audit log as the substrate**: charon already writes one JSONL line per CONNECT attempt (success or failure) to `~/Library/Logs/charon.log`. The data is there; the gap is exposure, not collection.
- **Why the 407 body convention is correct, not a bug**: charon's response shape follows HTTP. The fix isn't "stop using 407+body" but "give agents another channel to read the body."

## Log

### 2026-05-09 — created
Surfaced while testing nous#15's reauth flow. Operator (and an unrelated brain-side agent) saw "agent can't read email" — investigation showed charon was disarmed; the agent received a generic CONNECT failure and couldn't tell. Charon's actual response body (`{"error":"session_disarmed","fix":"charon arm ..."}`) never reached the agent because curl/SDK CONNECT machinery discards non-2xx response bodies.

Confirmed by raw socket: `printf 'CONNECT ... HTTP/1.1\r\n\r\n' | nc 127.0.0.1 8230` shows the well-formed 407 + JSON body. So charon is doing the right thing on its side — the gap is in agent-side surfacing.
