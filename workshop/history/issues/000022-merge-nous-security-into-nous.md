---
id: 000022
status: done
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
- [x] M2: Add `lib/notify` with three backends. Unit tests where
      possible (mostly: assert correct backend chosen for given
      `codesign.IsSigned()` mock). End-to-end: a manual test of
      `nous security menubar` posting a notification in each mode.
- [x] M3+M4: Add `nous security {check, remedy, menubar}` subcommands;
      delete `cmd/nous-security/` entirely; update Makefile.nous and
      the most-prominent atlas pointers. Combined into one commit
      since the M3-without-M4 intermediate state has the new code AND
      the old binary side-by-side, which isn't a useful checkpoint
      (the M2 retrofit already made cmd/nous-security depend on
      lib/notify, so it was already structurally redundant).
- [x] M5: Atlas + README + workshop/lessons.md updates. Punt or
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

### 2026-05-18 — M5 landed

Deep atlas rewrite + #19 rescope.

- `atlas/charon/security-audit.md` rewritten in place. The doc now
  uses `nous security {check, remedy, menubar}` throughout; the old
  `nous-security` framing is folded into a one-paragraph history
  note. Replaced the per-binary "Why a `.app` bundle" section with
  "Why a `.app` bundle (deferred)" that explains both why it
  matters and why it isn't blocking. New section: "Notification
  dispatch (lib/notify)" with the backend-selection table.
- `atlas/charon/charon.md` — consent-oracle paragraph updated to
  point at `nous security menubar` and `lib/notify`'s fallback
  behavior rather than the deleted `Charon Security.app`.
- `atlas/nous/dev-vs-runtime-mode.md` — partial-exception bullet
  redirected from `nous-security` to `nous security` + `lib/notify`;
  runtime-mode-packaging bullet, See-also list, and notify file
  pointer all updated.
- `workshop/issues/000019-nous-security-app-packaging.md` — added a
  Revisions section dated 2026-05-18 explaining how the merge
  changes what this issue is about (the bundle now wraps a small
  exec-shim into `nous security menubar`, not a separate binary).
  Open Questions about Developer ID, identifier naming, and
  notarization toolchain remain unchanged. Status stays `open`,
  priority lowered (dev path is unblocked via terminal-notifier).

Skipped: workshop/lessons.md update. The work executed cleanly with
no surprise corrections — the milestone split was useful as a plan
but didn't surface lessons that future me would need.

Issue status flipped to `done`.

### 2026-05-18 — M3+M4 landed

`cmd/nous-security/` is gone. `nous security` cluster now lives in
`cmd/nous/`:

- `cmd/nous/security.go` (new, ~250 lines) — `newSecurityCmd()`
  registers the cluster + check / remedy subcommands. Flags scoped
  to a `securityFlags` struct closure (avoids polluting cmd/nous's
  package namespace). `runSecurityCheck` and `runSecurityRemedy`
  port `cmd/nous-security/main.go`'s body verbatim, parameterized on
  the flags struct instead of package globals.
- `cmd/nous/security_menubar.go` (git mv from cmd/nous-security/
  menubar.go) — the menubar agent. Cobra constructor renamed
  `menubarCmd` → `newSecurityMenubarCmd` for clarity. Docstrings
  updated to reflect "runs as `nous security menubar`" framing,
  including the deferred `.app` packaging note.
- `cmd/nous/security_menubar_test.go` (git mv) — existing menubar
  tests, no change.
- `cmd/nous/main.go` — added `root.AddCommand(newSecurityCmd())` next
  to identity / brain / provider.

Makefile.nous: dropped docstring references to `nous-security` as a
separate binary; updated layout block and post-install note to point
at `nous security` subcommands and the rescoped nous#19. The build
loop is unchanged (it iterates cmd/*/, no nous-security-specific
plumbing was there).

Atlas:
- `atlas/nous/lib-layout.md` — `cmd/nous-security/` entry deleted;
  consumer of `lib/security/` now named as `cmd/nous/security.go`;
  historical note appended.
- `atlas/charon/index.md` — added `lib/notify/` + `lib/codesign/`
  to the layout list; folded nous-security retirement into the
  cmd/nous bullet.
- `atlas/charon/security-audit.md` — added a top-of-file Note redirecting
  `nous-security <verb>` → `nous security <verb>`. Deep edits deferred
  to M5.

`go build`, `go vet`, `go test ./cmd/nous/ ./lib/notify/ ./lib/codesign/
./lib/security/...` all green. `nous security --help` and `nous
security check --help` render correctly; smoke-tested `bin/nous
security` from the new symlink-into-bin/ layout.

Skipped: a deeper rewrite of `atlas/charon/security-audit.md`,
`atlas/charon/charon.md`, and `atlas/nous/dev-vs-runtime-mode.md`.
Those still describe nous-security as a separate binary; the top
note in security-audit.md tells readers to translate. Tracked in M5.

### 2026-05-18 — M2 landed
Added `lib/notify` with the decision-tree dispatcher (signed+bundled
→ UserNotifications.framework; else terminal-notifier when on PATH;
else osascript). Public API is `notify.Send(Notification)` +
`notify.RequestAuth()`; `notify.SetBackend` swaps the dispatch for
tests. Backend selection is lazy (first Send picks; subsequent Sends
reuse).

Files:
- `lib/notify/notify.go` — public API, mutex-guarded cached dispatch.
- `lib/notify/backend_darwin.go` — pickBackend + terminal-notifier
  and osascript backends. Test-swappable `hasBundle` and
  `terminalNotifierPath` vars.
- `lib/notify/userns_darwin.go` — UserNotifications.framework cgo
  body (moved from `cmd/nous-security/notify_darwin.go`; renamed
  C symbols charon_ → nous_; subtitle support added).
- `lib/notify/backend_other.go` — non-darwin no-op stub.
- `lib/notify/funcptr.go` — small reflect helper used by tests to
  compare which backend pickBackend returned without polluting test
  code with imports.
- `lib/notify/notify_test.go` + `lib/notify/backend_darwin_test.go`
  — 8 tests total: SetBackend dispatch, error propagation, nil-clear-
  resets-cache, four pickBackend decision-tree cases, escapeAppleScript
  table-driven.

Retrofit:
- `cmd/nous-security/menubar.go` — local `notify()` function replaced
  by `notifyBanner()` wrapper that goroutines `notify.Send(...)` (the
  goroutine matches the prior osascript-async behavior; matters for
  the slow shell-out backends).
- `cmd/nous-security/notify_darwin.go` + `notify_other.go` — deleted;
  their logic now lives in lib/notify.

Brewfile: added `terminal-notifier`.

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
