---
id: 000014
status: working
deps: [000004]
created: 2026-05-08
updated: 2026-05-09
estimate_hours: 16
---

# Absorb charon — unified `nous` CLI + TUI

## Problem

nous-substrate operations are split across two repos and multiple binaries:

- **`charon`** repo: AI-credential proxy (`charon serve`), provider/OAuth management (`charon auth`), instructions (`charon instructions`), manifest, plus a separate `charon-security` macOS menubar app, and for host security practice check.
- **`nous` repo**: workflow conventions, the `brain-sync` Go daemon, brain provisioning bash scripts (`new-brain.sh`, `cloneto.sh`, `moveto.sh`), the `/nous-resolve` skill. and we currently use bunch of make targets for manual operation, with some workflow missing. 

Both serve nous (the operator's AI-coding tool); both have daemon + TUI shapes + other command line interface; both manage credentials (charon: API tokens, OAuth; brain: GPG keys); both want gpg-agent lifecycle management (`charon#21` literally about gpg-agent; brain's gcrypt encrypt/decrypt leans on it); both use cobra + bubbletea + lipgloss; both ship as launchd services. There's real overlap, and parallel infrastructure means double-built TUI patterns + service plumbing + credential abstractions.

User's directive: merge them. charon's repo gets archived. The combined surface lives at `nous/cmd/nous/` (one binary), with subcommands that emerge from use cases rather than from translating the existing tools.

This supersedes `nous#13` (brain CLI unification, narrower-in-scope).

## Spec

### One binary, four use-case clusters + cross-cutting top-level commands

```
nous                          # cobra-default help: lists clusters + entry points

nous identity ...             # GPG keys + agent + peers (no TUI; interactive CLI for the (h) ops)
nous brain ...                # brains: provision, sync, recipients (TUI at `nous brain`)
nous provider ...             # AI providers: auth + config (TUI at `nous provider`)
nous service ...              # service control + observability (CLI only; no TUI)

nous instructions [topic]     # agent-only: canonical guide (progressive disclosure)
nous manifest [topic[:filter]] # agent-only: machine-readable state introspection
```

**Service is unified**: `nous service install/start/stop` controls *all* nous services together (brain-sync + provider proxy). No per-subsystem service subcommands like `nous brain sync install` — there's no value in starting brain-sync without the proxy or vice versa. The proxy daemon doesn't even have its own subcommand path; it's just one of the things `nous service start` brings up.

**Two TUIs only** (`nous brain` and `nous provider`) — domain-focused, not a forced merge across clusters. Identity = interactive CLI prompts where needed. Service = pure CLI. Bare `nous` = cobra help.

**Menubar is its own binary**: `cmd/charon-security/` (renamed `cmd/nous-menubar/` on the move) stays as a separate cmd. macOS app mode runs into the menubar-vs-CLI build seam (different Info.plist, lifetime, signing). Eventual one-binary-three-modes is phase-2 work, not this issue.

The clusters emerged from walking through actual use cases:

| Use case | Cluster |
|---|---|
| Generate keypair, export pubkey, import & verify peer key, gpg-agent lifecycle | `nous identity` |
| Provision brain (private/shared), recipient list/add/remove, resolve conflicts, brain status | `nous brain` |
| Configure AI provider, OAuth, paste API key | `nous provider` |
| Install/start/stop services (brain-sync + proxy together), status, doctor, audit | `nous service` |

`nous service` is the cross-cutting cluster that absorbs both observability (was: scattered across `charon-security` + `brain-sync status` + ad-hoc) and service-management (install/start/stop launchd plists). It controls all nous services together — there's no value in starting brain-sync without the proxy or vice versa. No per-subsystem service subcommands.

### Security posture: keeping agents out of identity-and-access ops

Sharp concern: an agent running on the device could in principle run `nous identity init` to mint a key and `nous brain recipient add` to admit itself to a shared brain. Two safeguards prevent the silent path:

1. **TTY-only on key-mint and recipient-admit operations.** `nous identity init`, `nous identity import`, and `nous brain recipient add` refuse to run if stdin is not a TTY. No `--passphrase` flag, no `--accept-fingerprint` flag, no batch-mode override — the human's terminal must be present. Agents driving the binary via subprocess pipes hit a refusal.

2. **Verify-fingerprint ceremony is unforgeable from agent context.** `nous identity import` and `nous brain recipient add` require the operator to type the last 8 hex chars of the imported key's fingerprint manually. The 8 chars must match what the script computed; otherwise the import aborts. An agent that generated a key locally has the fingerprint, sure — but it can't *type* into the human's terminal session in a way that bypasses the prompt without the human noticing.

This sits on top of the existing threat model in `brain/atlas/threat-model-shared-brain.md`, which already concedes "agent-as-threat for brain-private" — any agent on the device can read brain-private's plaintext (no per-agent ACL). What this section adds: the *delegation* boundary. An agent can read what's already accessible; an agent cannot silently *expand* what's accessible to itself or to a third party. That requires the human's TTY.

Operational consequence: `nous identity init` invoked from a non-interactive context (CI, headless SSH without `-t`, Claude Code subprocess) fails with `"identity init requires a TTY — run from your interactive terminal"`. Same for `recipient add` and `import`. The error tells the operator what to do.

The threat model gets a `## Revisions` entry pointing at this issue's commit when the safeguards land, so the doc and the code stay in sync.

### Subcommand tree (full)

Each command is tagged with intended audience: **(h)** human-primary (interactive prompts or TUI), **(a)** agent-primary (machine-readable, scriptable). When `(h)` is a full bubbletea TUI, it's marked `(h, TUI)`; otherwise `(h)` means interactive CLI prompts (no full screen, just sequenced questions).

