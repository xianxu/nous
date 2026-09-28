---
id: 000013
status: wontfix
deps: [000004]
created: 2026-05-08
updated: 2026-05-08
estimate_hours: 10
---

# `brain` — unified CLI + TUI for managing brains

## Problem

Brain operations are scattered across multiple shell scripts and a single-purpose Go binary:

- `make new-brain` (bash, single-recipient, hand-edit `.brain/config.md` to admit a peer)
- `make cloneto`, `make moveto` (bash)
- `brain-sync` (Go, watches shared brains, runs as launchd service)
- No tool for `recipient add/remove/list`, no safeguards, no public-key registry view, no fingerprint-verification ceremony

Symptoms surfaced trying to provision `brain-shared-family` for `nous#12 M1`:
- The user couldn't recall the multi-recipient invocation (because there isn't one)
- The "admit a peer" flow is six manual steps including hand-editing `.brain/config.md` and `gcrypt-participants` config
- No safeguard against removing the operator's own fingerprint, dropping the last recipient, or accepting a typo'd pubkey
- Documentation drift between SKILL/atlas/threat-model on the recipient layout vs what the scripts actually produce

The user's directive: "no one's going to remember what command to use. The ergonomics need to be there." Match the **charon pattern** — a single `brain` Go binary with cobra subcommands and a bubbletea TUI shell, replacing the bash + makefile scatter.

## Spec

### Final shape

One Go binary at `cmd/brain/`, replacing `cmd/brain-sync/` and absorbing `scripts/new-brain.sh`, `scripts/cloneto.sh`, `scripts/moveto.sh`:

```
brain                              # foreground TUI: status of all known brains, menu of actions
brain new <path>                   # provision (guided: private/shared, recipients, GitHub repo)
brain recipient list <brain>
brain recipient add <brain>        # guided: import pubkey, verify fingerprint, admit, force-push
brain recipient remove <brain>     # guided: confirm fingerprint, warn revocation, remove, force-push
brain key list                     # named recipients across all brains (fp + brains-admitted-to)
brain sync                         # foreground watcher (current bare brain-sync)
brain sync install/start/stop/status [brain]
brain doctor                       # diagnostics: gpg setup, key access, remotes, fingerprints
```

Underlying primitives stay the same (gpg, git, gcrypt). The wrapper provides ergonomics + safeguards.

### Drop `mode:` from `.brain/config.md` schema

`mode:` was load-bearing in exactly one place: `lib/brainsync/discovery.go` filters auto-discovery to `mode: shared`. Replace with explicit registration: `brain sync install <path>` adds the brain to a daemon-tracked list; the daemon watches what it's told to watch, no filesystem scanning by mode. Sets up the orthogonal-axes posture (recipient-count and sync-yes-or-no are independent), naturally supports single-recipient-multi-machine.

Migration: existing brains' `.brain/config.md` get `mode:` removed; `brain sync install` registers them. Backward-compat with old-format manifests not maintained (small blast radius — only my brain-private + the soon-to-exist brain-shared-family).

### Safeguards (baked into recipient subcommands)

