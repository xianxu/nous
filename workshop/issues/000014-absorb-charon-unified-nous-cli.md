---
id: 000014
status: open
deps: [000004]
created: 2026-05-08
updated: 2026-05-08
estimate_hours: 16
---

# Absorb charon — unified `nous` CLI + TUI

## Problem

nous-substrate operations are split across two repos and multiple binaries:

- **`charon`** repo: AI-credential proxy (`charon serve`), provider/OAuth management (`charon auth`), instructions (`charon instructions`), manifest, plus a separate `charon-security` macOS menubar app.
- **`nous` repo**: workflow conventions, the `brain-sync` Go daemon, brain provisioning bash scripts (`new-brain.sh`, `cloneto.sh`, `moveto.sh`), the `/nous-resolve` skill.

Both serve nous (the operator's AI-coding tool); both have daemon + TUI shapes; both manage credentials (charon: API tokens, OAuth; brain: GPG keys); both want gpg-agent lifecycle management (`charon#21` literally about gpg-agent; brain's gcrypt encrypt/decrypt leans on it); both use cobra + bubbletea + lipgloss; both ship as launchd services. There's real overlap, and parallel infrastructure means double-built TUI patterns + service plumbing + credential abstractions.

User's directive: merge them. charon's repo gets archived. The combined surface lives at `nous/cmd/nous/` (one binary), with subcommands that emerge from use cases rather than from translating the existing tools.

This supersedes `nous#13` (brain CLI unification, narrower-in-scope).

## Spec

### One binary, four use-case clusters + cross-cutting top-level commands

```
nous                          # cobra-default help: lists clusters + entry points

nous identity ...             # GPG keys + agent + peers (no TUI; interactive CLI for the (h) ops)
nous brain ...                # brains: provision, sync, recipients (TUI at `nous brain`)
nous provider ...             # AI providers: auth + config (TUI at `nous provider auth`)
nous service ...              # service control + observability (CLI only; no TUI)

nous instructions [topic]     # agent-only: canonical guide (progressive disclosure)
nous manifest [topic[:filter]] # agent-only: machine-readable state introspection
```

**Service is unified**: `nous service install/start/stop` controls *all* nous services together (brain-sync + provider proxy). No per-subsystem service subcommands like `nous brain sync install` — there's no value in starting brain-sync without the proxy or vice versa. The proxy daemon doesn't even have its own subcommand path; it's just one of the things `nous service start` brings up.

**Two TUIs only** (`nous brain` and `nous provider auth`) — domain-focused, not a forced merge across clusters. Identity = interactive CLI prompts where needed. Service = pure CLI. Bare `nous` = cobra help.

**Menubar is its own binary**: `cmd/charon-security/` (renamed `cmd/nous-menubar/` on the move) stays as a separate cmd. macOS app mode runs into the menubar-vs-CLI build seam (different Info.plist, lifetime, signing). Eventual one-binary-three-modes is phase-2 work, not this issue.

The clusters emerged from walking through actual use cases:

| Use case | Cluster |
|---|---|
| Generate keypair, export pubkey, import & verify peer key, gpg-agent lifecycle | `nous identity` |
| Provision brain (private/shared), recipient list/add/remove, resolve conflicts, brain status | `nous brain` |
| Configure AI provider, OAuth, paste API key | `nous provider` |
| Install/start/stop services (brain-sync + proxy together), status, doctor, audit | `nous service` |

`nous service` is the cross-cutting cluster that absorbs both observability (was: scattered across `charon-security` + `brain-sync status` + ad-hoc) and service-management (install/start/stop launchd plists). It controls all nous services together — there's no value in starting brain-sync without the proxy or vice versa. No per-subsystem service subcommands.

### Subcommand tree (full)

Each command is tagged with intended audience: **(h)** human-primary (interactive prompts or TUI), **(a)** agent-primary (machine-readable, scriptable). When `(h)` is a full bubbletea TUI, it's marked `(h, TUI)`; otherwise `(h)` means interactive CLI prompts (no full screen, just sequenced questions).

