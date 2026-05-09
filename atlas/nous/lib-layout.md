# nous/lib/ — domain-organized libraries

Post-`nous#14 M2`, the substantive library code lives in `lib/`, organized by domain rather than by source-of-origin. Per the lib-first design principle (see `nous#14`'s captured-decisions list, item #9): `cmd/*` are thin wrappers over `lib/*` operations, so future repackaging (e.g. a charon-only side-binary if the credential proxy ever ships outside the nous ecosystem) doesn't require disentangling commands from library logic.

## Layout

```
lib/
  brainsync/   — brain-sync runtime: file-level conflict resolution, ref-watcher,
                 git ops layer. Used by cmd/brain-sync (will collapse into the
                 unified nous binary's service runtime in M3).
  gmail/       — Gmail tool primitives (cmd/gmail consumer).
  provider/    — AI provider domain. Single roof for everything credential-and-
                 proxy:
                   provider/oauth/     OAuth flows
                   provider/providers/ per-provider impls (anthropic, gcp, openai)
                                       + provider/providers/catalog (scope catalog)
                   provider/proxy/     the credential proxy daemon
                   provider/runtime/   provider runtime (TTL, state)
                   provider/vault/     credential storage (keychain + memory)
  security/    — host-security audit machinery. Sibling to provider/, not nested,
                 since security audits are orthogonal to credential routing.
                 Consumer: cmd/nous-security/.
  service/    — launchd plist generation + service control. Will absorb
                 cmd/brain-sync's service_darwin.go in M3.
  tui/         — bubbletea + lipgloss components. Used by cmd/charon today;
                 will be used by all future TUIs (`nous brain`, `nous provider`).
```

## Cross-import rule

`lib/provider` does not import `lib/brainsync` (or future `lib/brain`). `lib/brainsync` does not import `lib/provider`. They are independent domains. Common ground is:

- `lib/tui/` — both will use it for bubbletea components
- `lib/service/` — both ship as launchd services
- `lib/agent/` (planned, not yet extracted) — gpg-agent ops, used by both

This separation is what allows future repackaging: a charon-only binary imports `lib/provider` + `lib/tui` + `lib/service` (+ eventually `lib/agent`), not `lib/brainsync`.

## What's planned but not yet here

- `lib/agent/` — gpg-agent ops (prewarm, flush, status). Net-new code; charon used gpg-agent indirectly via system tools, no charon-origin code to relocate. Lands as `nous#14 M3-M4` (charon#21 absorption).
- `lib/identity/` — keypair gen/export/import, keyring inspection. Net-new code; lands in `nous#14 M4`.
- `lib/brain/` — provisioning/recipient/resolve. Net-new code (M4); also will absorb `lib/brainsync/` as `lib/brain/sync/` if the rename feels right at that point.

## History

The current layout was assembled in two milestones of `nous#14`:

- M1 (commit `ff7e1f2`) — flat-copied charon's substantive code into `internal/charon/{oauth, providers, proxy, runtime, security, service, tui, vault}/` and `cmd/charon/`. 70 imports rewritten.
- M2 (commit `07f4b6f`) — disassembled `internal/charon/` into the domain-organized `lib/` tree. 71 imports rewritten. `internal/` directory removed entirely.

charon's pre-merge git history is preserved in the `xianxu/charon` GitHub repo (planned to be archived after `nous#14` ships).