1. **Self-removal guard**: `brain recipient remove` errors if the target fingerprint matches any of the operator's own GPG secret keys. Override with `--force` only; `--force` requires a `--reason` flag whose value goes into the commit message.
2. **Last-recipient guard**: refuses to remove the last recipient. No `--force` override (you'd be locking yourself out of your own brain).
3. **Verify-before-add prompt**: after import, print full fingerprint + UID + key creation date; require operator types out the last 8 hex chars of the fingerprint to confirm. Defends against pubkey typos in sneakernet.
4. **Revocation warning**: on remove, print: "removing X means future commits won't be readable to them, but past commits remain encrypted to their key. If X is compromised, rotate keys + assume past content leaked." Then prompt for confirmation.

### TUI shape (bubbletea)

When `brain` is invoked with no subcommand:

```
┌─ brain ───────────────────────────────────────────────────────────┐
│                                                                   │
│  Known brains:                                                    │
│    ● brain          (private, 1 recipient, sync: not installed)   │
│    ● brain-family   (shared, 2 recipients, sync: running)         │
│                                                                   │
│  Recipients (across all brains):                                  │
│    xianxu (you)     0ECF6AC0...3872C2F0   → brain, brain-family   │
│    ying             A1B2C3D4...           → brain-family           │
│                                                                   │
│  Actions:                                                         │
│    [n] new brain                                                  │
│    [r] recipient management                                       │
│    [s] sync service control                                       │
│    [d] doctor                                                     │
│    [q] quit                                                       │
│                                                                   │
└───────────────────────────────────────────────────────────────────┘
```

Hitting `r` opens a recipient submenu; `n` starts the guided provisioning flow; etc. Each subcommand can also be invoked from the shell directly (cobra root commands), so power users skip the TUI.

Same pattern as `charon` — TUI for "I forgot the syntax", direct subcommand for "I know what I want".

### Compatibility

- `make` targets stay as thin wrappers for backward compat: `make brain-new` calls `brain new`, `make brain-recipient` calls `brain recipient`. Eventually the make targets can be removed once muscle memory shifts.
- Existing `brain-sync` launchd plist gets renamed at install (the binary is now `brain`); existing service installs are gracefully migrated by `brain sync install --migrate`.

## Done when

- A user (operator's wife, on her Mac, after `make nous-bootstrap`) can:
  1. Run `brain new ../brain-private-ying` interactively and end up with a working private brain (no manual `.brain/config.md` edit).
  2. Run `brain recipient add` against `brain-shared-family` with a pubkey from her husband, complete the verify-fingerprint dance, and have the recipient admitted.
  3. Run `brain` (no args) and see a TUI showing both brains with their state.
- All four safeguards fire as described and have unit tests against them.
- `mode:` is removed from `.brain/config.md` schema; brain-sync auto-discovery is replaced with explicit registration; existing brains migrated.
- `brain-sync` binary is gone; `brain sync ...` subcommands cover the previous capabilities.
- Documentation: atlas entry at `nous/atlas/nous/brain-cli.md` describing the unified surface; SKILL.md for `nous-tools` updated to point at `brain` for daily workflows.

## Estimate

Range: **8–14 hr**. Best guess: **~10 hr**.

| Milestone | Primitive | Total |
|---|---|---|
| M1 — rename binary brain-sync → brain; subcommand restructuring | Cross-cutting refactor | 0.7–1.5 |
| M2 — `brain new` (port new-brain.sh + guided + multi-recipient) | Greenfield Go (medium) | 2–3 |
| M3 — `brain recipient list/add/remove` + safeguards | Greenfield Go (small) + tests | 1.5–2.5 |
| M4 — drop `mode:` schema; switch discovery to registered list | Cross-cutting refactor | 0.7–1.5 |
| M5 — TUI shell (status board + recipient submenu + new submenu) | bubbletea (familiar from charon) | 3–5 |
| **+30% design buffer** | | +0.3–1 |
| **Total** | | **~8.2–14.5** |

Familiarity ×1 (no buffer): cobra + bubbletea + gpg/git pipelines are all known from charon; the work is composition not invention. Largest unknown is the TUI layout iteration cost.

## Plan

### M1 — rename brain-sync → brain

- [ ] Rename `cmd/brain-sync/` → `cmd/brain/`. Update package name, imports across `lib/brainsync/`.
- [ ] Move existing `serve` / `service install/uninstall/start/stop/status` under a `sync` subcommand: `brain sync` runs the foreground watcher; `brain sync install/...` for service control.
- [ ] Update launchd plist template: binary name + label change. Add `brain sync install --migrate` to handle in-place upgrade from `brain-sync` plist.
- [ ] Update Makefile.nous: `make build` produces `brain` not `brain-sync`. `make nous-test-brain-sync` → `make nous-test-brain` (or keep alias).
- [ ] Update atlas + sync-substrate-decision atlas to reference `brain sync ...`.
- [ ] Verify: `brain --help` shows the subcommand tree; `brain sync` foreground works; `brain sync install/start/stop/status` works against existing brain-private; the local two-process integration test (`scripts/test-brain-sync.sh`) still passes after rename.

### M2 — `brain new` (provision)

- [ ] New cobra subcommand `brain new <path>`. Shells out to git, gh, gpg as `new-brain.sh` does today.
- [ ] Guided prompts (interactive when stdin is TTY; flag-based otherwise): target path → GitHub owner/repo → recipient list (multi-select from local GPG keyring with fingerprints, with named entries pulled from any prior brain's `keys/recipients/<name>.asc`).
- [ ] Multi-recipient: `gcrypt-participants` is space-separated fingerprint list; `.brain/config.md` `recipients:` array carries them all.
- [ ] Drop `mode:` from the generated manifest (M4 follow-on, but shipping M2 with the new schema avoids needing a second migration).
- [ ] Write recipient pubkeys into `<brain>/keys/recipients/<name>.asc` (per nous#3 M2's recipient-layout convention).
- [ ] Tests: provision a brain into `$TMPDIR`, assert manifest + remote config + keys/ contents.
- [ ] Delete `scripts/new-brain.sh`; `make brain-new` calls `brain new` directly.

### M3 — `brain recipient`

- [ ] `brain recipient list <brain>`: print recipients with names, fingerprints, brains-they're-also-in.
- [ ] `brain recipient add <brain>`: prompt for `<pubkey-file>` (or fingerprint if already in keyring); print full fingerprint + UID + creation date; require operator to type the last 8 hex chars of the fingerprint to confirm; `gpg --import` if needed; `gpg --sign-key`; update `.brain/config.md` recipients; update `gcrypt-participants` config; copy to `keys/recipients/<name>.asc`; commit + force-push.
- [ ] `brain recipient remove <brain>`: prompt for fingerprint or name; check self-removal guard (refuse unless `--force --reason=...`); check last-recipient guard (refuse always); print revocation warning; require y/n confirm; update manifest + gcrypt config + delete keys/recipients/<name>.asc; commit + force-push.
- [ ] Tests: synthetic brain with two recipients; exercise add (with mocked pubkey import), remove with each safeguard triggered, then with safeguard intentionally bypassed.

### M4 — drop `mode:` from schema; switch discovery to registered list

- [ ] Remove `mode:` write from `brain new` (M2 already prepared for this).
- [ ] Replace `lib/brainsync/discovery.go`'s mode-filter with reading a registered-list file (e.g. `~/.config/brain/brains.list` or `~/Library/Application Support/brain/brains.list` on macOS — one path per line). `brain sync install <brain>` adds to the list; `brain sync uninstall <brain>` removes.
- [ ] Migration path: on first run after upgrade, scan existing brains for `mode: shared`, auto-register them, and emit a deprecation note.
- [ ] Update `brain` TUI to show "registered for sync: yes/no" instead of "mode: private/shared".
- [ ] Update atlas + threat-model docs.

### M5 — TUI shell

- [ ] `brain` (no subcommand) launches bubbletea-based TUI per the spec sketch above. Status board: known brains (from registered list + workspace scan), recipients (joined from keys/recipients/ across brains), actions menu.
- [ ] Submenus: recipient management (list / add / remove with the same safeguards as the CLI subcommands), new brain (the guided prompt sequence rendered as a multi-step form), sync service control (per-brain install/start/stop/status with live state), doctor (cards showing gpg setup, key access, remote reachability per brain).
- [ ] Each TUI action is a thin wrapper over the cobra subcommand — same logic, different rendering.
- [ ] Test by running it interactively against my brain-private + (the soon-to-be) brain-family. Manual test, no automation here — TUI testing is its own rabbit hole.

## Notes

- **Why rename brain-sync → brain even though brain-sync was just shipped (nous#4 M2)**: the unified-CLI direction wasn't a clean part of #4's scope (which was strictly the daemon). Once we started designing the recipient management surface, putting it next to `brain-sync` would have created a confusing "brain-sync but also it does these other things" name. Cleaner to rename early — only artifact paying the cost is the launchd plist label, which `brain sync install --migrate` handles in-place.
- **Why bubbletea**: charon already uses it. Familiar to me as the operator. Charm libs are stable. No need to evaluate alternatives.
- **Why not split TUI from CLI subcommands** (some projects do): the cobra subcommands are the primitives; the TUI is rendering. Same logic, different presentation. Splitting would force duplication.
- **Out of scope explicitly**: key generation (stays `make identity` until that's also unified — different rabbit hole), multi-machine identity sync (orthogonal), operator's own pubkey export ceremony (`gpg --armor --export` is fine for now).

## Log

### 2026-05-08 — created
Surfaced from `nous#12 M1` (provision brain-shared-family) ergonomics review. User pushed for the unified `brain` cmd + TUI direction over piecemeal bash improvements. Path B chosen: build the tooling first, then provision brain-family with the new commands. This blocks `nous#12 M1`.

### 2026-05-08 — wontfix, superseded by `nous#14`
Same conversation evolved further: the architectural unity isn't just "a brain CLI" but "all nous-substrate tools (brain + charon) under one binary." Filed `nous#14 — absorb charon, unified nous CLI` which covers everything in this issue plus the charon merge plus the cross-cutting observability/identity surface. Closing this as wontfix to preserve the design history; the work itself is being done in #14.
