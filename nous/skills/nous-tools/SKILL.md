---
name: nous-tools
description: "Use proactively when the user asks about email, data access, or anything that might be handled by a local tool. Discovers and invokes Go-based tools in cmd/<name>/."
---

# Nous Tools

This repo has Go-based tools in `cmd/` directories. Each tool has its own `SKILL.md` with detailed usage.

## How to discover tools

```bash
ls cmd/*/SKILL.md
```

Read the SKILL.md of any tool to understand its commands, flags, and output format.

## How to invoke tools

All tools that access external services run through the Charon proxy:

```bash
charon run -- go run ./cmd/<name> <subcommand> [flags] [args]
```

## Available tools

Check `cmd/*/SKILL.md` for the current list. Each `cmd/<name>/` directory is a self-contained tool with:
- `main.go` — the Go binary
- `SKILL.md` — usage documentation for this tool

## Key conventions

- **Flags before positional arguments** (Go convention): `--account user@gmail.com "query"`, not `"query" --account ...`
- **JSON output** to stdout — structured for programmatic use
- **Charon proxy** handles all authentication — tools never touch credentials
- **Build**: `make build` compiles all tools to `cmd/<name>/bin/<name>`
- **Test**: `make test` runs all Go tests
