---
id: 000019
status: open
created: 2026-05-10
updated: 2026-05-10
estimate_hours: 4
---

# nous-security `.app` packaging — signed + notarized menubar

## Problem

`nous-security` is the macOS host-hygiene auditor + menubar surface
(`charon arm`/`disarm`-style UI, security audit notifications). To
deliver actual notifications via UserNotifications.framework, it
needs to run as a proper `.app` bundle with:

- A valid Info.plist (bundle id, executable name, NSPrincipalClass)
- Code-signed with a real identity (ad-hoc signing posts notifications
  on some macOS versions but is increasingly flaky on recent releases)
- Notarized + stapled (so Gatekeeper accepts it on first launch
  without right-click-then-Open ceremony)
- Bundled icon, optional menubar template image

Today the binary at `cmd/nous-security/main.go` already knows how to
detect bundle-vs-bare and falls back to osascript for notifications
when there's no bundle. That fallback is good enough for engineer
dev iteration; it's not good enough for the day-to-day surface
when nous#19 ships.

This is **separate from** nous#16's daemon install flow:
- nous#16 = unsigned daemon binaries (nous, charon, brain-sync), dev
  posture, charon-dev keychain namespace. Right for the engineer.
- nous#19 = signed + notarized nous-security.app. Right for everyone
  who uses macOS notifications (operator included).
