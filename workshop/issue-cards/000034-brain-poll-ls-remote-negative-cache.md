---
id: '000034'
status: done
created: 2026-05-26
updated: 2026-05-26
estimate_hours: 3
actual_hours: 1
---

# `nous serve` brain-poll: cheap negative cache via `git ls-remote`

## Problem

`nous serve` background-polls each shared brain repo with `git fetch` on a
tight interval. For encrypted brains (git-remote-gcrypt remotes), every
fetch spawns `gpg --status-fd 3 -q -d` to decrypt the manifest **even
when nothing has changed**, because the gcrypt transport can't ask the
server "did anything move?" — it has to download the encrypted manifest
and decrypt it to find out.

At steady state across N brain repos, this dominates CPU:

- During a recent incident on the operator's mac, `nous serve` had been
  running 2+ days. `gpg-agent` sustained 90%+ CPU and load average hit
  16.6 on a 12-core machine. Trace showed `git fetch` → `git-remote-gcrypt` →
  `gpg --status-fd 3 -q -d` firing every 1–2 seconds across multiple
  brain repos (`brain-family`, `brain-shared-test`, ...). Nothing was
  changing on any of them.
- `nous service uninstall` resolved the symptom by removing the launchd
  job; load recovered within minutes.

The polling architecture itself is fine — we want git history and have
ruled out syncthing event-driven sync. The bug is that polling
unconditionally invokes the decrypt path.