```
# Identity — keys + agent + peers. Mostly run-once or shell-hook ops; no TUI.
nous identity init                     (h)  guided keypair generation (interactive prompts)
nous identity export [--my-fp]         (a)  print armored pubkey for sneakernet
nous identity import <file>            (h)  verify-fingerprint ceremony (type last 8 hex chars)
nous identity list                     (a)  keys with brain-context (--json)
nous identity agent prewarm            (a)  shell hook / session start
nous identity agent flush              (a)  shell hook / session end
nous identity agent status             (a)  cache + lifecycle state

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

# Provider — auth/config for AI providers. Bare `nous provider auth` opens TUI (à la `charon auth`).
nous provider auth [<name>]            (h, TUI)  list providers + drill into auth flows; <name> targets one
nous provider list                     (a)  machine-readable list (--json) — agent inspection
nous provider add <name>               (h)  guided: paste key, fill config
nous provider remove <name>            (h)  confirmation prompt

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

**Two TUIs only**: `nous brain` and `nous provider auth`. Both are domain-focused (no forced merge across clusters). Identity ops are interactive CLI prompts (no full-screen TUI — humans rarely "browse" their keys). Service ops are pure CLI (mechanical, scriptable, runs from launchd hooks).

Bare `nous` prints the standard cobra help — clusters listed, no TUI launched.

### Design principle: agent-facing help vs. human-facing TUI

This is the structural insight that makes the TUI investment safe.

**Agent-facing surface = subcommand `--help` text + missing-arg explainers.** When Claude Code (or any coding agent) drives `nous`, it discovers capabilities by walking `nous --help`, `nous brain --help`, `nous brain recipient add --help`. It hits explainers when args are missing (warmup pattern, like `close-issue.py`). This is the agent's manual. It must be:
- Dense and accurate
- Self-sufficient (the agent should not need to read SKILL.md or atlas to drive a command)
- Source-of-truth for procedures (the verify-fingerprint ceremony, the safeguards, what files get touched)

**Human-facing surface = per-cluster TUIs** (`nous brain`, `nous provider`, `nous identity`, `nous service` — each invokes its own bubbletea TUI). Screen labels, button text, inline hints. **Doesn't render in any agent transcript** when the agent only sees `nous --help`. Humans get conversational, multi-step, visually-rich flows scoped to one domain; agents get a dense, scannable manual — neither pollutes the other.

Per-cluster TUIs (not a unified status board) because the clusters are genuinely different domains: the provider auth flow has nothing in common with brain-recipient management beyond "both involve secrets." A unified board would force unrelated state into one screen. Each cluster's TUI is focused on that cluster (`nous provider` looks like today's `charon auth`; `nous brain` is similar shape for brain ops). Bare `nous` prints help, no TUI.

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
| `charon auth <provider>` | `nous provider auth <name>` (the human TUI; lists + drills in) |
| `charon auth` (TUI listing providers) | `nous provider auth` (no name) |
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
| M4 — net-new commands: `nous identity` cluster; `nous brain recipient` w/ safeguards; `nous brain new` guided; `nous service status/doctor/services/audit` | Greenfield Go (medium) | 3–5 |
| M5 — Per-cluster TUIs (brain, provider, identity, service); each focused on its domain | Bubbletea (familiar from charon) | 3–5 |
| **+30% design buffer** | | +0.5–1.5 |
| **Total** | | **~12.5–22.5** |

Familiarity ×1: cobra + bubbletea + gpg/git pipelines all known from charon; the work is composition. Largest unknowns are (a) the subtree-merge dance preserving sane git history, (b) TUI layout iteration, (c) reconciling charon's idiosyncrasies with nous conventions.

## Plan

### M1 — subtree-merge charon → nous

- [ ] `git subtree add --prefix=cmd/charon https://github.com/xianxu/charon main --squash` (or filter-repo dance for unsquashed history). Decide squash vs preserve based on whether full charon log is worth the noise.
- [ ] Move `charon-security/` → `nous/cmd/charon-security/`.
- [ ] Move charon's `internal/` → `nous/internal/charon/` (so chamber lib doesn't pollute global `internal/`).
- [ ] Reconcile `go.mod`: nous and charon get merged; module path stays `github.com/xianxu/nous`.
- [ ] Both binaries build via `make build`; existing tests pass for both. `make nous-test-brain-sync` still green; charon's tests still green.
- [ ] AGENTS.md / CLAUDE.md: charon's AGENTS.md content folded into nous's where unique; nous's stays canonical.
- [ ] Atlas: charon's atlas content moves to `nous/atlas/charon/` with reorg notes.

### M2 — extract shared libs

- [ ] `lib/tui/`: pull lipgloss styles + bubbletea form components from charon's `internal/tui/` into a public-ish lib. Both charon and (future) nous-cmd use it.
- [ ] `lib/agent/`: gpg-agent ops (prewarm, flush, status). charon#21's primary work lands here.
- [ ] `lib/service/`: launchd plist generation + service control. brain-sync's `service_darwin.go` and charon's equivalent merged.
- [ ] No CLI changes — refactor only. Existing binaries still work.

### M3 — `nous` cobra root + subcommand restructuring

- [ ] New `cmd/nous/main.go` with cobra root + cluster subcommands.
- [ ] Move existing brain-sync runtime (the foreground watcher loop in `cmd/brain-sync/`) into `lib/brainsync/runner.go` (or similar); invoked by the launchd-installed service binary, not as its own subcommand.
- [ ] Move `cmd/charon/` proxy runtime similarly into a lib; invoked by the launchd-installed service binary.
- [ ] Move `cmd/charon/` user-facing subcommands under `nous provider auth`, `nous provider list`, etc.; `nous instructions` and `nous manifest` at top level.
- [ ] `nous service install` writes both launchd plists (brain-sync + proxy); single command brings up the full nous-substrate. `--migrate` handles in-place upgrade from old `brain-sync` plist.
- [ ] Backwards-compat: `cmd/brain-sync/` and `cmd/charon/` stop building (or alias-via-shim that prints deprecation + delegates to `nous`).
- [ ] charon#21 M1 (config + keygrip discovery) lands as `nous identity agent` foundation.
- [ ] `Makefile.nous`: build target produces `bin/nous`. Old aliases (`make brain-sync`, `make charon`) point to `bin/nous` or removed.
- [ ] Update atlas + sync-substrate-decision atlas + charon docs to reference `nous ...`.

