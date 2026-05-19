# nous security — host hygiene audit + runtime-consent menubar

## What

`nous security` is a two-purpose surface inside the unified `nous`
binary, exposed as a subcommand cluster (`nous#22`):

1. **Hygiene auditor** (`nous security check`) — verifies that the
   environment charon's threat model assumes actually holds: SIP
   enabled, no TCC grants on terminals/IDEs, charon's keychain ACL
   boundary intact, no suspicious launchd persistence. Prints
   actionable remediation when it doesn't (`nous security remedy
   <finding-id>`).
2. **Runtime-consent oracle** (`nous security menubar`) — macOS
   menubar agent that arms/disarms the proxy's `Session` gate. Talks
   to the proxy at `~/Library/Caches/charon/runtime.sock`.

Distinct from charon proper: charon is the credential proxy; the
`security` cluster audits the surrounding environment and acts as its
consent oracle. The implementation lives in `cmd/nous/security.go`
(check + remedy) and `cmd/nous/security_menubar.go` (menubar). Audit
machinery is in `lib/security/`; notification dispatch is in
`lib/notify/`.

(Pre-nous#22 history: these lived as a standalone `cmd/nous-security`
binary packaged as `Charon Security.app`. The packaging story is
recounted in *Why an `.app` bundle (deferred)* below; the merge into
`nous` happened to simplify dev iteration.)

## Architecture

```
nous security check       → audit subcommand (hygiene scan)
nous security remedy <id> → look up remediation for one finding class
nous security remedy      → print full playbook (10 entries)
nous security menubar     → runtime-consent oracle (#16)
```

Each subcommand is reachable from any signed-or-unsigned `nous` binary.
The menubar today works from a bare `nous security menubar` invocation
in a terminal; the prod packaging story (signed `.app` wrapper for
proper TCC attribution + LSUIElement dock-less + native
`UserNotifications.framework` source) is a deferred follow-up
(rescoped nous#19).

## Notification dispatch (lib/notify)

The menubar surfaces arm/disarm events via notifications. Backend is
selected at runtime by `lib/codesign.IsSigned()` + bundle detection:

| signed (`make nous-install`) | inside `.app` bundle | backend |
|---|---|---|
| yes | yes | `UserNotifications.framework` (cgo) |
| yes | no  | `terminal-notifier` (UNUSR no-ops without a bundle id) |
| no  | (any) | `terminal-notifier` when on `$PATH`, else `osascript` |

`terminal-notifier` is installed by `make nous-bootstrap` (Brewfile);
`osascript` is the universal last-resort fallback (always present on
macOS, attributes notifications to "Script Editor").

## Why an `.app` bundle (deferred)

TCC keys permissions on the **responsible code**. A bare Mach-O CLI
run from Terminal gets attributed to `com.apple.Terminal` in TCC.
Granting Full Disk Access to "nous security" in that scenario
actually grants it to Terminal — and revoking would nuke Terminal's
FDA. An `.app` bundle with its own bundle ID makes TCC see the auditor
as a distinct actor.

Today: `nous security check` works without a bundle; if it needs FDA
and the operator isn't in a bundle, the offer-FDA-grant flow prints a
"running outside a .app bundle" hint and skips the system-settings
prompt rather than attaching FDA to the terminal.

Tomorrow (deferred follow-up, rescoped nous#19): generate a signed
`Nous Security.app` whose `MacOS/` binary execs into `nous security
menubar`. Bundle ID grants distinct TCC identity, LSUIElement=true
keeps the process dock-less, `UserNotifications.framework` works for
native banners.

## Check layers

Three layers, evaluated in order:

1. **Privilege-free** (`lib/security/check_*.go`):
   - `csrutil status` — SIP enabled?
   - `sudo -nv` — sudo cache active in this shell?
   - `~/Library/LaunchAgents` enumeration — third-party persistence?
   - `codesign -d --entitlements -` per detected app — hardened-runtime
     weakening entitlements (A5 in threat-model)?
   - Filesystem + `mdfind` — which terminals/editors/IDEs are installed?
2. **TCC** (`lib/security/check_tcc.go`):
   - User + system TCC.db read via `/usr/bin/sqlite3`. Joins rows
     against detected apps. FDA / Accessibility on terminal = Critical;
     Screen Recording = Important; AppleEvents to Keychain Access /
     1Password / Bitwarden / etc. = Critical, others = Important.
   - Requires FDA on the bundle (when running inside one).
3. **Charon-specific** (`lib/security/check_charon.go`):
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

`--strict` promotes every tier up one before rollup (Hygiene → Info,
Info → Important, Important → Critical) for stricter CI gating.

## Remedy text

`lib/security/remedy.go` curates 10 RemedyEntry records, each with
Why / Fix / SeeAlso sections in markdown. Rendered via
charmbracelet/glamour for terminal output (colored headings, fenced
code blocks, ordered lists). `--no-color` falls back to ASCII style.

Every check that emits a `RemedyRef` has a matching entry; tests
enforce this (`TestFindingRefsHaveRemedies`).

## Runtime-consent oracle (#16)

The menubar mode owns the proxy's `armed`/`disarmed` bit. Pre-merge
design ran the oracle in a separate `com.charon.security`-bundled
binary so the proxy's DR check would see it as distinct from
`com.charon.cli`. Post-merge, both surfaces share the unified `nous`
binary (one codesign identity). When the deferred `.app` packaging
lands, the bundle is the new TCC anchor; until then, oracle and
audit run under the same identity that everything else in nous uses.

Hardened runtime + no Accessibility entitlement: synthetic events from
`cliclick` / AppleScript can't drive the menubar UI; the audit's
existing "Accessibility-on-terminal = Critical" check closes the loop
on humans-only clicks.

Trust edge: unix-domain socket at
`~/Library/Caches/charon/runtime.sock` (0600). Proxy reads
`LOCAL_PEEREPID`, evaluates the peer's codesign DR. Unsigned dev
binaries auto-bypass so `make dev` keeps working; signed prod
requires the real DR on the other end.

UI surface (`cmd/nous/security_menubar.go`):
- ●/○ glyph + remaining TTL in the menubar title (e.g. `● 27m` /
  `○ off`). Adaptive poll cadence — 10s baseline, 1s in the final
  minute so the countdown ticks live.
- Dropdown: status line, Arm 30m / 1h / 8h, Disarm, Quit.
- Notifications via `lib/notify.Send(...)` — backend selected per
  the table above.

## See also

- [`docs/threat-model.md`](../docs/threat-model.md) — why each check exists
- [`docs/security-audit-test-plan.md`](../docs/security-audit-test-plan.md) — manual verification steps
- [`workshop/issues/000010-security-audit-tool.md`](../workshop/issues/000010-security-audit-tool.md) — design + log of the original tool
- [`workshop/issues/000016-runtime-consent-and-stats.md`](../workshop/issues/000016-runtime-consent-and-stats.md) — runtime-consent gate + caller ID + stats; menubar shipped here
- [`workshop/issues/000022-merge-nous-security-into-nous.md`](../workshop/issues/000022-merge-nous-security-into-nous.md) — the merge into the unified `nous` binary; lib/codesign + lib/notify extraction
