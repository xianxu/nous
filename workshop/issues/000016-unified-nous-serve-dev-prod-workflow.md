---
id: 000016
status: open
deps: [000014]
created: 2026-05-09
updated: 2026-05-09
estimate_hours: 6
---

# Unified `nous serve` foreground daemon + nous-dev/nous-install dev/prod workflow

## Problem

Today nous has multiple binaries (`nous`, `charon`, `brain-sync`, `nous-security`, `gmail`, `oneshot`) and the dev/prod workflow has rough edges. After `nous#14` M5 ships the menubar split and we're settled into multi-binary, the gap is:

1. **No single foreground entry point.** Running production via launchd is fine, but iterating on a code change requires either: stopping launchd, running each daemon (`./bin/charon serve` + `./bin/brain-sync` in two terminals), and remembering to put launchd back when done; OR doing `nous service install` repeatedly which is slow and pollutes the launchd state.

2. **`make nous-dev` doesn't actually run anything.** It builds binaries; the operator still has to start daemons manually. There's no "switch to dev mode" command that swaps prod off and dev on in one shot.

3. **No `make nous-install` either.** Production reinstall today is `make nous-dev && ./bin/nous service install` which works but is two steps with implicit ordering. And it doesn't sign the binaries (charon's `make sign` does, but nous-cmd doesn't reuse that path). The signed-vs-unsigned distinction matters: `lib/provider/vault/keychain/service.go::ResolveServiceName` routes signed binaries to the `charon` keychain namespace, unsigned to `charon-dev`. Today an "installed" launchd service runs unsigned binaries and stores prod-credentials in the dev namespace — the namespace split exists in code but the build pipeline doesn't enforce it.

The fix is to land a unified foreground story (`nous serve` running both daemons in one process) plus a clean dev/prod split in the Makefile.

## Spec

### `nous serve` — single-process foreground daemon

Single subcommand that runs both runtimes (brain-sync watcher + charon proxy) as goroutines in one process:

```
./bin/nous serve              # runs both, blocks until SIGTERM
./bin/nous serve --proxy-only # only charon proxy
./bin/nous serve --sync-only  # only brain-sync
```

