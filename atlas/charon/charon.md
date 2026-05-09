# Charon: Credential Proxy

## What
Single Go binary that acts as a fully transparent HTTPS forward proxy. AI agents route traffic through it via `HTTPS_PROXY`, and Charon transparently injects credentials into requests. The agent never sees the token, uses real API URLs, requires no code changes.

## Scope (what's protected, what isn't)
Charon protects **credentials** (OAuth tokens, the proxy CA private key, the signing key), not **user content**. An agent running as your user can read your home directory and spawn processes by default — Unix said yes. If you need content protection, run the agent in a sandbox (Apple's sandbox framework, a container runtime, a VM). Charon's job is narrower: stop credential exfiltration that would let the agent act as you against every API you've authorized. See [`../docs/threat-model.md`](../docs/threat-model.md) for the full posture.

## Architecture
```
charon serve          → starts HTTPS proxy on localhost:8230
                        generates persistent CA cert (~/.config/charon/ca.pem)
                        builds combined CA bundle (system CAs + charon CA)

charon auth google x  → OAuth flow: browser → callback → tokens in keychain

charon run -- <cmd>   → sets HTTPS_PROXY, SSL_CERT_FILE, REQUESTS_CA_BUNDLE, etc.
                        exec's child process — fully transparent

Agent → HTTPS request (real URL) → Charon proxy (CONNECT + TLS interception)
                                     → injects credential → upstream API
                                     ↕                ↕
                                OS Keychain      OAuth refresh
                                (macOS)          (automatic)
```

## Key Components
- `cmd/charon/main.go` — CLI (cobra): `serve`, `run`, `auth`, `accounts`, `status`, `vault set/delete`
- `internal/vault/vault.go` — `Store` interface + `Credential` type with expiry logic
- `internal/vault/keychain/` — macOS Keychain backend (pure Go, via `security` CLI)
- `internal/vault/memory/` — in-memory backend for testing
- `internal/proxy/proxy.go` — HTTPS forward proxy with CONNECT interception + credential injection + auto-refresh
- `internal/proxy/cert.go` — persistent CA (`LoadOrCreateCA`), per-host cert generation with DNS/IP SAN support
- `internal/proxy/cabundle.go` — builds combined CA bundle (system CAs + charon CA)
- `internal/proxy/routing.go` — host → `Provider` mapping with pluggable `AuthMethod`
- `internal/proxy/audit.go` — append-only JSON lines audit log + bounded in-memory ring (5000 entries) for `charon who/stats` queries
- `internal/proxy/session.go` — runtime-consent state (armed/disarmed bit + idle/absolute timers); see "Runtime consent" below
- `internal/proxy/runtime_socket.go` — DR-pinned unix-domain RPC for `charon arm/disarm/who/stats` and the security.app menubar
- `internal/proxy/peerinfo.go` — best-effort caller identification (lsof+ps); display-quality, never auth-quality
- `internal/proxy/stats.go` — request/response byte counts + generic JSON top-level item count (Tier 1 + Tier 2)
- `internal/oauth/google.go` — Google OAuth flow: browser auth, local callback, token exchange, refresh with rotation, ID token email extraction
- `internal/oauth/scope_catalog.go` — known Google scope definitions with short names
- `internal/oauth/obfuscate.go` — XOR encode/decode for baked-in client credentials (same mechanism as brain)
- `internal/proxy/scope_tracker.go` — scope denial tracking (ring buffer) + scope enforcement helpers

## Credential Flow
```
request host → Provider (routing table) → {provider.Name, account} → token (vault/cache)
  → if expired and has refresh_token → Refresher.Refresh() → updated token + vault persist
  → InjectAuth (bearer header)
```
- Routing: exact host match first, then suffix match (e.g. `*.googleapis.com` → `{google, bearer}`). The Google suffix rule transparently covers Vertex AI (`{region}-aiplatform.googleapis.com`) and the API Keys mint endpoint (`apikeys.googleapis.com`) — OAuth bearer with the `cloud-platform` scope authenticates both. AI Studio (`generativelanguage.googleapis.com`) is an *exact-match override* mapped to `{google-aistudio, query}`: charon attaches `?key=<cred.AIStudio.KeyMaterial>` instead of an Authorization header. The credential lookup uses `VaultProvider="google"` so the same Google OAuth credential carries both bearer tokens (for Vertex/Workspace) and the AI Studio key sidecar.
- Auth methods (#15): three styles dispatch from `proxy.Provider.Auth`. `bearer` sets `Authorization: <prefix><token>` (default prefix `Bearer `; overridable for providers like Replicate using `Token <key>`). `header` sets a custom header named by `HeaderName` (e.g. `x-api-key` for Anthropic). `query` appends a URL parameter named by `HeaderName` (default `key`). All three apply `ExtraHeaders` for static decoration (e.g. `anthropic-version: 2023-06-01`). The catalog wires Tier-3 providers through these three styles without per-provider Go code; compiled providers (Google OAuth, OpenAI admin-key) keep their existing routing.Provider entries which always win on hostname overlap.
- **Consumer-project setup (one-time, by whoever owns charon's OAuth client).** GCP APIs check API enablement on the *consumer's* project — i.e. the GCP project that hosts the OAuth client_id charon ships with, not the user's project. For the OAuth client embedded in this binary, that project is `brain-494300`. The following APIs must be enabled on it once before any user can complete `cloud-platform` setup: `cloudresourcemanager.googleapis.com` (#14 M3 list/create), `serviceusage.googleapis.com` (M3 enable APIs), `cloudbilling.googleapis.com` (M3 billing detection), `apikeys.googleapis.com` (M4 AI Studio mint). Failure mode without this: confusing "API not used in project 998387738" errors at flow-time. URLs of form `https://console.cloud.google.com/apis/library/<api>?project=brain-494300` open straight to the Enable button.
- Account resolution: single account auto-selected; multiple requires `X-Charon-Account` header
- Token cache: in-memory `sync.Map`, keyed by `provider:account`, respects expiry with 30s grace
- Cache invalidation: `vault set/delete` POSTs to `/cache/clear` on the proxy

## OAuth
- Google OAuth 2.0 installed app flow (client_id + client_secret baked in, XOR-obfuscated)
- `charon auth google [email]` — opens browser, email auto-detected from ID token; optional email used as `login_hint`
- Tokens stored in macOS Keychain (refresh_token persisted, access_token cached in memory)
- Auto-refresh: proxy detects expired tokens, calls `Refresher.Refresh()`, persists rotated refresh tokens
- Incremental scopes via `charon auth google grant user@gmail.com scope1 scope2`
- `Refresher` interface: pluggable per-provider, wired into `Server.Refreshers` map

## Auth Method (pluggable)
```go
type AuthMethod string
const AuthBearer AuthMethod = "bearer"  // Authorization: Bearer <token>
// Future: AuthBasic, AuthHeader, AuthQuery, AuthAWSSigV4
```
Currently only `bearer` is implemented. Each `Provider` has an `Auth` field and an `InjectAuth` method that dispatches on it.

## Transparent Proxy Model (like Infisical Agent Vault)
- `charon run -- python agent.py` wraps child with proxy env vars
- Agent code uses real URLs (`https://gmail.googleapis.com/...`), no changes needed
- CONNECT tunneling: known hosts get TLS interception + credential injection; unknown hosts get raw passthrough
- HTTP keep-alive supported within a CONNECT tunnel (multiple requests per connection)
- CA trust handled automatically via `SSL_CERT_FILE`, `REQUESTS_CA_BUNDLE`, `NODE_EXTRA_CA_CERTS`, `CURL_CA_BUNDLE`, `GRPC_DEFAULT_SSL_ROOTS_FILE_PATH`

## HTTP/2 Downgrade
The MITM proxy operates at HTTP/1.1 on both sides (client↔proxy and proxy↔upstream). Upstream connections force HTTP/1.1 via `TLSNextProto: {}` on the transport. This is standard for MITM proxies (mitmproxy, Infisical Agent Vault do the same) — the MITM reads/writes HTTP/1.1 text framing, which is incompatible with HTTP/2 binary frames. The practical impact is negligible: API latency (80-200ms) dwarfs any protocol overhead, and keep-alive provides connection reuse.

## Multi-account
Agent sends `X-Charon-Account: user@gmail.com` header to select account when multiple exist for same provider. Charon strips it before forwarding.

## Agent-side protocol

Canonical spec for tools that call APIs through charon:
[`docs/agent-protocol.md`](../docs/agent-protocol.md). Covers headers
(`X-Charon-Account`, `X-Charon-Scope`), 407 handling and the `fix`
command, scope discovery strategies for Google, and per-provider
sections.

## Scope Management
- **Scope enforcement**: Callers can declare required scopes via `X-Charon-Scope: gmail.readonly,calendar.readonly` header. Proxy checks against granted scopes and returns 407 with structured JSON error on mismatch.
- **Scope tracking**: Proxy tracks scope denials (bounded ring buffer, 100 entries, 24h expiry). Exposed via `/scopes/denied` endpoint.
- **Scope catalog**: `charon auth google scopes` lists known Google scopes. `charon auth google scopes user@gmail.com` shows granted scopes.
- **Grant command**: `charon auth google grant user@gmail.com calendar.readonly` triggers incremental OAuth.
- **Fix command**: `charon auth fix` / `charon auth google fix [email]` queries proxy for denied scopes and offers to grant them interactively, one provider×account pair at a time.
- **Scope resolution**: Short names (e.g. `calendar.readonly`) resolve to full URLs (e.g. `https://www.googleapis.com/auth/calendar.readonly`).

## Design Decisions

### Credential lifecycle principle: manage what you mint, revoke what you touched

Charon distinguishes two classes of credentials it stores:

1. **Minted by charon** — created via the provider's admin API (OpenAI
   service-account keys, Google AI Studio keys, etc.). Charon has full
   lifecycle responsibility: it created the credential, knows its
   upstream id, and can list/revoke it via the same admin API.
2. **Pasted into charon** — created by the user in the provider's
   dashboard, then pasted into charon (catalog/Tier 3 long-tail
   providers; also Anthropic, since their Admin API can list and
   deactivate keys but cannot create them). Charon doesn't own the
   credential's existence.

**The principle**: *charon manages what it minted; charon revokes what
it touched.*

For minted credentials: full lifecycle. Mint, list, revoke — all
through the provider's admin API. Deletion in charon propagates
upstream.

For pasted credentials: charon doesn't pretend to own them. Deletion
in charon is local-only. **Exception**: revocation is offered
best-effort even for pasted keys, because charon read the key
material and is therefore on the hook if it leaks. When the
provider exposes a deactivate / revoke endpoint (e.g. Anthropic's
`POST /v1/organizations/api_keys/{id}` with `status: inactive`),
charon uses it. When no such endpoint exists, charon tells the user
"removed locally; please clean up at provider's dashboard" and
points them at the right URL.

The catalog (#15) declares per-provider revoke endpoints so this
distinction is data-driven.

#### Catalog providers (Tier 3) — paste-and-revoke

The catalog at `internal/providers/catalog/catalog.yaml` is the
data-driven mechanism for the long-tail of API-key providers
that don't justify per-provider Go code. Anthropic seeds the
catalog today; new entries land as one-line YAML PRs (Groq,
Mistral, xAI, etc.).

Lifecycle posture for catalog credentials:
- **Add**: user pastes key in TUI, optionally verified against the
  entry's `verify_url` (M5: rejected → retype, inconclusive → store
  with degraded note, OK → store with "verified" note).
- **Route**: hostname → catalog entry → vault lookup by
  `X-Charon-Account` → auth shape (bearer / header / query) applied
  with optional `extra_headers`. Compiled providers win on hostname
  overlap; collisions are rejected at boot.
- **Revoke**: best-effort upstream when the entry declares a
  `revoke` endpoint (direct or list-then-deactivate). Default-preserve
  on upstream failure — the credential is charon's *handle* on the
  upstream key, throwing it away on transient failure forces a
  re-paste. Explicit `[d]` force-delete affordance for cases where
  retry will never succeed.

The full catalog reference (schema, validation, how to add an
entry) is in [`docs/providers.md`](../docs/providers.md).

### Other decisions
- **CGo on darwin for keychain access** (was: pure Go via `security` CLI; revisited in #000003 because the CLI shell-out makes keychain ACLs meaningless — the requesting process becomes `/usr/bin/security`, not charon). Build-tag split: darwin+cgo uses Security framework directly via `github.com/keybase/go-keychain` for Get/Delete/List + direct CGo (`acl_darwin.go`) for ACL'd Set; `!cgo || !darwin` keeps the legacy CLI shell-out for hermetic CI / cross-compile.
- **File-backed dev vault** (`internal/vault/keychain/dev_file.go`) — when the running binary is unsigned (ServiceDev), all keychain ops route to `~/.local/share/charon/dev-vault.json` instead of the macOS Keychain. Avoids the keychain-permission-prompt-per-rebuild friction and prevents training users to click "Always Allow" on every dev build. ServiceProd unchanged — production binaries still use the codesign-DR-pinned ACL on macOS Keychain.
- **Persistent CA** — stored in `~/.config/charon/`, reused across restarts
- **HTTP/1.1 forced upstream** — necessary for HTTP/1.1 MITM, standard practice
- **Chunked re-encoding** — Go's transport dechunks upstream responses; proxy re-adds `Transfer-Encoding: chunked` when `ContentLength < 0` so clients know where the body ends
- Token stored as JSON in keychain; access token cached in memory
- Health endpoint at `/healthz`, CA download at `/ca.pem`, cache clear at `/cache/clear`
- Auth method configurable per provider, defaults to bearer

## Test Coverage (90+ tests)
- **CLI** (17) — all commands, flags, validation, help text, proxy lifecycle
- **Proxy** (9) — HTTP/CONNECT injection, passthrough, multi-account, health, CA endpoint
- **Scope enforcement** (7) — scope granted/missing, multiple scopes, denial tracking, /scopes/denied endpoint
- **Scope tracker** (5) — track, filter, expiry, max size, missing scope detection
- **Refresh** (4) — auto-refresh on expiry, failure fallback, vault persistence, no-refresher case
- **Cache** (8) — expiry simulation with mock clock, cache clear, account resolution, vault fetch count
- **Keep-alive** (2) — 5 requests to same host = 1 CONNECT tunnel; different hosts = separate tunnels
- **Routing** (6) — all Google hosts, unknown hosts, InjectAuth dispatch
- **CA/Cert** (5) — generation, persistence, DNS/IP SANs, serial uniqueness
- **Audit** (4) — JSON lines format, append mode, default path
- **Vault** (8) — expiry logic with `IsExpiredAt` (7), grace period boundary
- **Memory store** (2) — CRUD, not-found
- **Keychain** (5) — `security` CLI contract tests (flags/subcommands exist)
- **OAuth** (14) — ID token parsing (7), login_hint (2), scope merging, required scopes, scope catalog (3), XOR
- **Keychain integration** (5) — behind `integration` build tag

## Zero-Config Deployment
Single binary, everything in keychain:
- CA cert + key → keychain (account: `_ca:cert`, `_ca:key`)
- OAuth credentials → keychain (account: `provider:email`)
- CA bundle → ephemeral temp dir, regenerated on each `serve` start
- Audit log → stderr by default, `--audit-log <path>` for file output
- No config directory needed

## Keychain Service Namespace + ACL (#000003)

The keychain service-name is resolved at runtime from the binary's own
codesign state:
- Signed `make install` binary (codesign `--identifier com.charon.cli`,
  signed by `Charon Self-Signed` self-signed cert) → service `charon`
- Anything else (`go build`, `go run`, `go test`, ad-hoc-signed with a
  different identifier) → service `charon-dev`

So a dev binary and the installed binary never collide on state, and a
dev rebuild can't read or accidentally overwrite the prod entries.

Entries written under `charon` get a SecAccess (legacy macOS keychain
ACL) whose trusted-applications list pins to the current process's
designated requirement. Any other reader — including
`security find-generic-password` — triggers the macOS Allow/Deny
dialog. Entries written under `charon-dev` skip the ACL (dev iteration
from many ephemeral binaries with non-matching DRs would otherwise
lock itself out).

Writes go through `SecItemUpdate` first (atomic in-place data swap;
preserves the existing ACL), falling back to `SecItemAdd` only on
`errSecItemNotFound`. Token rotation never opens a delete-then-add
window during which the ACL would be absent.

**Load-bearing observation, not Apple-spec**: SecItemUpdate is documented
to leave attributes-not-in-the-update-dict unchanged; we rely on this
applying to the kSecAttrAccess (SecAccess/ACL) attribute too. Verified
empirically by integration test (`acl_darwin_test.go`); if a future
macOS release changes that, the ACL would need to be re-attached on
every update.

**Deprecated APIs in use**: `SecTrustedApplicationCreateFromPath` +
`SecAccessCreate` are deprecated since macOS 10.10 but remain
functional for legacy file-based keychains (login.keychain-db). Modern
`SecAccessControlCreateWithFlags` is for biometric/passcode gating, a
different use case. Deprecation warnings suppressed via cgo
`-Wno-deprecated-declarations`.

## Runtime consent (#16)

Proxy carries a single `armed` / `disarmed` bit. Disarmed CONNECT
and HTTP requests are rejected with `407 session_disarmed` *and
recorded in the audit ring* — denied requests still surface in
`charon who --since 5m` so the user can see what tried to talk
through the proxy while they were away. `charon serve` boots
disarmed; persisting armed state across restarts would defeat the
whole point.

Two timers gate an armed session, both in `internal/proxy/session.go`:
- **Idle TTL — 30m.** Resets on every gate evaluation, which fires
  once at CONNECT setup and once per plain-HTTP request. Requests
  multiplexed inside an already-open MITM tunnel do *not* re-check
  the gate, so a long-lived keep-alive tunnel with intermittent
  internal activity can still see the idle timer lapse — at which
  point the *next* CONNECT is rejected (existing tunnels drain by
  design; agents tolerate TCP RST poorly).
- **Absolute cap — 8h.** Hard ceiling regardless of activity; a
  chatty agent can't keep the session alive forever.
- **Default TTL — 1h** when arm is called without an explicit
  duration. Effective expiry is the min of the three.

Trust between the proxy and the consent oracle is a unix-domain
socket at `~/Library/Caches/charon/runtime.sock` (perms 0600). The
proxy reads the connecting peer's PID via `LOCAL_PEEREPID` and
verifies its codesign DR matches `com.charon.security` — same
DR-pinning mechanism as the keychain ACL. Unsigned dev binaries
auto-bypass (so `make dev` still works); signed prod requires the
real `Charon Security.app` on the other end.

The consent oracle ships in the same `Charon Security.app` bundle
as the audit tool — `nous-security menubar` (the no-args default
when launched via Finder) shows a status icon (●/○ + remaining
TTL) with arm/disarm options. Native notifications via
UserNotifications.framework, attributed to `com.charon.security` so
the user can pick Banner vs Alert style scoped to charon. See
[`atlas/security-audit.md`](security-audit.md) for the bundle.

Caller identification is best-effort and **never on the auth path**.
At CONNECT time the proxy resolves the peer process via lsof+ps
(PID, exe path, argv0, parent chain up to launchd). Logged as
observed for display in `charon who`; a fork-exec race between
accept and lookup could mis-attribute a single request — the design
accepts that because the gate is the user click, not the peer
identity.

Stats live in the same audit entry. Tier 1 (always populated): exact
`req_bytes`, `resp_bytes`, `resp_content_type`. Tier 2 (when JSON
and < 1 MiB): generic top-level array count → `items_returned`.
Bigger or non-JSON responses pass through with `items_returned`
absent. **Posture shift**: the proxy now reads response *content*
to count items (it always *could* — TLS-MITM is the whole design —
but #16 makes that explicit). Only counts and byte sizes are
logged; never JSON keys or values. See `docs/threat-model.md`
"Content sampling" for the explicit statement.

## Logging
- Normal mode: startup info and errors only
- `charon serve -v`: debug logging (TLS handshakes, per-request details, connection close reasons)
- Audit log: JSON lines to stderr (method, host, path, status, latency, provider, account)

## Scope-Management TUI (`charon auth`)

The interactive TUI is the canonical UX for OAuth scope management
(see #000005). Replaces the legacy `auth google scopes/grant/fix`
command family.

```
charon auth                                            # opens picker
```

Picker → pick existing account or "+ new account" → scope view:
- Search bar at top with substring filter (case-insensitive on short
  name + description)
- Catalog rows + custom keychain scopes + proxy-requested scopes
- Color matrix: muted grey (off), muted yellow (off + requested by
  proxy), normal (granted), green (toggled on, pending), red (toggled
  off, pending)
- Persistent session markers: `+` for scopes added in this session,
  `-` for scopes removed in this session
- Required scopes (openid, email) display as `[x] foo (req)`, can't
  be toggled off

Key bindings (see help line at the bottom of the TUI):
- search focus: type to filter, ↓/enter → list, ^r revoke account, esc quit
- list focus: ↑↓ nav, space toggle, enter apply, a add custom URL,
  ^r revoke account, / search, q quit

Apply paths:
- target == realized: no-op exit
- target ⊋ realized (additive): incremental OAuth (`include_granted_scopes=true`)
- target has any reduction: confirmation modal → fresh OAuth
  (`include_granted_scopes=false`) so the new token covers exactly the
  requested set, not the union of past grants
- ^r (revoke account): confirmation modal → calls Google's revoke
  endpoint, deletes the credential from the keychain, exits

### Window-resize contract

`model.Update` (`internal/tui/model.go`) caches `tea.WindowSizeMsg`
dimensions on the parent and dispatches the message to whichever
sub-model is `m.current`. On every screen transition (when
`m.current` changes) it batches a synthetic `WindowSizeMsg` carrying
the cached dimensions so a freshly-opened screen sees real width/
height on its first frame instead of zero — which matters for any
sub-model that lays out against terminal size (today
`scopesModel`'s row windowing; tomorrow anything else). New
sub-models don't need a forwarding branch in the parent; just handle
`tea.WindowSizeMsg` in their own `Update`. See #000020.

### TUI environment knobs

- `CHARON_TUI_HEIGHT=N` — manual height override (raw value, no -1
  margin). Useful when the multiplexer doesn't keep PTY size in sync
  with the visible pane.
- `CHARON_TUI_NO_ALT=1` — disable alt-screen mode. For diagnosing
  terminals where alt-screen interacts badly with bubbletea's render diff.
- `CHARON_TUI_DEBUG=1` — log every render and key event to
  `/tmp/charon-tui-debug.log`. Off by default (zero overhead).

## CLI
```
charon serve [-v] [--audit-log path]                  # start proxy
charon run -- <cmd>                                    # run child with proxy env
charon auth                                            # scope-management TUI
charon manifest                                        # JSON: proxy {addr,url,ca_pem_url} + granted scopes (single-shot snapshot for agents)
charon instructions                                    # Markdown: agent-using-charon guide, embedded in the binary (always matches installed version)
charon scopes                                          # JSON: catalog of known scopes per provider (what's grantable)
charon status                                          # check proxy
charon vault set/delete                                # manual token management
charon arm [--ttl 1h] / disarm                         # runtime-consent gate (#16); CLI fallback for the menubar
charon who [--since 1h] [--json]                       # recent peer activity (group-by-exe view)
charon stats [--since 1h] [--json]                     # aggregate (exe,host) → calls, items, bytes
charon service install/uninstall/start/stop/status     # OS service management
```

## Status
- M1 (proxy + keychain + manual token + `charon run`): done
- M2 (OAuth + auto-refresh + keep-alive): done
- M3 (wildcard routing + auth remove + zero-config + integration test): done
- M4 (scope management + auth flow improvements): done (#000004)
- M5 (scope-management TUI replaces legacy auth subcommands): done (#000005)
- Future: multi-provider (#000006), scope catalog with categories +
  filter syntax (#000007), synthesize denials from upstream 403s
  (#000008), Linux secret service (#000002), code signing + Keychain
  ACL (#000003), PKCE
