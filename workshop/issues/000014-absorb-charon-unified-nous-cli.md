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
nous                          # default: TUI status board (brains + providers + services + recent events)

nous identity ...             # GPG keys + agent + peers
nous brain ...                # brains: provision, sync, recipients, resolve
nous provider ...             # AI providers: config, auth, proxy daemon
nous service ...                  # observability + service control (status, doctor, install/start/stop)

nous instructions             # canonical agent guide (charon's existing pattern)
nous manifest                 # machine-readable state introspection
nous menubar                  # macOS app mode (eventually absorbs charon-security)
```

The clusters emerged from walking through actual use cases:

| Use case | Cluster |
|---|---|
| Generate keypair, export pubkey, import & verify peer key, gpg-agent lifecycle | `nous identity` |
| Provision brain (private/shared), recipient list/add/remove, sync daemon, resolve conflicts | `nous brain` |
| Configure AI provider, OAuth, paste API key, run proxy daemon | `nous provider` |
| Status board, health checks, install/start/stop services | `nous service` |

`nous service` is the cross-cutting cluster that absorbs both observability (was: scattered across `charon-security` + `brain-sync status` + ad-hoc) and service-management (install/start/stop launchd plists). Per-subsystem service control still lives in the cluster (`nous brain sync install`, `nous provider proxy install`) because it's natural there; `nous service` adds the *unified view* across all of them.

### Subcommand tree (full)

```
nous identity init             generate keypair (was: make identity)
nous identity export [--my-fp]  armor my pubkey for sneakernet
nous identity import <file>     import peer pubkey + verify-fingerprint ceremony
nous identity list              keys in keyring with brain-context (named, admitted-where, signed-status)
nous identity agent prewarm     pre-cache passphrase (charon#21 M2)
nous identity agent flush       clear gpg-agent cache (charon#21 M3)
nous identity agent status      show cache + lifecycle state

nous brain new <path>           provision (was: make new-brain) — guided, multi-recipient
nous brain list                 all known brains, recipients, sync state
nous brain recipient list <brain>
nous brain recipient add <brain>     uses `nous identity import` internally; appends to brain
nous brain recipient remove <brain>  with self / last-recipient / revocation safeguards
nous brain sync                       foreground watcher (was: brain-sync)
nous brain sync install/start/stop/status [brain]
nous brain resolve <brain> [--undo]   mechanical side of /nous-resolve skill
nous brain status [brain]             health: remote reachable, recipients valid, sync healthy

nous provider list              configured + state
nous provider add <name>        set up a provider (paste key, OAuth, etc.)
nous provider remove <name>
nous provider auth <name>       OAuth dance or rotate key (was: charon auth)
nous provider proxy             foreground proxy (was: bare charon / charon serve)
nous provider proxy install/start/stop/status

nous service status                 unified state dump (scriptable, cron-able, --json available)
nous service doctor                 prescriptive checks with verdicts + named fixes
nous service list                   all installed launchd services + state (across brain sync, provider proxy)
nous service audit [--since ...]    unified audit log (proxy requests + brain ops + recipient changes)

nous instructions [topic]       agent guide. Default = overall scope (clusters + top-level commands).
                                Topic narrows: `nous instructions brain`, `nous instructions provider`,
                                `nous instructions identity`, `nous instructions service`.
                                Progressive disclosure — agents fetch only the topic they need.

nous manifest [topic[:filter]]  machine-readable state. Default = summary (version, paths, top-level state).
                                Topic narrows: `nous manifest provider` (configured providers + state),
                                `nous manifest provider:permission` (what each provider permits, e.g. gmail
                                scopes), `nous manifest brain` (brains + recipients + sync state),
                                `nous manifest all` (kitchen-sink dump).
                                Same progressive-disclosure principle as instructions.

nous menubar                    macOS app mode (defers to phase 2; today this is charon-security)
```

### Design principle: agent-facing help vs. human-facing TUI

This is the structural insight that makes the TUI investment safe.

**Agent-facing surface = subcommand `--help` text + missing-arg explainers.** When Claude Code (or any coding agent) drives `nous`, it discovers capabilities by walking `nous --help`, `nous brain --help`, `nous brain recipient add --help`. It hits explainers when args are missing (warmup pattern, like `close-issue.py`). This is the agent's manual. It must be:
- Dense and accurate
- Self-sufficient (the agent should not need to read SKILL.md or atlas to drive a command)
- Source-of-truth for procedures (the verify-fingerprint ceremony, the safeguards, what files get touched)

**Human-facing surface = the TUI** (`nous` with no subcommand, drilling into clusters). Bubbletea-rendered. Screen labels, button text, inline hints. **Doesn't render in any agent transcript** when the agent only sees `nous --help`. So humans get conversational, multi-step, visually-rich flows, while agents get a dense, scannable manual — neither pollutes the other.

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
| `brain-sync` (foreground) | `nous brain sync` |
| `brain-sync service install` | `nous brain sync install` |
| `charon` (foreground proxy) | `nous provider proxy` |
| `charon serve` | `nous provider proxy serve` (or just `nous provider proxy`) |
| `charon auth <provider>` | `nous provider auth <name>` |
| `charon instructions` | `nous instructions` |
| `charon manifest` | `nous manifest` |
| `charon-security` (menubar app) | `nous menubar` (eventually — separate cmd until then) |
| `/nous-resolve <brain-root>` (CC skill) | Skill stays + calls `nous brain resolve --auto` for mechanical bits |

## Done when

- One Go binary at `cmd/nous/` exposing the full subcommand tree above.
- `xianxu/charon` repo archived with redirect.
- All `make` targets that wrap brain/charon ops become thin aliases to `nous ...` (or get removed).
- A user (operator's wife, on her Mac after `make nous-bootstrap`) can:
  1. Run `nous` (no subcommand) and see the TUI status board for both brains and providers.
  2. Run `nous brain new ../brain-private-ying` interactively and end up with a working private brain.
  3. Run `nous brain recipient add ../brain-shared-family` against a sneakernet'd pubkey, complete the verify-fingerprint dance, and have her husband admitted.
  4. Run `nous service doctor` and see green/yellow/red verdicts across GPG, agent, brains, providers, services.
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
| M5 — TUI shell (bubbletea status board + drill-in submenus for each cluster) | Bubbletea (familiar from charon) | 3–5 |
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
- [ ] Move existing `cmd/brain-sync/` subcommands under `nous brain sync ...`.
- [ ] Move `cmd/charon/` subcommands under `nous provider ...` and `nous instructions`/`nous manifest` at top level.
- [ ] Backwards-compat: `cmd/brain-sync/` and `cmd/charon/` stop building (or alias-via-shim that prints deprecation + delegates to `nous`).
- [ ] charon#21 M1 (config + keygrip discovery) lands as `nous identity agent` foundation.
- [ ] `Makefile.nous`: build target produces `bin/nous`. Old aliases (`make brain-sync`, `make charon`) point to `bin/nous` or removed.
- [ ] Update launchd plist templates to use new binary path. `nous brain sync install --migrate` handles in-place upgrade from `brain-sync` plist.
- [ ] Update atlas + sync-substrate-decision atlas + charon docs to reference `nous ...`.

### M4 — net-new commands

- [ ] `nous identity` cluster: init (port `make identity`), export, import (with verify-fingerprint ceremony — type last 8 hex chars), list (joined view of keyring × brains).
- [ ] `nous identity agent prewarm/flush/status`: charon#21 M2-M3 work lands here.
- [ ] `nous brain new <path>`: guided multi-recipient flow. Drops `mode:` from generated manifests. Replaces `make new-brain`. Deletes bash scripts.
- [ ] `nous brain recipient list/add/remove`: full surface with all four safeguards (self-removal guard, last-recipient guard, verify-before-add, revocation warning).
- [ ] `nous brain resolve`: mechanical conflict-find + preserve + commit (uses `lib/brainsync/`). The `/nous-resolve` Claude Code skill is updated to call this for mechanical bits while still doing the agent-driven semantic merge in-session.
- [ ] `nous service status`: scriptable JSON-or-text dump. List of brains, providers, services with state.
- [ ] `nous service doctor`: prescriptive checks (gpg setup, agent reachable, remotes pingable, services running, recipient validity). Each red item names a fix.
- [ ] `nous service services`: all installed launchd services across all subsystems + state.
- [ ] `nous service audit`: unified audit log query (proxy requests + brain ops + recipient changes).
- [ ] Drop `mode:` from `.brain/config.md` schema. Migration: scan existing brains, auto-register via `nous brain sync install`.

### M5 — TUI shell

- [ ] `nous` (no subcommand) launches bubbletea status board: brains card, providers card, services card, recent-events card.
- [ ] Drill-in submenus per cluster:
  - **Brains**: list → detail (recipients, sync state, last commit) → actions (recipient add/remove, sync install/start/stop, resolve)
  - **Providers**: list → detail (auth state, token expiry, routing) → actions (auth, rotate, remove)
  - **Services**: per-service install/start/stop with live state
  - **Doctor**: cards for each check with green/yellow/red and the fix command
- [ ] Each TUI action wraps the cobra subcommand — same logic, different rendering. Underlying ops in `lib/`.
- [ ] Manual test: run interactively on operator's brain-private + brain-shared-family + Anthropic provider. No TUI automation tests in M5; that's its own rabbit hole.
- [ ] Document the agent-facing-vs-TUI design principle in `nous/atlas/nous/cli.md`.

## Notes

- **Repo identity**: `xianxu/charon` archived after M1's subtree-merge lands. Migration message in archived README.
- **charon's open issues**: `charon#21` (gpg-agent lifecycle) absorbs into M3-M4 explicitly. Other charon issues that are still relevant: file new `nous#` issues for them at cutover. Closed charon issues stay in the archived repo for historical reference.
- **charon-security menubar**: comes along on the move (M1). Continued life as separate cmd until eventual menubar-merge phase (deferred — not in this issue).
- **`nous#13` (brain CLI unification)** — wontfix, superseded by this issue. Captured the design transition in #13's Log.
- **Top-level `nous status` aliases `nous service status`**: shorthand for the most-used read. `nous service` covers the broader surface (status, doctor, list, audit); the cluster name `service` matches the user's framing of "where I go to see what's running."
- **TUI library budget**: bubbletea + bubbles + lipgloss already in charon's go.mod; no new dependencies introduced.

## Log

### 2026-05-08 — created (supersedes nous#13)

Surfaced from `nous#12 M1` (provision brain-shared-family) ergonomics review. Initial scope was just brain CLI (nous#13); evolved through design conversation into "absorb charon, single nous CLI, unified observability." Path B chosen: build the tooling first, then provision brain-family.

Key design decisions captured at creation:

1. **Use cases drive subcommand structure**, not "translate existing tools." Resulting clusters: `identity`, `brain`, `provider`, `service`. Top-level: `instructions`, `manifest`, `menubar`.
2. **Agent-vs-human help split**: subcommand `--help` is the agent's manual (skill-as-script applies); TUI is the human's UI. Both wrap shared lib operations. Separation prevents mutual pollution.
3. **Progressive disclosure on instructions and manifest**: default = overall summary; `<cluster>` topic narrows; `<cluster>:<filter>` narrows further; `manifest all` is the explicit kitchen-sink. Agents fetch what they need, not the universe. Cobra's per-subcommand `--help` already follows this; `instructions` and `manifest` extend the same shape.
4. **`nous service` cluster** absorbs both observability (status, doctor, audit) and unified service-control views (list across all subsystems). Per-subsystem install/start/stop stays in the cluster (`nous brain sync install`, `nous provider proxy install`) — discoverable where the user already is. The cluster name `service` (not `obs`) reflects the user's framing: this is where you go to see what's running, fix what's broken, and inspect what happened.
5. **Subtree-merge over rewrite**: preserve charon's git history under `cmd/charon/` initially via subtree, then restructure piece by piece. Lower-risk than a full rewrite.
6. **`nous` is the binary name**: matches repo, replaces `brain-sync` and `charon`, backward-compat via aliases during transition.