Wires `lib/brainsync.Run()` and `lib/provider/proxy.Serve()` (extracted from cmd/charon's serve subcommand) as goroutines with shared signal handling. One log stream, one PID, one launchd plist.

### `make nous-dev`

Stop production launchd services + build unsigned binaries + run `./bin/nous serve` in foreground. One command takes you from "I want to test this change" to "running with the new code." Ctrl-C exits cleanly; on exit the operator can `make nous-install` to put production back.

### `make nous-install`

Stop dev foreground process (if running) + build + sign + install + start launchd service. One command takes you from "I want this change live" to "running in prod from launchd."

Code-signing reuses charon's existing `make sign` workflow (already in `charon/Makefile.local`). The signed binary uses the `charon` keychain namespace; the launchd service is the only way to drive it normally. dev keeps using `charon-dev`.

### Namespacing implication

Once this lands, the prod/dev split is:

- **Production**: signed binaries, launchd-managed, use `charon` keychain namespace. Stable. Survives reboot.
- **Development**: unsigned binaries, foreground-run via `make nous-dev`, use `charon-dev` keychain namespace. Disposable. Re-builds on each iteration.

Operators can freely switch between them via `make nous-dev` ↔ `make nous-install` without any state corruption.

## Done when

- `bin/nous serve` runs both brain-sync and charon proxy as goroutines in one process; SIGTERM cleanly shuts both down.
- `make nous-dev` stops launchd services, builds, and runs `./bin/nous serve` in foreground.
- `make nous-install` stops any dev foreground process, builds, signs, installs to a stable location (likely `~/.local/bin/nous` to avoid PATH issues), updates launchd plist to point at the signed binary, starts the service.
- `lib/brainsync` and `lib/provider/proxy` expose `Run(ctx)` / `Serve(ctx)` functions usable from a single process; existing per-binary entry points stay as legacy shims (cmd/charon, cmd/brain-sync) until they can be removed.
- The launchd plist `nous service install` writes points at the unified `nous serve` (one service, not two), so `nous service status` shows one daemon's state.
- charon-side codesigning workflow reused: `nous-install` calls into charon's `make sign` (or extracts that logic) so signed nous binaries hit the `charon` keychain namespace.

## Plan — sketch

### M1 — extract daemon runtimes into lib

- [ ] `lib/brainsync.Run(ctx)` exposes the brain-sync watcher loop as a callable function (today inline in `cmd/brain-sync/main.go`).
- [ ] `lib/provider/proxy.Serve(ctx, addr, vault, ca, audit)` exposes the proxy daemon (today inline in `lib/charoncli`'s ServeCmd).

### M2 — `nous serve` subcommand

- [ ] `cmd/nous/serve.go`: subcommand with `--proxy-only` / `--sync-only` flags. Default runs both. Single signal handler shuts down both via context cancellation.
- [ ] One log stream (stderr by default; can split via flags later).

### M3 — `make nous-dev` runs foreground

- [ ] Stop launchd services (existing `nous-dev` already does).
- [ ] After build, `exec ./bin/nous serve` (block until Ctrl-C).
- [ ] Print "running in dev mode (charon-dev namespace)" status banner so the operator sees the namespace split clearly.

### M4 — `make nous-install` signs + installs

- [ ] Pre-step: stop dev foreground process. Detection: `pgrep -f 'bin/nous serve'`.
- [ ] Build to `bin/`.
- [ ] Sign each binary that ships into prod (nous, charon, brain-sync, nous-security). Reuse charon's `make sign` workflow — likely refactor `Makefile.local`'s sign target into a callable script `scripts/sign.sh <binary>` that nous-install can call per-binary.
- [ ] Copy signed binaries to `~/.local/bin/` (PATH-stable location).
- [ ] Update launchd plist to point at `~/.local/bin/nous serve`.
- [ ] Install + start the unified service.

### M5 — drop legacy multi-service install

- [ ] `nous service install` writes one plist (com.xianxu.nous), not two (com.charon.proxy + com.xianxu.brain-sync).
- [ ] Migration: detect old plists at install time, unload + delete them, install the new unified plist.
- [ ] `nous service status` reflects one service.

## Estimate

~6 hr P50 (range 4-10 hr). Mostly composition work — runtime extraction is mechanical (the loops already exist), nous-install is gluing existing pieces (charon's sign workflow + cmd/nous's existing service plumbing). Largest unknown is signal/lifecycle behavior across the two goroutines (brain-sync's fsnotify watcher + charon's HTTPS listener share a process for the first time).

## Notes

- **Why not bundle this into nous#14**: nous#14 was scoped to "absorb charon, unified CLI structure." This is "unified daemon process + dev/prod orchestration" — a related but distinct concern. Splitting keeps each issue's scope tractable and the calibration data clean (nous#14's actuals shouldn't drag this work's hours).
- **`nous-security` stays separate**: macOS menubar app needs different Info.plist + signing + lifetime model. Always its own binary.
- **Why `~/.local/bin` and not `/usr/local/bin`**: charon's existing `make install` already uses `~/.local/bin`. Match that. No sudo needed; survives Homebrew rebuilds; PATH-addable.
- **What's preserved from today**: `bin/charon` and `bin/brain-sync` legacy entries can stay as deprecation shims even after this lands. They invoke `cmd/charon/main.go::charoncli.BuildRoot()` (charon) or block forever calling `lib/brainsync.Run()` (brain-sync). Safe to remove once operator migrates.

## Log

### 2026-05-09 — created
Surfaced from operator UX feedback after nous#15 closed: "let's do `make nous-dev` to do `nous-all` in dev mode + stop production first. `make nous-install` should stop dev + install prod. For now since we have multiple binaries, just rename nous-all to nous-dev and file a ticket for the eventual single-binary unified workflow."

Today's commit (`2480ff4` → renamed to nous-dev with the stop-production prelude): partial today's-work scope. This issue tracks the full unified-binary + nous-install + signing pipeline.
