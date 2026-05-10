# nous/lib/ — domain-organized libraries

Post-`nous#14` (M1–M3), the substantive library code lives in `lib/`, organized by domain rather than by source-of-origin. Per the lib-first design principle (see `nous#14`'s captured-decisions list, item #9): `cmd/*` are thin wrappers over `lib/*` operations, so future repackaging (e.g. a charon-only side-binary if the credential proxy ever ships outside the nous ecosystem) doesn't require disentangling commands from library logic.

## Layout

```
lib/
  agent/       — gpg-agent lifecycle: keygrip discovery (M3d). M4a wires
                 `flush` via gpg-connect-agent reloadagent. Prewarm
                 (PRESET_PASSPHRASE from Keychain) + status (KEYINFO
                 parse) remain stubbed pending the lib-side primitives.
                 Leaf module — lib/brain* and lib/identity import it;
                 doesn't import them.
  brain/       — brain manifest reader (M4a): Manifest struct, Read,
                 DiscoverAll, hand-rolled YAML-frontmatter parser. M4b
                 will add the *write* surface: New, RecipientAdd/Remove,
                 gcrypt push wiring. Distinct from lib/brainsync (the
                 sync daemon) — that may eventually fold under
                 lib/brain/sync if the layering settles.
  brainsync/   — brain-sync runtime: file-level conflict resolution,
                 ref-watcher, git ops layer. Today's cmd/brain-sync
                 consumer; future M5 may collapse into the unified
                 nous binary's service runtime.
  charoncli/   — cobra subcommand constructors for the legacy `charon`
                 binary surface (serve, run, auth, manifest, status,
                 service, vault, scopes, gcp, instructions, arm, disarm,
                 who, stats). Both cmd/charon (legacy entry) and
                 cmd/nous (unified entry) import these — single source
                 of truth for the provider-facing CLI behavior.
  gmail/       — Gmail tool primitives (cmd/gmail consumer).
  identity/    — GPG keypair operations (M4a): List (own secret keys),
                 ListPublic (peers), Export (armor), Inspect (dry-run
                 import for verify ceremony), Import (commit), Last8
                 (canonical short fingerprint form). Shells out to gpg;
                 no OpenPGP library dependency. Sits above lib/agent —
                 humans think in fingerprints, agent thinks in keygrips.
  provider/    — AI provider domain. Single roof for everything
                 credential-and-proxy:
                   provider/oauth/     OAuth flows
                   provider/providers/ per-provider impls (anthropic,
                                       gcp, openai) + catalog (scopes)
                   provider/proxy/     the credential proxy daemon
                   provider/runtime/   provider runtime (TTL, state)
                   provider/vault/     credential storage (keychain +
                                       memory)
  security/    — host-security audit machinery. Sibling to provider/,
                 not nested, since security audits are orthogonal to
                 credential routing. Consumer: cmd/nous-security/.
  service/     — launchd plist generation + service control. Today
                 the charon-side service manager; brain-sync has its
                 own at lib/brainsync/service_darwin.go (kept separate
                 for now since merging would cascade through legacy
                 cmd/brain-sync). cmd/nous's `nous service` cluster
                 dispatches to both.
  tui/         — bubbletea + lipgloss components. Used by cmd/charon
                 today via charoncli; future TUIs (`nous brain`,
                 `nous provider`) will use it directly.
  workspace/   — Root() resolver: $WORKSPACE_ROOT → $NOUS_DIR's parent
                 → binary's grandparent (when shaped <root>/<repo>/bin/
                 <exe>) → $HOME/workspace as final fallback. lib/brain
                 and lib/brainsync both consume; centralized so brain
                 discovery doesn't hardcode $HOME/workspace.
```

## Cmd consumers

- `cmd/nous/` — unified binary (M3b+). Cobra root with cluster subcommands. Mounts `charoncli.{InstructionsCmd, ManifestCmd, AuthCmd}` at top-level / `nous provider` paths. `cmd/nous/service.go` (M3c) dispatches `nous service install/start/stop/status` to both `lib/service` and `lib/brainsync`. `cmd/nous/identity.go` (M4a) wires the identity cluster over `lib/identity` + `lib/brain` (joined `list` view). Brain cluster remains a placeholder pending M4b.
- `cmd/charon/` — legacy entry, ~15-line `main.go` shim that calls `charoncli.BuildRoot().Execute()`. Stays for backwards-compat; eventual cleanup after operator migration.
- `cmd/brain-sync/` — legacy entry for the brain-sync watcher. Same posture: kept for backwards-compat.
- `cmd/nous-security/` — macOS menubar app. Separate cmd (different Info.plist + signing). Imports `lib/security`.
- `cmd/gmail/`, `cmd/oneshot/` — Gmail tool entry points. Import `lib/gmail`.

## Cross-import rule

`lib/provider` does not import `lib/brainsync` (or future `lib/brain`). `lib/brainsync` does not import `lib/provider`. They are independent domains. Common ground modules are:

- `lib/agent/` — gpg-agent ops (M3d shipped foundation; M4 adds verbs)
- `lib/tui/` — bubbletea + lipgloss components
- `lib/service/` — launchd plist + service control
- `lib/charoncli/` — cobra constructors for the provider-cluster surface

This separation is what allows future repackaging: a charon-only binary imports `lib/provider` + `lib/agent` + `lib/charoncli` + `lib/tui` + `lib/service`, not `lib/brainsync`.

## What's planned but not yet here

- `lib/brain/` provisioning + recipient writers — M4b. The reader path (`Read`, `DiscoverAll`, manifest parser) shipped in M4a; the *write* path (`New`, `RecipientAdd/Remove`, gcrypt push wiring) lands in M4b.
- `lib/agent/` verbs (prewarm, status) — M4a wired `flush` (it's a one-line gpg-connect-agent shell-out); prewarm and status need the keychain-passphrase flow and KEYINFO parsing.

## History

`nous#14`'s milestones, each with its commit anchor:

- M1 (commit `ff7e1f2`) — flat-copied charon's substantive code into `internal/charon/{oauth, providers, proxy, runtime, security, service, tui, vault}/` and `cmd/charon/`. 70 imports rewritten.
- M2 (commit `07f4b6f`) — disassembled `internal/charon/` into the domain-organized `lib/` tree. 71 imports rewritten. `internal/` directory removed entirely.
- M3a (`05211d1`) — refactored `cmd/charon` cobra constructors into `lib/charoncli/` (importable). `cmd/charon/main.go` slim shim.
- M3b (`fb47554`) — `cmd/nous/main.go` cobra root + cluster subcommands. Two TUIs (provider, brain placeholder); identity + service clusters scaffolded.
- M3c (`18fdd1e`) — real `nous service install/start/stop/status` unifying brain-sync + charon launchd plists.
- M3d (`5242e51`) — `lib/agent/` foundation (Identity, Keygrip, DiscoverIdentity).
- M4a — `lib/identity/` (List, ListPublic, Export, Inspect, Import, Last8) and `lib/brain/` (Manifest, Read, DiscoverAll, parseManifest). `nous identity {init,export,import,list,agent}` cluster wired in `cmd/nous/identity.go`. Init shells out to `scripts/identity.sh` (200 lines of gpg-agent + pinentry-mac config not worth re-porting yet); import is TTY-only with the verify-fingerprint ceremony (last-8 confirmation, 3 attempts, case-insensitive).

charon's pre-merge git history is preserved in the `xianxu/charon` GitHub repo (planned to be archived after `nous#14` ships).
