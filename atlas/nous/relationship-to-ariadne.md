# Nous vs ariadne — what nous adds on top

This doc answers a specific question someone reading nous for the
first time asks within their first hour: *"how much of this is
nous, and how much is the ariadne base layer?"* The honest
answer: **nous is ariadne plus a ~32k-line Go runtime**, broken
into roughly five chunks. This atlas quantifies the chunks and
maps them to the operator's mental model.

For the higher-level "what is nous" framing, see
[`architecture.md`](architecture.md) (which covers the layer
system + bootstrap targets). For the lib/* internals, see
[`lib-layout.md`](lib-layout.md). This doc sits between them.

## The split, in one diagram

```
                       nous repo
                       ─────────
       ariadne base layer (vendored via `make refresh`)
       ──────────────────────────────────────────────────
       • workflow conventions (atlas/workflow/, AGENTS.md,
         Makefile.workflow)
       • skill system (construct/, mostly upstream:
         superpowers, brainstorming, etc.)
       • workflow scripts (issue-sync.sh, parallel-checks.sh,
         pre-merge-checks.sh, close-issue.py, lib.sh)

                       +

       nous-specific additions
       ────────────────────────
       ┌─ CLI dispatch          cmd/nous              ~4.6k loc
       ├─ Charon machinery      lib/{provider,         ~12k loc
       │                        security,charoncli,
       │                        codesign,service}
       │                        + cmd/charon
       ├─ Brain machinery       lib/{brain,brainsync,  ~6.5k loc
       │                        identity,gh,workspace}
       │                        + cmd/brain-sync
       ├─ TUI layer             lib/tui/*              ~9k loc
       ├─ Demos                 cmd/{gmail,oneshot},   ~1.3k loc
       │                        lib/{gmail,agent}
       └─ Glue                  Makefile.nous (435 lines),
                                scripts/{identity,new-brain,
                                nous-bootstrap,sign,...}.sh,
                                nous/ (install scaffolding),
                                atlas/{nous,charon}/
```

Total Go (non-test): **~32k lines**. None of those lines are
ariadne; ariadne contributes zero Go.

## What's in each chunk

### CLI dispatch (cmd/nous, ~4.6k)

Every `nous foo bar` subcommand: cobra command builders, flag
plumbing, output formatting. Substantial because the surface
area is wide (brain new / clone / list / invite / join /
recipient add|remove|verify; identity init / list / import /
export; service install / uninstall / start / stop / status /
doctor / audit / reinstall; security review; doctor; resolve).

### Charon machinery (~12k, biggest chunk)

The credential proxy + security audit substrate:
- `lib/provider/*` — provider abstraction, OAuth flows, vault
  (Keychain), proxy runtime, per-provider catalogs (Anthropic,
  OpenAI, GCP, Gmail).
- `lib/security/*` — the audit + arm/disarm state machine that
  gates agentic access; reads charon's runtime + apps + sudo.
- `lib/charoncli/*` — operator-facing TUI for charon (scope
  review, arm-state, log audit).
- `lib/codesign/*`, `lib/service/*` — codesign verification at
  runtime + launchd plist management.
- `cmd/charon/` — the legacy standalone charon binary (now
  retired; `nous serve` is the unified daemon, but cmd/charon
  remains for migration purposes).

This is "make the agent's credentials handle-able by a sane
human" — gates, audits, scoped tokens, opt-in arming.

### Brain machinery (~6.5k)

The shared-brain machinery proper:
- `lib/brain/*` — manifest read/write, gcrypt-participants
  sync (nous#24), auto-admit (nous#26), verified.yaml + drift
  detection (nous#26 M6), filestore abstraction.
- `lib/brainsync/*` — the daemon's pull/push/watch loop;
  conflict file detection; resolve verb's plumbing.
- `lib/identity/*` — GPG key lifecycle (init, list, import,
  export, primary).
- `lib/gh/*` — thin wrappers over `gh api` (nous#26).
- `lib/workspace/*` — workspace root resolution (the "nous
  and brains are siblings" convention encoded in code).

Smaller than charon, but more conceptually load-bearing for
the human↔AI collaboration vision — the brain is what
accumulates state across sessions.

### TUI (~9k, the surprise)

`lib/tui/*` is bigger than brain + brainsync combined. Each
operator-facing flow has a bubbletea sub-model: brain list /
detail / new / invite-collaborator / recipient-add /
recipient-remove / conflict-preview / scopes / admin-mint /
catalog-paste / gcp-setup / admin-key-detail / admin-key-paste.

A lot of the "feels like a product" comes from this layer,
not from the daemons. Worth knowing because: when you read
`cmd/nous/brain.go` and see TTY-dispatch to `runBrainTUI()`,
that's the entrypoint for a 2k-line sub-tree.

### Demos (~1.3k)

- `cmd/gmail/` (93 lines) — a script-shaped Gmail tool that
  reads / sends / drafts via the OAuth-proxied Gmail API.
  Intentionally minimal — illustrates how a coding agent
  builds a feature on top of nous's provider + identity
  infrastructure.
- `cmd/oneshot/` (151 lines) — a single-prompt LLM tool
  routed through the charon proxy.
- `lib/gmail/`, `lib/agent/` — supporting helpers.

These aren't core nous functionality — they're scaffolding
examples. The operator clones nous, sees these as small
working tools, and learns how to build their own by reading
+ extending. Part of the "co-designed workspace" pitch.

### Glue

- `Makefile.nous` (435 lines) — every `make nous-*` target:
  `nous-bootstrap`, `nous-build`, `nous-install`,
  `nous-sign`, `nous-notarize`, `nous-test-*`, and the
  test-loop targets.
- `scripts/identity.sh`, `nous-bootstrap.sh`,
  `new-brain.sh`, `sign.sh`, `cloneto.sh`, `moveto.sh`,
  `nous-test-{bootstrap,roundtrip,snapshot}.sh` — non-Go
  glue, mostly shell scripts orchestrating gpg/git/gh calls
  that would be awkward to write in Go.
- `nous/setup.sh`, `nous/manifest.md`, `nous/skills/`,
  `nous/plugins/` — the install scaffolding consumed by
  *downstream* repos (e.g., a personal repo that does
  `../nous/nous/setup.sh` to inherit the toolchain).

## The bootstrap mental model

The operator workflow nous targets:

1. `git clone .../nous` — single starting point, sources +
   scaffolding.
2. `make bootstrap` — installs Homebrew deps, generates
   GPG + SSH keys, sets up gpg-agent / pinentry.
3. `make nous-build` — builds `bin/nous`.
4. `nous service install` — launchd-managed `nous serve`
   (proxy + brain-sync as goroutines under one process).
5. Workspace = nous's parent dir. Brains live as siblings of
   nous, not children. `lib/workspace.Root()` formalizes this.
6. `git pull nous` upgrades the tooling without disturbing
   brain state — because brain manifests live in sibling
   directories, not under nous/.

Steps 1–4 are nous-specific. Step 5 is the architectural
convention (encoded in code). Step 6 is the upgrade promise
that makes the dual-role of nous (tool + scaffolding) work
without painful version-migration drama.

## Where to read next

- [`architecture.md`](architecture.md) — the higher-level
  "what is nous" story, including the construct/ ariadne
  layer setup.
- [`lib-layout.md`](lib-layout.md) — detailed per-package
  catalog of lib/*.
- [`cli.md`](cli.md) — `nous` command surface.
- [`bootstrap-entry-points.md`](bootstrap-entry-points.md) —
  the bootstrap targets in detail (steps 2–4 above).
- [`recipient-onboarding.md`](recipient-onboarding.md) —
  the nous#26 collaborator flow that drove most of brain's
  recent growth.

## When this doc gets stale

- Significant addition/removal of a chunk (e.g., charon ships
  as its own repo, or a new domain layer joins). The LoC
  numbers + diagram need updating.
- The dual-role of nous (tool + scaffolding) is split into
  two repos. Then the framing in this doc no longer applies.
- Ariadne starts contributing Go code. Today it contributes
  zero; if that changes, "ariadne is the workflow layer, nous
  is the runtime" no longer holds cleanly.

LoC numbers are approximate (snapshot from the time this doc
was written) — refresh with:

```sh
find . -name '*.go' ! -name '*_test.go' ! -path '*/.git/*' | xargs wc -l | tail -1
```

…or per chunk via the `lib/*` and `cmd/*` paths listed in the
diagram.