### M4 — net-new commands

- [ ] `nous identity` cluster: init (port `make identity`), export, import (with verify-fingerprint ceremony — type last 8 hex chars), list (joined view of keyring × brains).
- [ ] `nous identity agent prewarm/flush/status`: charon#21 M2-M3 work lands here.
- [ ] `nous brain new <path>`: guided multi-recipient flow. Drops `mode:` from generated manifests. Replaces `make new-brain`. Deletes bash scripts.
- [ ] `nous brain recipient list/add/remove`: full surface with all four safeguards (self-removal guard, last-recipient guard, verify-before-add, revocation warning).
- [ ] `nous brain resolve`: mechanical conflict-find + preserve + commit (uses `lib/brainsync/`). The `/nous-resolve` Claude Code skill is updated to call this for mechanical bits while still doing the agent-driven semantic merge in-session.
- [ ] `nous service install/uninstall`: writes/removes both launchd plists (brain-sync + proxy) as a unit. `--migrate` upgrades old `brain-sync` plist in place.
- [ ] `nous service start/stop`: brings up or shuts down all nous services together via launchd.
- [ ] `nous service status`: scriptable JSON-or-text dump (also `nous status` alias). What's installed, what's running, errors.
- [ ] `nous service doctor`: prescriptive checks (gpg setup, agent reachable, remotes pingable, services running, recipient validity). Each red item names a fix.
- [ ] `nous service audit`: unified audit log query (proxy requests + brain ops + recipient changes).
- [ ] Drop `mode:` from `.brain/config.md` schema. Migration: scan existing brains, auto-register via `nous service install`.

### M5 — TUIs (brain + provider)

Two TUIs only. Bare `nous` stays as cobra-default help (no TUI). Identity ops use interactive CLI prompts (no full-screen TUI). Service ops are pure CLI. Domains where humans actually browse-and-act get TUIs; everything else stays as targeted CLI.

- [ ] `nous brain` — brain TUI: list of brains → drill-in (recipients, sync state, last commit, conflicts) → actions (recipient add/remove, resolve, status).
- [ ] `nous provider auth [<name>]` — provider TUI à la today's `charon auth`: list providers → drill into auth flows (OAuth dance, token rotation). With `<name>`, jumps straight to that provider's flow.
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

### 2026-05-08 — created (supersedes nous#13)

Surfaced from `nous#12 M1` (provision brain-shared-family) ergonomics review. Initial scope was just brain CLI (nous#13); evolved through design conversation into "absorb charon, single nous CLI, unified observability." Path B chosen: build the tooling first, then provision brain-family.

Key design decisions captured at creation:

1. **Use cases drive subcommand structure**, not "translate existing tools." Resulting clusters: `identity`, `brain`, `provider`, `service`. Top-level: `instructions`, `manifest`, `menubar`.
2. **Agent-vs-human help split**: subcommand `--help` is the agent's manual (skill-as-script applies); TUI is the human's UI. Both wrap shared lib operations. Separation prevents mutual pollution. **Every command in the subcommand tree is tagged with audience** `(h)` / `(a)` / `(b)` so design-time intent is preserved as the surface evolves.
3. **Progressive disclosure on instructions and manifest**: default = overall summary; `<cluster>` topic narrows; `<cluster>:<filter>` narrows further; `manifest all` is the explicit kitchen-sink. Agents fetch what they need, not the universe. Cobra's per-subcommand `--help` already follows this; `instructions` and `manifest` extend the same shape.
4. **`nous service` cluster** absorbs both observability (status, doctor, audit) and unified service control (install/start/stop applies to ALL services together — brain-sync + proxy as one unit). Pure CLI, no TUI. Cluster name `service` (not `obs`) reflects "where you go to see what's running, fix what's broken, and inspect what happened."
5. **Subtree-merge over rewrite**: preserve charon's git history under `cmd/charon/` initially via subtree, then restructure piece by piece. Lower-risk than a full rewrite.
6. **`nous` is the binary name**: matches repo, replaces `brain-sync` and `charon`, backward-compat via aliases during transition.
7. **Two TUIs only — `nous brain` and `nous provider auth`**: domain-focused, not a forced merge across clusters. Bare `nous` prints help. Identity ops use interactive CLI prompts where needed (no full TUI; humans rarely browse keys). Service ops are pure CLI (mechanical, scriptable). TUIs live where humans actually browse-and-act.

   **Service controls all services together** — `nous service install/start/stop` brings up brain-sync AND the proxy as one unit. No per-subsystem service subcommands like `nous brain sync install` or `nous provider proxy install`. There's no value in starting one without the other.
8. **Menubar stays a separate cmd**: `cmd/nous-menubar/` (was `cmd/charon-security/`). macOS menubar app's Info.plist + lifetime + signing differs from CLI; absorbing into one binary is phase-2.
