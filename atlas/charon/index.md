# Charon — credential proxy + provider auth

The charon-origin code lives in nous's substrate. Original charon GitHub repo (`xianxu/charon`) is being archived (per `nous#14`); the substantive code moved into `nous` via M1 (flat copy) and M2 (disassembled into domain libs).

## Entries

- [Charon overview](charon.md) — what charon is, the proxy + provider-auth surface
- [Security audit](security-audit.md) — threat model + audit log conventions

## Where the code lives now (post-M2)

`internal/charon/` no longer exists. Disassembled into `lib/` per the lib-first design principle (`nous#14 M2`, commit `07f4b6f`):

- `lib/provider/{oauth, providers, proxy, runtime, vault}/` — credential proxy + provider auth + per-provider impls
- `lib/security/` — host-security audit machinery (powers `cmd/nous-security/`)
- `lib/tui/` — bubbletea + lipgloss components
- `lib/service/` — launchd plist generation + service control
- `cmd/nous/` — the substrate binary; mounts `lib/charoncli`'s cobra constructors at top-level + under `nous provider` (run, arm, disarm, vault top-level; gcp/who/stats/scopes under provider). The unified daemon entry is `nous serve`. (`cmd/charon/` existed until nous#20 as a standalone shim over `charoncli.BuildRoot`; retired so all keychain reads use one codesign identity.)
- `cmd/nous-security/` — separate menubar binary; signing model differs (Info.plist + notarization), so it stays its own cmd

Full layout map: [`atlas/nous/lib-layout.md`](../nous/lib-layout.md).

## Cross-refs

- `nous/atlas/nous/brain-conflict-resolution.md` — sibling skill (`/nous-resolve`) that depends on the same gpg-agent + tui foundations charon helped pioneer
- `brain/atlas/threat-model-shared-brain.md` — the brain-side counterpart; `nous#14` adds a delegation-boundary subsection that pairs with charon's identity model
