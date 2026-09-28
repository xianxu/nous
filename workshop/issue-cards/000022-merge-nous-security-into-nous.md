---
id: '000022'
status: done
created: 2026-05-18
updated: 2026-05-18
estimate_hours: 4
actual_hours: N/A
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
