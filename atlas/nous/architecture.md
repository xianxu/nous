# Architecture

## What is Nous

Nous is a personal AI extension — a template repo where you and a coding agent collaborate. It provides tools to access your data (email, calendar, etc.) and the accumulated context that makes AI increasingly useful over time.

## Layer System

Nous is built on two layers, each owning a directory:

```
construct/    ← ariadne layer (agentic development: issues, plans, skills, workflow)
nous/         ← nous layer (Go tools, plugins, setup for downstream repos)
```

**Ariadne** (private) provides the development workflow — issue tracking, plan management, skills, Claude Code settings. Vendored into nous so users don't need ariadne access.

**Nous** provides the tool infrastructure — Go libraries, CLI binaries, Charon integration, and a plugin system.

Downstream repos consume nous via a single command — the canonical setup.sh, vendored from ariadne and walking both ariadne's and nous's manifests in one invocation (see ariadne#32 for the unified replication model):
```bash
../nous/construct/setup.sh            # symlink everything (default)
../nous/construct/setup.sh --vendor   # copy everything (for repos that can't sibling-link)
../nous/construct/setup.sh --yes      # skip confirmations (non-interactive)
```

## Repo Structure

```
cmd/                  # Go binaries — each is also an agent skill
  gmail/              # Gmail search CLI + SKILL.md
  nous/.skip-make-build  # opt-out sentinel: signed/notarized binaries don't auto-build
lib/                  # Reusable Go libraries
  gmail/              # Gmail API client via Charon proxy
construct/            # Substrate management (both ariadne-vendored + nous-own)
  setup.sh            # Canonical setup script (symlinked from ariadne)
  base.manifest       # Nous's contributions (skills, Makefile.nous, plugins)
  skills/             # Nous-owned Claude skills (nous-tools, charon, nous-resolve)
  scripts/            # Ariadne-vendored helper scripts
  local/              # Ariadne-vendored skill dir
atlas/                # This map
workshop/             # Issues, plans, history, lessons
life/                 # Personal data (scaffold for downstream repos)
```

## Convention: cmd/ = skill

Each `cmd/<name>/` directory is a Go binary and an agent skill:
- `main.go` — the binary
- `SKILL.md` — how agents invoke it

The `nous-tools` meta-skill (in `construct/skills/`) tells Claude to discover tools by reading `cmd/*/SKILL.md`. No per-tool registration needed.

## Go Tooling

- `go.mod` at repo root (`github.com/xianxu/nous`)
- `make nous-build` — compiles all binaries to `cmd/<name>/bin/<name>` (with `bin/<name>` symlinks). Pure go build; no codesign. `make nous-sign` adds Developer ID signing to `bin/nous` on top; `make nous-notarize` adds Apple notary submission on top of that. Iterate at the level you need; the notary roundtrip stays off the daily path.
- `make test` — runs all Go tests
- `make clean` — removes build artifacts
- All tools run through Charon proxy for credential isolation

## Plugin System (historical → folded into base.manifest)

The pre-2026-05-19 plugin system used per-plugin manifests in `nous/plugins/`.
That mechanism was folded into `construct/base.manifest` as part of ariadne#32:
plugin-shaped contributions (gmail, oneshot) became inline entries in nous's
single base.manifest. Adding new "plugin-shaped" contributions = appending
symlink lines.

The setup script's behavior (modes, idempotency, confirmation on mode change)
is now provided by the canonical `construct/setup.sh` vendored from ariadne.
Mode recorded in `.ariadne-mode` (unified marker filename across layers).
Switching modes requires confirmation.
