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

Downstream repos consume nous via a single command (matches `ariadne/construct/setup.sh`'s shape):
```bash
../nous/nous/setup.sh            # symlink everything from nous (default)
../nous/nous/setup.sh --vendor   # copy everything (for repos that can't sibling-link)
../nous/nous/setup.sh --yes      # skip confirmations (non-interactive)
```

## Repo Structure

```
cmd/                  # Go binaries — each is also an agent skill
  gmail/              # Gmail search CLI + SKILL.md
lib/                  # Reusable Go libraries
  gmail/              # Gmail API client via Charon proxy
nous/                 # Nous layer construct system
  setup.sh            # Bootstraps downstream repos
  nous.manifest       # Core files to install
  plugins/            # Per-plugin manifests (gmail.manifest, etc.)
  skills/             # Nous-owned Claude skills (nous-tools meta-skill)
construct/            # Ariadne layer (vendored)
atlas/                # This map
workshop/             # Issues, plans, history, lessons
life/                 # Personal data (scaffold for downstream repos)
```

## Convention: cmd/ = skill

Each `cmd/<name>/` directory is a Go binary and an agent skill:
- `main.go` — the binary
- `SKILL.md` — how agents invoke it

The `nous-tools` meta-skill (in `nous/skills/`) tells Claude to discover tools by reading `cmd/*/SKILL.md`. No per-tool registration needed.

## Go Tooling

- `go.mod` at repo root (`github.com/xianxu/nous`)
- `make nous-build` — compiles all binaries to `cmd/<name>/bin/<name>` (with `bin/<name>` symlinks). Pure go build; no codesign. `make nous-sign` adds Developer ID signing to `bin/nous` on top; `make nous-notarize` adds Apple notary submission on top of that. Iterate at the level you need; the notary roundtrip stays off the daily path.
- `make test` — runs all Go tests
- `make clean` — removes build artifacts
- All tools run through Charon proxy for credential isolation

## Plugin System

Plugins are defined by manifest files in `nous/plugins/` and applied
in bulk — every plugin manifest is processed on every setup run. Two
modes (matches `ariadne/construct/setup.sh`):

- **default (symlink)**: symlink everything into the target tree, track nous HEAD
- **`--vendor`**: copy files into the target so the consumer owns them
  (for public repos that can't depend on nous as a sibling clone)
- Re-run with no args: refresh in whatever mode was previously set

Mode recorded in `.nous-mode` (content: `symlink` or `vendor`).
Switching modes requires confirmation.

Historical note: pre-2026-05-19 the script had `--all` / `--add <plugin>`
/ `--rm <plugin>` for selective plugin management. That distinction
solved a problem operators didn't have (plugin set is small; everyone
wanted everything); folded into the simpler ariadne-shaped two-mode
design. Legacy `.nous-mode` values `all` and `selective` auto-migrate
to `symlink` / `vendor` on first run.