```
# Identity — keys + agent + peers. Mostly run-once or shell-hook ops; no TUI.
nous identity init                     (h)   TTY-only. Guided keypair generation (interactive prompts).
nous identity export [--my-fp]         (h/a) Print armored pubkey for sneakernet (human pipes to clipboard)
                                             or for agents to retrieve into manifest output.
nous identity import <file>            (h)   TTY-only. Verify-fingerprint ceremony — operator types last
                                             8 hex chars of the imported key's fingerprint.
nous identity list                     (a)   Keys in keyring with brain-context (--json output).
                                             Pretty text output for humans who prefer the terminal.
nous identity agent prewarm            (a)   Pre-cache the GPG passphrase in gpg-agent so subsequent
                                             gcrypt push/pull and brain-sync ops don't prompt mid-flow.
                                             Used as a shell `precmd` hook or at session start. Reads
                                             passphrase from macOS Keychain, hands to gpg-agent.
nous identity agent flush              (a)   Clear gpg-agent's cached passphrase (`gpg-connect-agent
                                             reloadagent /bye` semantics). Used at session end as a
                                             security-hygiene step so the next op re-prompts.
nous identity agent status             (a)   Show whether gpg-agent has cached the passphrase, when
                                             the cache expires, and the agent socket health. Diagnostic.

# Brain — the most-touched cluster; bare `nous brain` opens TUI.
nous brain                             (h, TUI)  brain TUI: list, drill-in, actions
nous brain new <path>                  (h)  guided provision (interactive prompts)
nous brain list                        (a)  machine-readable list (--json)
nous brain recipient list <brain>      (a)
nous brain recipient add <brain>       (h)  verify-fingerprint ceremony + safeguards
nous brain recipient remove <brain>    (h)  confirmation + revocation warning
nous brain resolve <brain> [--undo]    (a)  mechanical conflict-find + preserve + commit;
                                            called by /nous-resolve skill for the mechanical bits
nous brain status [brain]              (a)  machine-readable health

# Provider — auth/config for AI providers. Bare `nous provider` opens TUI (à la today's `charon auth`).
# Add/remove/auth all happen inside the TUI; no separate CLI subcommands for those.
nous provider                          (h, TUI)  list providers; drill into add/remove/auth flows.
                                                 Shape mirrors `charon auth` today.
nous provider list                     (a)  machine-readable list (--json) — agent inspection.
                                            The only agent-facing CLI in this cluster — config and
                                            auth flows are human-only (OAuth dance opens a browser).

# Service — pure CLI ops; no TUI. One install/start/stop covers ALL services
# (brain-sync + provider proxy). No per-subsystem service control.
nous service install                   (a)  install all services as launchd agents (--migrate for old plists)
nous service uninstall                 (a)  remove all
nous service start                     (a)  start (or restart) all
nous service stop                      (a)  stop all
nous service status                    (a)  what's installed, what's running, summary (also `nous status` alias)
nous service doctor                    (a)  prescriptive checks across the whole stack with named fixes
nous service audit [--since]           (a)  unified audit log (proxy requests + brain ops + recipient changes)

# Top-level — agent-facing entry points. Progressive disclosure.
nous instructions [topic]              (a)  agent guide. Default = overall;
                                            `nous instructions brain` etc. narrows.
nous manifest [topic[:filter]]         (a)  machine-readable state. Default = summary;
                                            `nous manifest provider:permission` etc. narrows;
                                            `nous manifest all` for kitchen-sink.
```

**Two TUIs only**: `nous brain` and `nous provider`. Both are domain-focused (no forced merge across clusters). Identity ops are interactive CLI prompts (no full-screen TUI — humans rarely "browse" their keys). Service ops are pure CLI (mechanical, scriptable, runs from launchd hooks).

Bare `nous` prints the standard cobra help — clusters listed, no TUI launched.

### Design principle: agent-facing help vs. human-facing TUI

This is the structural insight that makes the TUI investment safe.

**Agent-facing surface = subcommand `--help` text + missing-arg explainers.** When Claude Code (or any coding agent) drives `nous`, it discovers capabilities by walking `nous --help`, `nous brain --help`, `nous brain recipient add --help`. It hits explainers when args are missing (warmup pattern, like `close-issue.py`). This is the agent's manual. It must be:
- Dense and accurate
- Self-sufficient (the agent should not need to read SKILL.md or atlas to drive a command)
- Source-of-truth for procedures (the verify-fingerprint ceremony, the safeguards, what files get touched)

