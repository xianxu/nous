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

**Nous** provides the tool infrastructure — Go libraries, CLI binaries, Charon integration, and a plugin system for selective installation.

Downstream repos consume nous via a single command:
```bash
../nous/nous/setup.sh --all       # symlink everything, track HEAD
../nous/nous/setup.sh --add gmail  # vendor just the gmail plugin
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
- `make build` — compiles all binaries to `cmd/<name>/bin/<name>`
- `make test` — runs all Go tests
- `make clean` — removes build artifacts
- All tools run through Charon proxy for credential isolation

## Plugin System

Plugins are defined by manifest files in `nous/plugins/`:
- `--all` mode: symlink everything, track nous HEAD
- `--add <plugin>` mode: vendor selectively, user owns the files
- `--rm <plugin>`: remove a vendored plugin
- Re-run with no args: refresh in current mode
