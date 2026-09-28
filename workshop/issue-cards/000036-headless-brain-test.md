---
id: '000036'
status: done
created: 2026-06-01
updated: 2026-06-02
estimate_hours: 4
actual_hours: 5
---

# scriptable headless brain testing in tart VM

## Problem

Testing brain operations (e.g. admitting yingtest42 to brain-family, then
having that throwaway identity clone/edit/push) in a tart VM currently only
works via `make tart-gui`: a headless VM has no window server, so
`pinentry-mac` can't draw its passphrase dialog and every GPG/gcrypt op
(decrypt, re-key, sign) fails. `tart-gui` works but the Screen Sharing dance
is cumbersome.

We want the headless `make tart` (SSH) VM to be a first-class brain test
environment, fully scriptable from the host — no GUI, no passphrase typing.

Two blockers:
1. The `make tart` VM is never made GPG-ready (`tart-vm-setup.sh` does
   oh-my-zsh + symlinks only).
2. The recipient/identity ceremony commands are TTY-gated
   (`term.IsTerminal(os.Stdin)` in `cmd/nous/brain_recipient.go`,
   `cmd/nous/identity.go`) — they refuse to run non-interactively, which is
   exactly the scripted case.