**Human-facing surface = TUIs where humans actually browse-and-act + interactive CLI prompts elsewhere.** Two TUIs only — `nous brain` (browse + drill-in for brain ops) and `nous provider` (provider config + auth flows à la today's `charon auth`). Identity ops use sequenced CLI prompts where needed (no full-screen TUI; humans rarely browse keys). Service ops are pure CLI (mechanical, scriptable). Bare `nous` prints help. **None of this renders in any agent transcript** when the agent only sees `nous --help` — humans get the rich flows, agents get the dense manual, neither pollutes the other.

The two TUIs are domain-focused, not unified, because the clusters are genuinely different domains: provider auth has nothing in common with brain-recipient management beyond "both involve secrets." A unified board would force unrelated state into one screen. Two TUIs (and only where there's real browse-and-act work) keeps each focused. The other clusters get TUIs *only if* dogfood reveals real friction with the CLI prompt pattern.

Concrete implication: the cobra `--help` text for `nous brain recipient add` fully documents the procedure (verify ceremony, safeguards, files touched). The TUI version of the same flow is interactive ("type the last 8 hex chars to confirm: ___") — visually richer but functionally identical, calling the same underlying ops. Underlying functions live in `lib/`, both surfaces wrap them.

Side benefits:
- Some commands are CLI-only (`--json` output for scripting) — never appear in TUI
- Some flows are TUI-only (visual diff before confirming a merge) — don't need cobra subcommands
- The split keeps each surface focused and avoids "kitchen-sink help text" anti-pattern

This applies to skill-as-script (per `brain/data/life/42shots/ideas/2026-05-07-01-pensive-skill-as-script.md`): `nous`'s subcommand `--help` IS the skill-as-script for nous-using agents. SKILL.md files for nous-related skills (`nous-resolve`, `xx-issues` via `make close-issue`) defer to it.

### Design principle: progressive disclosure on instructions and manifest

Agent-facing surface should not greedily dump everything. An agent that needs to drive the proxy to talk to gmail shouldn't receive identity + brain + service material in its context. Both `instructions` and `manifest` follow the topic-narrowing pattern:

- **Default**: overall summary — what clusters exist, what entry points matter for orientation. Tells the agent *where to look next*.
- **Topic-narrowed**: `nous instructions <cluster>` returns the cluster-specific guide; `nous manifest <cluster>[:filter]` returns the cluster-specific state. Filters narrow further (`nous manifest provider:permission` for "what each provider permits" — narrower than full provider state).
- **`nous manifest all`**: the kitchen-sink dump for cases when an agent really does need everything (e.g., debugging, full audit). Explicit opt-in, not the default.

Cobra's per-subcommand `--help` already follows this principle — `nous brain --help` shows brain-specific content, not the universe. `instructions` and `manifest` extend the same shape. The cluster names match the cluster subcommand tree, so an agent that knows `nous brain ...` exists also knows `nous instructions brain` and `nous manifest brain` exist.

Concrete usage by an agent:
- `nous instructions` to orient: "I see brain, provider, identity, service clusters. The user's task involves the proxy."
- `nous instructions provider` for the proxy guide.
- `nous manifest provider` to discover configured providers.
- `nous manifest provider:permission` to discover what's allowed.
- Drives the operation. Never had to load brain or identity content.

### Design principle: lib-first code structure

Every operation in `cmd/nous/` is a thin wrapper over a function in `lib/`. The cobra subcommand parses args, dispatches to a lib function, formats output. The TUI does the same — different rendering, same lib calls. Same for the menubar binary if it ever absorbs functionality.

**Why**: charon may grow uses outside the nous ecosystem (other projects wanting just the credential proxy + provider auth without brain or workflow conventions). When that need materializes, ship a `cmd/charon/` repackage that imports only `lib/charon/`, `lib/provider/`, `lib/agent/` — no nous-specific lib code. The unified `nous` binary stays the primary product; the repackage is a side-product.

**Implications**:
- `lib/` modules are organized by domain (lib/brain, lib/provider, lib/identity, lib/agent, lib/service, lib/tui), not by command structure (no lib/cmd/whatever).
- Lib modules don't import each other unless there's a real domain dependency. `lib/provider` does not import `lib/brain`. `lib/agent` (gpg-agent ops) is a leaf, used by both `lib/brain` and `lib/identity`.
- No top-level imports between lib packages and `cmd/nous/` go-helpers. The cmd wrappers are thin.
- Tests sit in `lib/` for the operations; cmd-level tests just exercise arg parsing and output formatting.

This principle applies even when there's no current plan to repackage. Premature unification (cmd-level helpers that lib code calls back into) is the anti-pattern to avoid.

### Repo strategy

- `xianxu/charon` GitHub repo: **archive** with a pointer to `xianxu/nous` as the new home.
- charon's git history: subtree-merge into nous under `cmd/charon/` (or a temporary path), preserve via `git subtree add`.
- charon's open issues: keep existing charon repo's `workshop/issues/` for archive reference; create new `nous#NN` issues for any open work that's getting cut over (currently: `charon#21`).
- `cmd/charon-security/`: comes along; lives as separate cmd until the menubar absorption phase.
- charon's AGENTS.md, CLAUDE.md, atlas: reconcile with nous's. Most of charon's atlas content moves to `nous/atlas/charon/`.

### Mapping: today → tomorrow

| Today | Becomes |
|---|---|
| `make identity` | `nous identity init` |
| `gpg --armor --export <fp>` | `nous identity export` |
| `make new-brain` | `nous brain new` |
| `make cloneto`, `make moveto` | folded into `nous brain new --from-clone <path>` |
| `brain-sync` (foreground) | runs as part of `nous service start` (no separate foreground subcommand in v1) |
| `brain-sync service install` | `nous service install` (installs brain-sync + proxy together) |
| `charon` / `charon serve` (foreground proxy) | runs as part of `nous service start` |
| `charon auth` (TUI listing providers + auth flows) | `nous provider` (the same TUI, scoped to the provider cluster) |
| `charon auth <provider>` | drilled in via the `nous provider` TUI; no separate CLI subcommand |
| `charon instructions` | `nous instructions` |
| `charon manifest` | `nous manifest` |
| `charon-security` (menubar app) | `cmd/nous-menubar/` (separate cmd; one-binary-three-modes is phase-2) |
| `/nous-resolve <brain-root>` (CC skill) | Skill stays + calls `nous brain resolve --auto` for mechanical bits |

## Done when

- One Go binary at `cmd/nous/` exposing the full subcommand tree above.
- `xianxu/charon` repo archived with redirect.
- All `make` targets that wrap brain/charon ops become thin aliases to `nous ...` (or get removed).
- A user (operator's wife, on her Mac after `make nous-bootstrap`) can:
  1. Run `nous brain` and see her brains' state in a domain-focused TUI.
  2. Run `nous brain new ../brain-private-ying` interactively and end up with a working private brain.
  3. Run `nous brain recipient add ../brain-shared-family` against a sneakernet'd pubkey, complete the verify-fingerprint dance, and have her husband admitted.
  4. Run `nous service install && nous service start` to bring up brain-sync + proxy together; then `nous service doctor` to see green/yellow/red verdicts across GPG, agent, brains, providers, services with named fixes.
- All four recipient safeguards fire and have unit tests.
- `mode:` removed from `.brain/config.md` schema; brain-sync auto-discovery replaced with explicit registration.
- Atlas reorganized: `nous/atlas/nous/cli.md` (new) describes the unified surface; charon's existing atlas content moves under `nous/atlas/charon/` where still relevant; legacy entries marked archived.
- charon#21 (gpg-agent lifecycle) work lands inside this issue's M-cluster (M3 below) — not a separate effort.

## Estimate

Range: **12–22 hr**. Best guess: **~16 hr**.

| Milestone | Primitive | Total |
|---|---|---|
| M1 — subtree-merge charon → nous; both binaries build, tests pass | Cross-cutting refactor | 1.5–3 |
| M2 — extract `lib/tui/`, `lib/agent/`, `lib/service/` shared libs (no behavior change) | Refactor | 1.5–3 |
| M3 — introduce `cmd/nous/` cobra root; move existing subcommands under it; gpg-agent (charon#21 absorbed) | Greenfield Go (medium) + cobra restructuring | 3–5 |
| M4 — net-new commands: `nous identity` (TTY-only), `nous brain recipient` w/ safeguards + TTY, `nous brain new` guided, `nous service status/doctor/audit`, threat-model update | Greenfield Go (medium) | 3–5 |
| M5 — Per-cluster TUIs (brain, provider, identity, service); each focused on its domain | Bubbletea (familiar from charon) | 3–5 |
| **+30% design buffer** | | +0.5–1.5 |
| **Total** | | **~12.5–22.5** |

Familiarity ×1: cobra + bubbletea + gpg/git pipelines all known from charon; the work is composition. Largest unknowns are (a) the subtree-merge dance preserving sane git history, (b) TUI layout iteration, (c) reconciling charon's idiosyncrasies with nous conventions.

## Plan

### M1 — flat-copy charon → nous, rewrite imports, both binaries build

Initially planned as `git subtree add --squash`, but subtree lands the whole charon tree under one prefix and we'd want to mass-move pieces (`cmd/charon` → `cmd/charon`, `internal/` → `internal/charon`, etc.) anyway. The archived charon GitHub repo preserves charon's git history independently; we don't need it threaded into nous's log. Flat copy is simpler.

- [x] Copy `charon/cmd/charon/` → `nous/cmd/charon/` (the proxy + auth + instructions + manifest binary).
- [x] Copy `charon/cmd/charon-security/` → `nous/cmd/charon-security/` (rename to `cmd/nous-menubar/` deferred to a follow-on commit, not blocking M1).
- [x] Copy `charon/internal/` → `nous/internal/charon/` (oauth, providers, proxy, runtime, security, service, tui, vault as subpackages).
- [x] Copy `charon/atlas/{charon.md, security-audit.md}` → `nous/atlas/charon/`. Skipped charon's vendored `atlas/workflow/` (ariadne-base, nous has its own) and `atlas/index.md` (charon-internal).
- [x] Skipped: charon's `AGENTS.md`, `CLAUDE.md`, `Makefile.workflow`, `workshop/`, `.openshell/`, `construct/`, `Makefile`, `bin/`, `docs/`, `LICENSE`, `README.md`, `scripts/`, `test/` — all either vendored ariadne-base, or charon-internal that stays in the archived repo.
- [x] Merged `charon/go.mod` deps via `go mod tidy` (which auto-discovered the new imports). Pulled in: charmbracelet bubbles/bubbletea/lipgloss/glamour, keybase/go-keychain, fyne.io/systray, golang.org/x/{sync,term}, gopkg.in/yaml.v3.
- [x] Rewrote imports across copied files: `github.com/xianxu/charon/internal/...` → `github.com/xianxu/nous/internal/charon/...` via `sed`. 70 files affected; 0 charon imports remain after rewrite.
- [x] All tests pass: `go test ./...` green across all packages including new `internal/charon/{providers, proxy, runtime, security, tui, vault}`. `go build ./...` green.
- [x] AGENTS.md / CLAUDE.md unchanged — nous's stays canonical; charon's were vendored ariadne anyway.
- [x] Atlas: `atlas/charon/index.md` created with charon overview + security-audit links + reorg notes.
- [x] M1 source charon SHA: `d85363d` (charon's HEAD at copy time). Provenance: `cd ~/workspace/charon-archive && git log d85363d` (after the archive happens).

### M2 — extract domain-organized lib

Per the lib-first principle, organize `lib/` by domain so future repackaging (e.g. a charon-only binary) needs to grab a clean subset, not untangle commands.

- [x] `lib/tui/`: moved from `internal/charon/tui/`. Bubbletea + lipgloss components for the future `nous brain` and `nous provider` TUIs.
- [ ] `lib/agent/`: gpg-agent ops (prewarm, flush, status, passphrase fetch). **Deferred to M3-M4** — there's no charon-origin gpg-agent code to relocate (charon used gpg-agent indirectly via system tools); this is net-new code, lands as part of charon#21 absorption.
- [x] `lib/service/`: moved from `internal/charon/service/` (launchd plist gen + service control). M3 will merge brain-sync's `service_darwin.go` (currently in `lib/brainsync/`) into here.
- [x] `lib/security/`: moved from `internal/charon/security/` (host-security audit machinery, used by `cmd/nous-security/`). Kept as its own lib (sibling to provider/brain/etc.) since security audits are orthogonal to the credential/brain clusters.
- [ ] `lib/brain/`: not extracted in M2 — `lib/brainsync/` (existing) stays in place. M3 or follow-on can rename to `lib/brain/sync/`. Provisioning + recipient + resolve are net-new code (M4 scope).
- [x] `lib/provider/`: moved from `internal/charon/{oauth, providers, proxy, runtime, vault}/` → `lib/provider/{oauth, providers, proxy, runtime, vault}/`. The whole credential-and-proxy domain landed under one roof.
- [ ] `lib/identity/`: not extracted in M2 — net-new code (M4 scope). Charon doesn't have an identity-management surface to move.
- [x] **Cross-import rule verified**: `grep -rln 'github.com/xianxu/nous/lib/brain' lib/provider/` → 0 matches. `grep -rln 'github.com/xianxu/nous/lib/provider' lib/brainsync/` → 0 matches. Clean separation.
- [x] No CLI changes — refactor only. `go build ./...` green; `go test ./...` green across all packages including all relocated provider sub-packages, security, tui, service.
- [x] `internal/` directory removed (was only holding charon-imports during M1).

### M3 — `nous` cobra root + subcommand restructuring

Shipped across four sub-commits (M3a-M3d):

- [x] **M3a** (`05211d1`): refactored `cmd/charon` cobra constructors into `lib/charoncli/` package. Both `cmd/charon` (legacy entry, slim shim) and (future) `cmd/nous` import the same constructors. 14 subcommand constructors capitalized; package-level state (listenAddr, defaultListenAddr, auditPath, verbose) renamed/exported as needed. `cmd/charon` binary still builds + functions identically.
- [x] **M3b** (`fb47554`): `cmd/nous/main.go` (~140 lines) with cobra root + four cluster subcommands. `nous instructions` and `nous manifest` mount `charoncli.InstructionsCmd`/`ManifestCmd` directly. `nous provider` mounts `charoncli.AuthCmd` with overridden `Use="provider"` (bare cluster command IS the TUI entry). `nous provider list` mounts `charoncli.ManifestCmd` with `Use="list"`. `nous identity` and `nous brain` are M4 placeholders with helpful "see legacy X for now" errors.
- [x] **M3c** (`18fdd1e`): real `nous service install/uninstall/start/stop/status` in `cmd/nous/service.go` (~230 lines). Each subcommand dispatches to BOTH `lib/service` (charon's launchd manager) and `lib/brainsync` (brain-sync's), aggregating output. Sibling-binary discovery resolves `bin/charon` and `bin/brain-sync` paths next to nous.
- [x] **M3d** (`5242e51`): `lib/agent/` foundation — keygrip discovery via `gpg --with-keygrip --with-colons --list-keys` parsing. `Identity` type bundles fingerprint + UID + all keygrips (primary + subkeys). Live-tested against operator's actual keyring. Charon#21 M1 absorbed; M2-M3 (prewarm/flush/status verbs) land in M4.

**Deferred / out of M3 scope:**
- ~~Move brain-sync foreground watcher loop into a lib runner~~: today brain-sync stays in `cmd/brain-sync/` and runs as its own launchd service (controlled by `nous service`). Single-binary daemon mode (`nous serve` running both runtimes in goroutines) is phase-2 work.
- ~~Move charon proxy runtime similarly into lib~~: same; charon stays in `cmd/charon/` for now.
- ~~Backwards-compat shims that print deprecation~~: not done; both legacy binaries still build and work identically. Removing them is its own milestone (after M4-M5 land and operators have migrated).
- ~~Makefile build target produces `bin/nous`~~: `go build -o bin/nous ./cmd/nous` works; explicit Makefile target update is cosmetic.
- ~~Atlas updates (sync-substrate-decision, charon docs)~~: existing atlas at `atlas/charon/index.md` already covers the M2 lib reorg. Will refresh in M4 alongside the identity/brain cluster docs.

### M4 — net-new commands

- [x] `nous identity` cluster: init (port `make identity`, **TTY-only** — currently shells out to `scripts/identity.sh`; full Go port deferred), export, import (**TTY-only**, verify-fingerprint ceremony — type last 8 hex chars), list (joined view of keyring × brains). **M4a, commit pending.**
- [~] `nous identity agent prewarm/flush/status`: M4a wired `flush` (one-line gpg-connect-agent shell-out) and stubs `prewarm/status` returning "not yet implemented." Real prewarm/status need lib/agent's keychain-passphrase flow + KEYINFO parser; carry into M4b or a follow-up.
- [x] `nous brain new <path>`: guided multi-recipient flow (TTY-only when admitting non-self recipients during creation). Drops `mode:` from generated manifests. **(M4b — orchestrates `scripts/new-brain.sh` for substrate bootstrap, then re-keys via lib/brain.WriteManifest + SetGcryptParticipants + a second commit/push so gcrypt re-encrypts to all recipients. Bash script deletion deferred — script still does the heavy substrate dance; full Go port is a follow-up.)**
- [x] `nous brain recipient list/add/remove`: list is `(a)`; add and remove are `(h)` and TTY-only. **(M4b — list shows joined manifest × gcrypt-participants with mismatch warning; add runs verify-fingerprint ceremony then commits/pushes; remove enforces last-recipient guard + self-removal guard + revocation-caveat warning. Pure Go in cmd/nous/brain_recipient.go over lib/brain primitives.)**
- [~] `nous brain resolve`: mechanical conflict-find + preserve + commit (uses `lib/brainsync/`). **(Stubbed — returns "stubbed pending lib/brainsync surface refactor." `/nous-resolve` skill continues to call lib/brainsync directly. Tracked as nous#5 follow-up; not blocking M4 close because the existing skill works.)**
- [x] `nous service install/uninstall`: writes/removes both launchd plists (brain-sync + proxy) as a unit. `--migrate` upgrades old `brain-sync` plist in place. **(M3c shipped install/uninstall; --migrate not built — current install does stop+uninstall+install which covers the upgrade case.)**
- [x] `nous service start/stop`: brings up or shuts down all nous services together via launchd. **(M3c)**
- [~] `nous service status`: scriptable JSON-or-text dump (also `nous status` alias). What's installed, what's running, errors. **(M3c shipped human text; JSON mode + `nous status` alias deferred — useful but not gating.)**
- [x] `nous service doctor`: prescriptive checks (gpg setup, agent reachable, remotes pingable, services running, recipient validity). Each red item names a fix. **(M4c — 9 checks; remote-pingable deferred since `git ls-remote` is slow and gcrypt remotes have additional auth quirks.)**
- [x] `nous service audit`: unified audit log query (proxy requests + brain ops + recipient changes). **(M4c — tail/grep over `~/Library/Logs/{charon,brain-sync}.log` with `--lines`, `--grep`, `--which` flags.)**
- [x] Drop `mode:` from `.brain/config.md` schema. **(M4c — `AGENTS.md` §1 updated in ariadne + nous; `lib/brain.Manifest.Shared()` derives from `len(Recipients) > 1`; `lib/brainsync.FindSharedBrains` now uses `brain.Read` + `Shared()` rather than parsing `mode:`. Existing manifests with `mode:` still parse — preserved as `LegacyMode` for round-trip writes.)**
- [x] Update `brain/atlas/threat-model-shared-brain.md` `## Revisions` with the TTY-only delegation boundary. **(M4c — appended a 2026-05-09 entry covering both the TTY-only delegation boundary from M4a and the `mode:` drop. Anchors nous@73d61cc for M4a; M4b commit will be added when it lands.)**

### M5 — TUIs (brain + provider)

Two TUIs only. Bare `nous` stays as cobra-default help (no TUI). Identity ops use interactive CLI prompts (no full-screen TUI). Service ops are pure CLI. Domains where humans actually browse-and-act get TUIs; everything else stays as targeted CLI.

- [ ] `nous brain` — brain TUI: list of brains → drill-in (recipients, sync state, last commit, conflicts) → actions (recipient add/remove, resolve, status).
- [ ] `nous provider` — provider TUI à la today's `charon auth`: list providers → drill into config (add/remove) and auth flows (OAuth dance, token rotation). All add/remove/auth happen inside the TUI; no separate CLI subcommands for those.
- [ ] Each TUI action wraps the cobra subcommand — same logic, different rendering. Underlying ops in `lib/`.
- [ ] Manual test: run each interactively against operator's actual brains and a real provider (Anthropic). No TUI automation tests in M5; that's its own rabbit hole.
- [ ] Document the agent-vs-human help split + per-cluster-TUI choice + audience-tag scheme in `nous/atlas/nous/cli.md`.

## Notes

- **Repo identity**: `xianxu/charon` archived after M1's subtree-merge lands. Migration message in archived README.
- **charon's open issues**: `charon#21` (gpg-agent lifecycle) absorbs into M3-M4 explicitly. Other charon issues that are still relevant: file new `nous#` issues for them at cutover. Closed charon issues stay in the archived repo for historical reference.
- **charon-security menubar**: comes along on the move (M1) as `cmd/nous-menubar/`. Stays as a separate binary — macOS menubar apps want different Info.plist, lifetime model, and signing setup than a CLI. Earlier draft put `nous menubar` in the subcommand tree; dropped because the seam is real, not cosmetic. One-binary-three-modes (CLI + TUI + menubar) is its own phase-2 design, not gated by this issue.
- **`nous#13` (brain CLI unification)** — wontfix, superseded by this issue. Captured the design transition in #13's Log.
- **Top-level `nous status` aliases `nous service status`**: shorthand for the most-used read. `nous service` covers the broader surface (status, doctor, list, audit); the cluster name `service` matches the user's framing of "where I go to see what's running."
- **TUI library budget**: bubbletea + bubbles + lipgloss already in charon's go.mod; no new dependencies introduced.

## Log





- 2026-05-10: M5 close — code review addressed (6 Important findings, no Critical). Fixes in one pass: (1) SetPrimary now writes via tmp+rename with 0o600 (was bare os.WriteFile 0o644 — concurrent writes could leave half-written state); (2) confirmPersist requires explicit y/yes and treats EOF/empty as decline (was default-yes — ctrl+d on a TTY could silently persist a heuristic candidate); (3) WouldLockOut returns (true, err) on gpg outage so callers that forget the err check err safe (was fail-open); (4) `nous identity primary` emits a machine-stable single-line shape on non-TTY (atlas tags it (b), agents need parsable output); (5) reconciled (h)/(b) tag — file docstring says (b) now, matches atlas; (6) recipient-remove picker pre-computes the would-lock-out marker and renders `[⚠ would lock you out]` per row so the safeguard is visible before enter is pressed; (DRY bonus from review) lifted the brain-aware heuristic into `lib/brain.HeuristicPrimary`, both the annotator's read-only fallback and `nous identity primary`'s interactive resolver call it. workshop/lessons.md gains a M5-review section with six rules: atomic state-file writes from day one; default-yes confirmations are EOF footguns; fail-closed via the safe boolean not just the docstring; (a)/(b) audience tags require single-line non-TTY shapes; UI tier vs functional safeguard tier are separate concerns; safety markers belong in the picker, not post-selection. Plus two process notes (side-quests-mid-flight + DRY violations caught in review). All M5 review fixes go in one commit so the M5 close has a tidy review-fixes anchor.
- 2026-05-10: closed M5c (provider TUI audit + agent-vs-human atlas) — appName() now returns "nous provider" (or "(dev)" suffix) instead of "Charon"; user-visible "Charon will X" phrasings rewritten throughout `lib/tui/{admin_key_paste,admin_revoke,catalog_revoke,picker,scopes,gcp_setup}.go` to say "nous will X"; "via charon auth" → "via `nous provider`"; "charon-created projects" → "projects nous created". GCP-setup-unwired hint now says "re-launch with the production binary" instead of pointing at a non-existent `charon gcp setup` command. Internal package names (lib/charoncli), HTTP header `X-Charon-Account`, log paths (`~/Library/Logs/charon.log`), and Go type names (CreatedByCharon) intentionally left alone — those are protocol/filesystem identifiers, not user-facing copy. `cmd/nous/main.go:providerCmd` Long expanded with explicit audience-tag block. `atlas/nous/cli.md` lands as the canonical doc for the agent-vs-human split, cluster map, audience-tag scheme (a)/(h)/(b), and per-cluster-TUI rationale (brain + provider = TUI; identity = sequenced prompts; service = pure CLI). Cross-refs to issue 000014, threat model revisions, lib-layout. Tests: provider_picker_test updated for the new title; all lib/tui tests green.
- 2026-05-10: closed M5b (brain TUI recipient add/remove + primary-identity side-quest) — TUI flows: bare `a` launches a 4-stage add-recipient ceremony (textinput pubkey path → identity.Inspect → render fp/uid/last-8 → textinput last-8 with up-to-3 attempts → async apply: import + RewriteFrontmatter + SetGcryptParticipants + AddCommitPush → done banner). Bare `r` launches a 5-stage remove flow with all three safeguards: picker, last-recipient guard banner, optional REMOVE-SELF textinput (fires on `WouldLockOut`, not on every local-secret-half key), required REVOKED-OUT-OF-BAND textinput, async apply, done banner. Pure-Go helpers extracted to `lib/brain/recipient.go` (MatchRecipient, CanRemoveRecipient, WithoutRecipient, ContainsRecipient, LocalSecretFingerprints, WouldLockOut) so CLI and TUI share one implementation; cmd/nous/brain_recipient.go re-points onto them, deletes dead containsFold helper. **Side-quest, triggered by operator feedback during live-test:** adding a throwaway test key to the keyring exposed the bug that `(self)` was being painted on every secret-half key, not just the operator's actual identity. Introduced primary-identity concept: `lib/identity/primary.go` (state file at $UserConfigDir/nous/primary-identity, Primary/SetPrimary/ClearPrimary/IsPrimary/ErrPrimaryUnset+ErrPrimaryStale, resolves stored-state → only-one-secret implicit → unset); `nous identity primary` subcommand (no-args prints stored value, runs brain-aware heuristic for a private-brain recipient match and offers to persist, lists candidates on ambiguous; with-fp validates and persists); `lib/brain/annotate.go` rewritten to mark only primary as `(self)` and other secret-half keys as `(local secret)`, with the brain heuristic as a read-only fallback when state is unset; self-removal safeguard reworked from `IsOwnKey` (any local secret) to `WouldLockOut` (would removing this leave no decrypt path on this brain?). Operator-facing UI fix in the same pass: `nous brain` TUI and `nous brain list` now display the directory basename instead of manifest.Name (so `~/workspace/brain` shows as `brain`, not `personal`). Live-test pending — operator to drill into brain-shared-test, add throwaway, verify ceremony, remove, verify safeguards.
- 2026-05-10: closed M5a (brain TUI read-only views) — bare `nous brain` launches a bubbletea program (list → drill-in → conflict preview) on TTY, falls through to `--help` on non-TTY so agent transcripts stay clean. New `lib/brain/status.go` aggregator (`LoadStatus`) joins manifest × gcrypt-participants, reads HEAD + ahead/behind vs `@{u}`, and walks the brain dir for `*.conflict-<peer>-<utc>.<ext>` files. New `lib/brain/annotate.go` lifts the self/peer/unknown recipient annotator out of `cmd/nous/brain_recipient.go` so CLI and TUI share one implementation. New `lib/tui/brain/` package (sibling of charon's `lib/tui/`, not mixed in): `list.go` (workspace brains), `detail.go` (recipients / sync / conflicts sections + mismatch warning), `conflict_preview.go` (first-20-lines pager with marker highlighting), `root.go` (screen stack list ⇄ detail ⇄ conflict). Action keys `a`/`r` show a "lands in M5b" banner. Tests: 9 lib/brain tests including end-to-end LoadStatus against a synthetic git repo with upstream. Live-tested by operator: TUI launches, list + drill-in render correctly against `personal` + `shared-test` brains. Cross-checked aggregator output (`ahead 2 / behind 0` for brain repo) against `git rev-list --left-right --count HEAD...@{u}`.
- 2026-05-09: closed M4 — M4a/b/c live-tested on operator (nous identity list shows joined keyring x brains; nous brain list enumerates 2 brains; nous service doctor 9/9 green; TTY-only refusal verified). Multi-recipient e2e deferred. M4 review found 3 Important; addressed in bf9c0ff. Lessons captured.
- 2026-05-09: closed M4b (brain provisioning + recipients) — code-complete on this machine; full multi-recipient e2e (admit wife's pubkey, push, wife clones) requires wife's pubkey in operator's keyring which needs her machine. `lib/brain/write.go` ships WriteManifest (atomic, no `mode:`, sorted recipients, multi-recipient body), SetGcryptParticipants, ReadGcryptParticipants. `cmd/nous/brain.go` cluster: `nous brain new` orchestrates substrate-bootstrap (scripts/new-brain.sh, single-recipient) → re-key in-Go → commit/push so gcrypt re-encrypts to full recipient set; `nous brain list` enumerates brains under workspace root with shared/private + recipient count + sync substrate; `nous brain recipient list` shows joined manifest × gcrypt-participants with mismatch warning + per-key annotation (self/peer/unknown); `nous brain recipient add` does verify-fingerprint ceremony + push (TTY-only); `nous brain recipient remove` enforces all three safeguards (last-recipient guard, self-removal warning + --force, revocation-caveat) (TTY-only); `nous brain resolve` stubbed pending lib/brainsync refactor (nous#5 follow-up). Bash-script deletion deferred — scripts/new-brain.sh still bootstraps substrate; full Go port is non-blocking. Live-tested: `nous brain list` shows both brains correctly; `nous brain recipient list ~/workspace/brain` shows operator's key annotated `(self)`; TTY-only refusal verified for add/remove. M4 milestone code review pending before final close.
- 2026-05-09: closed M4c (service observability + schema cleanup) — `nous service doctor` (9 prescriptive checks: gpg installed, gpg-agent reachable, identity exists, brain manifests parse, recipient fingerprints in keyring, charon installed, brain-sync installed, charon running, brain-sync running) and `nous service audit` (`--lines`, `--grep`, `--which` over the two launchd-managed log files) wired in `cmd/nous/{doctor,audit}.go`. `mode:` field dropped from .brain/config.md schema: ariadne `AGENTS.md` §1 updated, nous `AGENTS.md` updated, `lib/brain.Manifest.Shared()` replaces the field as the derived signal, `lib/brainsync.FindSharedBrains` switches to `brain.Read + Shared()`, `LegacyMode` preserved for round-trip. Threat model `## Revisions` updated with both the TTY-only delegation boundary (from M4a) and the schema cleanup. Live-tested doctor against operator's actual state — all 9 checks green; live-tested audit `--grep CONNECT` surfaced the session_disarmed errors that motivated nous#17. Skipped: `nous service status` JSON mode + `nous status` alias (not gating); `nous brain` doctor sub-pieces (e.g., remote pingable) — `git ls-remote` is slow and gcrypt's auth quirks make it unreliable as a doctor signal. M4 not yet closed — M4b (brain provisioning + recipients) still pending; needs wife's pubkey for full e2e smoke test.
- 2026-05-09: closed M4a (identity cluster) — `lib/identity/` (List, ListPublic, Export, Inspect, Import, Last8) and `lib/brain/` (Manifest, Read, DiscoverAll) land as net-new packages. `nous identity {init,export,import,list,agent}` all wired. Tests: keygen-against-tempdir for the lib (gated on macOS unix-socket path length — uses /tmp/ngpg-N rather than t.TempDir's /var/folders path); promptVerify table-tests for the verify-fingerprint ceremony; keyBrains formatting tests. Smoke-tested against operator's real keyring: `nous identity list` showed `3872c2f0  [personal, shared-test]  Xian Xu <xianxu@gmail.com>`. Init shells out to scripts/identity.sh — 200 lines of pinentry-mac/keychain config not worth re-porting until surface stabilizes. agent prewarm/status stubbed; flush works via gpg-connect-agent.
- 2026-05-09: closed M3 — 4 sub-commits (a-d) all green; refactor cmd/charon→lib/charoncli; cmd/nous binary mounts cluster subcommands; nous service unifies brain-sync+charon launchd plists; lib/agent foundation live-tested against actual keyring
- 2026-05-09: closed M2 — git mv 8 packages from internal/charon/ to lib/{tui, service, security, provider/*}; sed-rewrite 71 imports; go build + go test ./... green across all relocated packages; cross-import rule verified clean (lib/provider ⊥ lib/brainsync); atlas/nous/lib-layout.md created
- 2026-05-09: closed M1 — go build ./... + go test ./... green; charon binary smoke-tested (charon --help, manifest, instructions, scopes all functional against real vault state); 70 import paths rewritten, 0 charon-prefix imports remain; nous-security binary builds clean post-rename
### 2026-05-08 — created (supersedes nous#13)

Surfaced from `nous#12 M1` (provision brain-shared-family) ergonomics review. Initial scope was just brain CLI (nous#13); evolved through design conversation into "absorb charon, single nous CLI, unified observability." Path B chosen: build the tooling first, then provision brain-family.

Key design decisions captured at creation:

1. **Use cases drive subcommand structure**, not "translate existing tools." Resulting clusters: `identity`, `brain`, `provider`, `service`. Top-level: `instructions`, `manifest`, `menubar`.
2. **Agent-vs-human help split**: subcommand `--help` is the agent's manual (skill-as-script applies); TUI is the human's UI. Both wrap shared lib operations. Separation prevents mutual pollution. **Every command in the subcommand tree is tagged with audience** `(h)` / `(a)` / `(b)` so design-time intent is preserved as the surface evolves.
3. **Progressive disclosure on instructions and manifest**: default = overall summary; `<cluster>` topic narrows; `<cluster>:<filter>` narrows further; `manifest all` is the explicit kitchen-sink. Agents fetch what they need, not the universe. Cobra's per-subcommand `--help` already follows this; `instructions` and `manifest` extend the same shape.
4. **`nous service` cluster** absorbs both observability (status, doctor, audit) and unified service control (install/start/stop applies to ALL services together — brain-sync + proxy as one unit). Pure CLI, no TUI. Cluster name `service` (not `obs`) reflects "where you go to see what's running, fix what's broken, and inspect what happened."
5. **Subtree-merge over rewrite**: preserve charon's git history under `cmd/charon/` initially via subtree, then restructure piece by piece. Lower-risk than a full rewrite.
6. **`nous` is the binary name**: matches repo, replaces `brain-sync` and `charon`, backward-compat via aliases during transition.
7. **Two TUIs only — `nous brain` and `nous provider`**: domain-focused, not a forced merge across clusters. Bare `nous` prints help. Identity ops use interactive CLI prompts where needed (no full TUI; humans rarely browse keys). Service ops are pure CLI (mechanical, scriptable). TUIs live where humans actually browse-and-act.

   **Service controls all services together** — `nous service install/start/stop` brings up brain-sync AND the proxy as one unit. No per-subsystem service subcommands like `nous brain sync install` or `nous provider proxy install`. There's no value in starting one without the other.
8. **Menubar stays a separate cmd**: `cmd/nous-menubar/` (was `cmd/charon-security/`). macOS menubar app's Info.plist + lifetime + signing differs from CLI; absorbing into one binary is phase-2.
9. **Lib-first code structure**: every operation lives in `lib/` (organized by domain — brain, provider, identity, agent, service, tui); `cmd/nous/` is thin wrappers. Allows future repackaging (e.g. a charon-only binary if ever needed outside the nous ecosystem) without moving logic. Lib modules don't import each other unless there's a real domain dependency.
10. **Identity-and-access ops are TTY-only**: `nous identity init`, `nous identity import`, `nous brain recipient add` refuse non-TTY invocation. The verify-fingerprint ceremony (type 8 hex chars) sits on top. Together: agents on the device cannot silently expand their access — the human's terminal must be present. Sits on the existing threat-model `agent-as-threat for brain-private` posture; adds the *delegation* boundary specifically.
