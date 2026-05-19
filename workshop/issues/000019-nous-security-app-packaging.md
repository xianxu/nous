---
id: 000019
status: open
deps: []
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

## Done when

- `make nous-security-app` (or equivalent target) produces a
  signed + notarized `Charon Security.app` (or `Nous Security.app`)
  bundle at `bin/Charon Security.app/`.
- Bundle deploys via copy to `/Applications/` (manually for now;
  future: integrate into `make nous-install` or homebrew bottle).
- First launch surfaces the Notifications authorization prompt;
  subsequent menubar actions deliver real banner notifications
  rather than osascript dialogs.
- macOS Gatekeeper doesn't quarantine the app on first launch
  (notarization stapling verified).

## Open questions

1. **Identity recovery.** The `xianxu/charon` repo had a `make sign`
   workflow with Developer ID details that didn't carry across the
   absorb. Need to either: (a) recover from the archived charon
   repo + Apple Developer account; (b) generate a fresh
   Developer ID (account-level setup); or (c) defer until wider
   deployment makes this worth the setup cost.
2. **Bundle id stability.** Existing keychain ACL entries are
   bound to `com.charon.security` from the charon era. Keep it,
   or rename to `com.xianxu.nous.security` and re-grant?
3. **Notarization toolchain.** `xcrun notarytool` (new) vs `altool`
   (deprecated). Need the Apple ID + app-specific password OR
   notary API key configured in a way that survives `make
   nous-security-app` runs without exposing secrets in the
   Makefile.
4. **Bundle structure source.** Author the Info.plist + folder
   layout by hand, or use a Go-side `gomobile`/`mac-app` style
   generator? The bundle is simple (one CLI, one Info.plist, one
   icon); by hand is probably fine.

## Plan — sketch (depends on Open Questions 1 + 3)

### M1 — bundle layout

- [ ] `cmd/nous-security/app/` directory holds `Info.plist`
      (template), `entitlements.plist`, icon.icns placeholder.
- [ ] `scripts/build-nous-security-app.sh` constructs the bundle:
      copies the compiled binary into
      `bin/Charon Security.app/Contents/MacOS/`, renders
      Info.plist with version + bundle id, copies icon.
- [ ] `make nous-security-app` invokes the script.

### M2 — code-sign + notarize

- [ ] Sign the bundle with the recovered (or fresh) Developer ID
      Application identity. Use `--options runtime` +
      `--entitlements cmd/nous-security/app/entitlements.plist`.
- [ ] Submit to notarization via `xcrun notarytool submit ... --wait`.
- [ ] Staple via `xcrun stapler staple bin/Charon\ Security.app`.

### M3 — install + first-run

- [ ] Add `make nous-security-install` that copies the stapled
      bundle to `/Applications/Charon Security.app` (or wherever
      operators expect).
- [ ] Document first-run flow: open the app, accept the
      Notifications prompt, confirm menubar icon appears.

## Notes

- **Dev-mode fallback unchanged.** When operator runs
  `cmd/nous-security/bin/nous-security` as a bare binary (e.g.
  during iteration), the existing bundle-detect → osascript path
  stays in place. Notifications work in a less-rich way; security
  audits still run.
- **Defer until needed.** Operator can use nous#16's daemon install
  today without nous-security.app — notifications are nice-to-have,
  not blocking. Pick this up when the menubar surface becomes
  daily-driver-critical (e.g., when arming/disarming the proxy
  needs richer feedback than a CLI banner).

## Log

### 2026-05-10 — daemon-signing scope moved to nous#16

`make nous-install` (nous#16 M5 follow-up) now signs nous + charon +
brain-sync with `scripts/sign.sh`, defaulting to ad-hoc — that closes
the agent-as-threat bypass (raw OAuth tokens were exfiltrable via
`security find-generic-password` against the ACL-less charon-dev
namespace). The threat-model rationale lives in
`atlas/nous/dev-vs-runtime-mode.md`.

What remains here in nous#19: the `.app` bundle packaging for the
menubar + Notifications.framework surface. That's a separate
concern with its own toolchain (Info.plist, entitlements, xcrun
notarytool, stapling, bundle layout) and a separate identifier
(probably `com.42shots.security`, distinct from `com.charon.cli`
which is the daemon-binary identifier).

The daemon ACL boundary works ad-hoc; the nous-security .app needs
a real Developer ID for notarization (Gatekeeper refuses ad-hoc on
recent macOS for distributed apps). So nous#19 still needs the
Developer ID recovery from archived xianxu/charon or a fresh setup
— that part of the open-questions section is unchanged.

### 2026-05-10 — created
Surfaced from operator feedback on nous#16 M4: "for now I can just
do the dev mode only. though we do need signing on mac so that we
can send notification etc." Splitting the daemon-install (initially
no signing; now signed via ad-hoc per the M5 follow-up) from the
menubar-packaging (signing + notarization required) lets each ship
at its own pace.

## Revisions

### 2026-05-18 — rescoped after nous#22 merge

nous#22 folded `cmd/nous-security/` into the unified `nous` binary
as the `nous security {check, remedy, menubar}` subcommand cluster.
That changes what this issue is actually about:

**Before:** build + sign + notarize a standalone `Charon Security.app`
that wraps the `nous-security` binary.

**After:** build + sign + notarize a small `.app` wrapper whose
`Contents/MacOS/` executable invokes `nous security menubar` (either
by exec'ing `/usr/local/bin/nous`, or by including a thin Go shim
binary that does `os.Args[0] = "nous"; syscall.Exec(...)`). The
audit half (`nous security check`) doesn't need a bundle — it runs
as a normal CLI, and `lib/notify` falls through to terminal-notifier /
osascript for any banners it produces. The bundle is purely for the
menubar's TCC identity + LSUIElement dock-less behavior + native
`UserNotifications.framework` source attribution.

Downstream effects on this issue's plan:

- Plan M1 (bundle layout): the bundle no longer holds a separate
  audit binary. `Contents/MacOS/<entry>` is either a symlink to
  `/usr/local/bin/nous` + `nous security menubar` argv (won't work;
  LaunchServices expects a real binary), or a 20-line Go shim that
  exec()s into the operator's installed `nous` with the right args.
  The shim binary is what carries the bundle's signature.
- Plan M2 (signing): unchanged in principle — Developer ID, hardened
  runtime, notarization, stapling. Still gated on Developer ID
  recovery (Open Question 1 above).
- Plan M3 (install + first-run): unchanged.
- The "dev-mode fallback unchanged" note in the existing Notes
  section is still accurate, but the mechanism is different — it
  used to be inline bundle-vs-bare detection in the nous-security
  binary; it's now `lib/notify` doing the same dispatch from inside
  `nous`.

What hasn't changed:
- The bundle identifier question (Open Question 2) — keep
  `com.charon.security` for keychain ACL continuity, or rename to
  `com.42shots.nous.security` / `com.42shots.security` to match the
  unified naming? Still an open call.
- Developer ID recovery (Open Question 1).
- Notarization toolchain (Open Question 3).

Status remains `open`. Lower priority than before, because the
day-to-day dev path is unblocked: `nous security menubar` works as
a foreground process from any terminal, with terminal-notifier
delivering banners. The bundle is now strictly a polish item for
the wife-onboarding case (notifications attributed to "nous
security" rather than "terminal-notifier").

See `nous#22` for the merge; see `cmd/nous/security_menubar.go` for
the current menubar implementation that the bundle would wrap.
