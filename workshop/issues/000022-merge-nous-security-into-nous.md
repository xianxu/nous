---
id: 000022
status: open
deps: [000020]
created: 2026-05-18
updated: 2026-05-18
estimate_hours: 4
---

# Merge `nous-security` into `nous`; abstract notifications behind signed-vs-unsigned

## Problem

Today nous ships as two binaries:

1. `nous` — the unified CLI + daemon (post nous#20). One binary, one
   signing decision (`make nous-install` signs it; `make build` doesn't).
2. `nous-security` — a separate binary with two modes:
   - `check`: ~4600 lines of `lib/security/*` audit code (SIP, TCC,
     sudo, TimeMachine, etc.) — pure CLI, no signing requirement.
   - `menubar`: a `fyne.io/systray` agent that arms/disarms via the
     proxy's Unix socket; benefits from `.app` bundle packaging
     (LSUIElement=true, TCC attribution, proper notification source).

The split exists for one structural reason: `UserNotifications.framework`
requires a bundle ID, so the menubar wants to be packaged as `.app`. The
audit code was bundled with the menubar because they were always built
together as nous-security.

Two costs of this split:

- **Mental model**: operator has to remember "security audit" is a
  different binary than the rest of nous. `nous security <subcmd>`
  fits the cluster pattern already established for `nous identity`,
  `nous brain`, `nous provider`, `nous service`.
- **Signing surface**: the audit half doesn't need signing at all. By
  hosting it inside the unsigned `nous` binary, dev iteration on
  hygiene checks stops requiring any signing dance.

Separately: the existing notification code at `cmd/nous-security/
notify_darwin.go` falls back to `osascript` when there's no bundle.
osascript notifications attribute to "Script Editor" — fine for
self-dev but visibly off-brand. `terminal-notifier` (Homebrew cask,
pre-signed by Homebrew) gives better attribution and supports actions
(reply, snooze, click → open URL), which the menubar's arm/disarm UX
will eventually want.

This issue does two coupled things that share a refactor: extract
the codesign primitive and the notification dispatch into reusable
libs, then move the nous-security CLI into nous as a subcommand.

Relationship to nous#19: that issue called for a signed + notarized
`Charon Security.app` as the prod packaging surface. After this
merge, the `.app` bundle question reduces to "wrap `nous security
menubar` in an Info.plist for prod install" — a smaller, deferrable
concern. nous#19 will be punted / rescoped depending on how the
deferred-bundle question lands.

## Done when

- `cmd/nous-security/` is deleted. Build system no longer references
  it (Makefile.nous build/sign/install loops, atlas, README).
- `nous security check` runs the audit (same flags, same output as
  today's `nous-security check`).
- `nous security menubar` launches the systray agent and arms/disarms
  the proxy via the existing Unix socket.
- `make build` produces only `bin/nous` and the plugin binaries
  (`bin/gmail`, `bin/oneshot`). One signing target, not two.
- Notifications routed through `lib/notify`:
  - Signed prod binary → `UserNotifications.framework` (cgo).
  - Unsigned dev binary → shells out to `terminal-notifier` if
    available, falls back to `osascript` if not.
  - Selection done at runtime via `lib/codesign.IsSigned()`; no env
    var, no flag.
- `terminal-notifier` added to Brewfile so `make nous-bootstrap`
  installs it on every dev/operator machine.
- Atlas updated to reflect single-binary model (the bits of
  `atlas/nous/` that still mention nous-security as a separate
  binary).

## Out of scope (carry forward)

- Generating a signed + notarized `.app` bundle wrapping `nous
  security menubar` for prod install. Deferred to a follow-up
  (nous#19 rescoped). Without the `.app`:
  - `nous security menubar` from a terminal works; systray icon
    shows up, arm/disarm works. TCC dialogs attribute to whichever
    terminal launched it (acceptable in dev; rough in prod).
  - Notifications from the menubar process come via terminal-notifier
    (since the binary itself is unsigned even after `make nous-install`
    — keychain ACL signing != bundle signing).
  - The proper `.app` packaging unblocks first-class menubar UX
    (LSUIElement dock-less, proper TCC name, native notification
    source) but isn't gating the merge.

## Spec

### `lib/codesign` (extracted primitive)

Move the existing `signatureCheck` from `lib/provider/vault/keychain/`
into its own package:

```go
// lib/codesign/codesign.go
package codesign

// Check is overridden by codesign_darwin.go's init when running on
// macOS+cgo. Tests override directly; do not call t.Parallel().
var Check = func() bool { return false }

// IsSigned reports whether the running binary is code-signed and
// satisfies its own designated requirement.
func IsSigned() bool { return Check() }
```

`lib/codesign/codesign_darwin.go` contains the existing
`selfSignatureValid` cgo body. `lib/provider/vault/keychain/service.go`
imports `lib/codesign` and replaces its local `signatureCheck` with
`codesign.IsSigned()`. Existing tests adapt to override
`codesign.Check` instead.

### `lib/notify` (new, reusable across nous + future surfaces)

```go
// lib/notify/notify.go
package notify

type Notification struct {
    Title    string
    Subtitle string
    Body     string
    // Actions []Action — deferred; terminal-notifier supports it,
    // UserNotifications.framework supports it; design once we have a
    // concrete caller that needs them.
}

func Send(n Notification) error { /* dispatches by codesign.IsSigned() */ }
```

Three backends, selected at runtime:

| `codesign.IsSigned()` | `terminal-notifier` available | Backend |
|---|---|---|
| true | (any) | `UserNotifications.framework` (cgo) |
| false | yes | `terminal-notifier` shell-out |
| false | no | `osascript` shell-out (last-resort fallback) |

Backends live in `notify_userns_darwin.go` (cgo, build-tagged), and
`notify_terminal.go` / `notify_osascript.go` (both pure-Go shell-outs,
both darwin-only at first; later may grow Linux variants under their
own build tags).

The existing cgo body in `cmd/nous-security/notify_darwin.go` moves
into `lib/notify/notify_userns_darwin.go` with no semantic change.

### `cmd/nous` subcommand surface

New file `cmd/nous/security.go` mirroring the existing identity/brain
cluster pattern:

```
nous security check     [--no-tcc] [--json] [--strict] [--yes]
nous security menubar
nous security           # TUI? for now, just prints help
```

Subcommand wiring imports `lib/security/*` directly — those packages
don't need to move.

### Delete `cmd/nous-security/`

After CLI verbs are mounted on nous and the notify code has moved
to `lib/notify`, delete:
- `cmd/nous-security/main.go`
- `cmd/nous-security/menubar.go`
- `cmd/nous-security/menubar_test.go`
- `cmd/nous-security/notify_darwin.go`
- `cmd/nous-security/notify_other.go`

`cmd/nous-security/bin/` is gitignored; the directory disappears
naturally on next clean build.

### Makefile.nous

- Drop `nous-security` from the `build` loop's natural inclusion
  (already auto-included via `cmd/*/`; after deletion of the dir,
  it falls out).
- Drop the `bin/nous-security` symlink path that the new
  per-binary symlink code creates (also auto-falls-out).
- Remove the docstring lines referencing nous-security in the
  install banner (lines ~67-71, 147).

### Brewfile

Add `brew "terminal-notifier"` so `make nous-bootstrap` installs it
unconditionally. Cheap on disk, removes the "is terminal-notifier
available" branch's else case for most operators (osascript fallback
becomes a safety net rather than a normal path).

## Plan

- [x] M1: Extract `lib/codesign` from `lib/provider/vault/keychain/`.
      Existing keychain tests still pass. No behavioral change.
- [ ] M2: Add `lib/notify` with three backends. Unit tests where
      possible (mostly: assert correct backend chosen for given
      `codesign.IsSigned()` mock). End-to-end: a manual test of
      `nous security menubar` posting a notification in each mode.
- [ ] M3: Add `nous security check` + `nous security menubar` to
      `cmd/nous/`. Both delegate to the existing `lib/security/*`
      and the menubar code (which migrates from cmd/nous-security
      to a new place — likely `lib/menubar` or under `cmd/nous/`
      directly). Existing menubar tests adapt.
- [ ] M4: Delete `cmd/nous-security/`. Update Makefile.nous and
      atlas references. Add `terminal-notifier` to Brewfile.
- [ ] M5: Atlas + README + workshop/lessons.md updates. Punt or
      rescope nous#19 with a Revisions section linking back here.

## Test plan

Manual, mostly — the value is in feature parity + the new dev-mode
notification path actually working:

- `nous security check` produces identical output to today's
  `nous-security check` (capture before/after, diff).
- `nous security check --json` parses (smoke).
- `nous security menubar` shows the systray icon, arm/disarm clicks
  hit the proxy.
- Run unsigned: notification fires via terminal-notifier (visible
  banner attributed to "terminal-notifier" — acceptable, off-brand).
- Run signed (after `make nous-install`): notification fires via
  UserNotifications.framework. **Caveat**: until the prod `.app`
  bundle exists, `nous security menubar` invoked from a signed
  binary still won't have a bundle ID, so UserNotifications.framework
  may no-op. In that intermediate state, signed binaries fall through
  to terminal-notifier too. Document this clearly; the gap closes
  with the deferred `.app` work.
- Uninstall terminal-notifier, re-test: osascript fallback fires.

## Notes

- The notification UX gap on signed-but-no-bundle is a known caveat,
  not a regression — today's nous-security has the same issue when
  invoked from a terminal (it already falls back to osascript in that
  state). What changes is which fallback we prefer.
- `lib/menubar` vs. `cmd/nous/menubar.go`: leaning toward keeping
  menubar code under `cmd/nous/` because it's a UI surface, not a
  reusable library. Will revisit during M3 if it grows enough to
  warrant a lib/ home.

## Log

### 2026-05-18 — M1 landed
Extracted `lib/codesign` from `lib/provider/vault/keychain/`. New
package surfaces `codesign.Check` (the swappable backend, default false)
and `codesign.IsSigned()` (the read API). The darwin+cgo init from
`keychain/codesign_darwin.go` moved verbatim to
`lib/codesign/codesign_darwin.go` (renamed C symbol prefix charon_ →
nous_; kept the existing `com.charon.cli` signed-identifier constant
since changing it is a separate scope). `keychain/service.go` now imports
`lib/codesign` and calls `IsSigned()`; the local `signatureCheck` var
and its file are deleted. Keychain tests adapted to override
`codesign.Check`. Added a minimal `TestIsSigned_RespectsCheckOverride`
regression test in the new package. Build, vet, and keychain tests
all green.
