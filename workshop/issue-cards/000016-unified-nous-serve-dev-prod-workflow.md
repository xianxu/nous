---
id: '000016'
status: done
created: 2026-05-09
updated: 2026-06-02
estimate_hours: 6
actual_hours: 2
---

# Unified `nous serve` foreground daemon + nous-dev/nous-install dev/prod workflow

## Problem

Today nous has multiple binaries (`nous`, `charon`, `brain-sync`, `nous-security`, `gmail`, `oneshot`) and the dev/prod workflow has rough edges. After `nous#14` M5 ships the menubar split and we're settled into multi-binary, the gap is:

1. **No single foreground entry point.** Running production via launchd is fine, but iterating on a code change requires either: stopping launchd, running each daemon (`./bin/charon serve` + `./bin/brain-sync` in two terminals), and remembering to put launchd back when done; OR doing `nous service install` repeatedly which is slow and pollutes the launchd state.

2. **`make nous-dev` doesn't actually run anything.** It builds binaries; the operator still has to start daemons manually. There's no "switch to dev mode" command that swaps prod off and dev on in one shot.

3. **No `make nous-install` either.** Production reinstall today is `make nous-dev && ./bin/nous service install` which works but is two steps with implicit ordering. And it doesn't sign the binaries (charon's `make sign` does, but nous-cmd doesn't reuse that path). The signed-vs-unsigned distinction matters: `lib/provider/vault/keychain/service.go::ResolveServiceName` routes signed binaries to the `charon` keychain namespace, unsigned to `charon-dev`. Today an "installed" launchd service runs unsigned binaries and stores prod-credentials in the dev namespace — the namespace split exists in code but the build pipeline doesn't enforce it.

The fix is to land a unified foreground story (`nous serve` running both daemons in one process) plus a clean dev/prod split in the Makefile.
