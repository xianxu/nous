# Charon Security Audit Tool

## What

`charon-security` is a two-purpose helper bundled into a single
`Charon Security.app`:

1. **Hygiene auditor** (`charon-security check` / `make security`)
   — verifies that the environment charon's threat model assumes
   actually holds: SIP enabled, no TCC grants on terminals/IDEs,
   charon's keychain ACL boundary intact, no suspicious launchd
   persistence. Prints actionable remediation when it doesn't.
2. **Runtime-consent oracle** (`charon-security menubar`, the
   no-args default since #16) — macOS menubar agent that arms/
   disarms the proxy's `Session` gate. Distinct bundle ID
   (`com.charon.security`) means the bundle is its own trust
   anchor for the proxy's DR check on the consent socket.

Distinct from charon proper: charon is the credential proxy; charon-
security is a separate binary in `cmd/charon-security/` that audits
the surrounding environment and acts as its consent oracle. Lives in
this repo because charon is the privacy-sensitive piece of the stack
and reuses charon's keychain ACL inspection helpers.

## Architecture

```
make security-install       → builds bin/charon-security, packages as
                              ~/Applications/Charon Security.app, signs
                              with Charon Self-Signed (hardened runtime).

make security               → runs the bundled binary. Pure run, never
                              re-signs (re-signing changes cdhash, which
                              invalidates TCC grants).

charon-security check       → audit subcommand (hygiene scan)
charon-security remedy <id> → look up remediation for one finding class
charon-security remedy      → print full playbook (10 entries)
charon-security menubar     → runtime-consent oracle (#16); no-args default
                              when launched via Finder/`open` (LSUIElement=true
                              keeps the bundle dock-less in this mode)
```

`make security` is the user-facing entry for the audit; the menubar
mode launches automatically when the user double-clicks the .app or
opens it via `open`. The rest are knobs for dev iteration / CI /
scripted use.

## Why a `.app` bundle

TCC keys permissions on the **responsible code**. A bare Mach-O CLI run
from Terminal gets attributed to `com.apple.Terminal` in TCC. Granting
Full Disk Access to "the security tool" in that scenario actually
grants it to Terminal — and revoking would nuke Terminal's FDA. The
`.app` bundle with its own bundle ID (`com.charon.security`) makes
TCC see the auditor as a distinct actor.

Bundle ID is intentionally distinct from `com.charon.cli`: revoking
TCC grants for the auditor must not affect charon, and vice versa.

## Check layers

Three layers, evaluated in order:

1. **Privilege-free** (`internal/security/check_*.go`):
   - `csrutil status` — SIP enabled?
   - `sudo -nv` — sudo cache active in this shell?
   - `~/Library/LaunchAgents` enumeration — third-party persistence?
   - `codesign -d --entitlements -` per detected app — hardened-runtime
     weakening entitlements (A5 in threat-model)?
   - Filesystem + `mdfind` — which terminals/editors/IDEs are installed?
2. **TCC** (`internal/security/check_tcc.go`):
   - User + system TCC.db read via `/usr/bin/sqlite3`. Joins rows
     against detected apps. FDA / Accessibility on terminal=Critical;
     Screen Recording=Important; AppleEvents to Keychain Access /
     1Password / Bitwarden / etc.=Critical, others=Important.
   - Requires FDA on the bundle. Macos 26 limitation deferred to
     [#000011](../workshop/issues/000011-apple-developer-id.md).
3. **Charon-specific** (`internal/security/check_charon.go`):
   - Inspects keychain entries under both `charon` and `charon-dev`
     namespaces via `keychain.Store.InspectACL`. Verifies the M4
     SecAccess pinning is intact: `(0,0)` → Critical, `(>0,1)` →
     healthy (silent), `(>0,N>1)` → Important.

## Severity tiers and exit codes

| Tier      | Exit | Meaning                                           |
|-----------|------|---------------------------------------------------|
| Critical  | 2    | Direct compromise of charon's threat-model.       |
| Important | 1    | Meaningful weakening; not catastrophic alone.     |
| Info      | 0    | Observational — user judgment.                    |
| Hygiene   | 0    | General macOS-app-hygiene; not charon-specific.   |

`--strict` promotes every tier up one before rollup (Hygiene→Info,
Info→Important, Important→Critical) for stricter CI gating.

## Remedy text

`internal/security/remedy.go` curates 10 RemedyEntry records, each
with Why / Fix / SeeAlso sections in markdown. Rendered via
charmbracelet/glamour for terminal output (colored headings, fenced
code blocks, ordered lists). `--no-color` falls back to ASCII style.

Every check that emits a `RemedyRef` has a matching entry; tests
enforce this (`TestFindingRefsHaveRemedies`).

## Runtime-consent oracle (#16)

The menubar mode owns the proxy's `armed`/`disarmed` bit. Why this
bundle and not a new one:

- **Distinct TCC identity from `com.charon.cli`.** The proxy's
  DR-check on the consent socket pins to `com.charon.security`,
  so a compromised `charon` binary can't drive arm/disarm on its
  own behalf — the oracle is genuinely separate even though both
  ship in the same repo.
- **Hardened runtime + no Accessibility entitlement.** Synthetic
  events from `cliclick` / AppleScript can't drive the menubar
  UI; the audit's existing "Accessibility-on-terminal=Critical"
  check closes the loop on humans-only clicks.
- **Same `.app` bundle as the audit tool** so users have one
  thing to install/sign/grant TCC to. The audit and oracle share
  no code paths beyond `cmd/charon-security/main.go` dispatch.

Trust edge: unix-domain socket at `~/Library/Caches/charon/runtime.sock`
(0600). Proxy reads `LOCAL_PEEREPID`, evaluates the peer's codesign
DR against `com.charon.security`. Unsigned dev binaries auto-bypass
so `make dev` keeps working; signed prod requires the real bundle
on the other end.

UI surface (`cmd/charon-security/menubar.go`):
- ●/○ glyph + remaining TTL in the menubar title (e.g. `● 27m` /
  `○ off`). Adaptive poll cadence — 10 s baseline, 1 s in the
  final minute so the countdown ticks live.
- Dropdown: status line, Arm 30m / 1h / 8h, Disarm, Quit.
- Native notifications via `notify_darwin.go` (cgo wrapper around
  UserNotifications.framework). Bundled mode posts via
  `addNotificationRequest` so the user can configure Banner vs
  Alert style scoped to charon. Bare-binary dev runs fall back to
  `osascript`.

Layered on the same `cmd/charon-security/` machinery so the audit
and menubar share `notify_darwin.go` + bundle introspection.

## See also

- [`docs/threat-model.md`](../docs/threat-model.md) — why each check exists
- [`docs/security-audit-test-plan.md`](../docs/security-audit-test-plan.md) — manual verification steps
- [`workshop/issues/000010-security-audit-tool.md`](../workshop/issues/000010-security-audit-tool.md) — design + log
- [`workshop/issues/000011-apple-developer-id.md`](../workshop/issues/000011-apple-developer-id.md) — the blocker for Tahoe TCC + auto-revoke (M6)
- [`workshop/issues/000016-runtime-consent-and-stats.md`](../workshop/issues/000016-runtime-consent-and-stats.md) — runtime-consent gate + caller ID + stats; menubar shipped here
