---
name: nous-tools
description: "Use when invoking a nous-provided tool (cmd/<name> — e.g. cmd/gmail for Gmail access) OR when writing a new tool whose Go code calls OAuth-protected APIs. Tool users invoke `cmd/<name>` directly; tool authors need the embedded proxy protocol via `nous instructions`."
---

# Nous Tools

Two audiences. Find yours first.

## Audience A — invoking an existing tool

You want to do a task that an existing `cmd/<name>` tool already handles (search Gmail, fetch from Drive, etc.). You do not need to know about the proxy or OAuth — the tool handles all of that internally.

**Discover what's available:**

```bash
ls cmd/*/SKILL.md
```

Read the tool's `SKILL.md` for commands, flags, and output. Then invoke:

```bash
nous run -- go run ./cmd/<name> <subcommand> [flags] [args]
```

The `nous run --` prefix starts the credential proxy on a local port and runs your command with the proxy configured. The proxy injects OAuth tokens / API keys based on the tool's outbound requests; your tool's code never sees credentials.

For built binaries (after `make build`):

```bash
nous run -- ./bin/<name> <subcommand> [flags] [args]
```

## Audience B — writing or debugging a new tool

You're adding a `cmd/<name>` (or modifying one) where the Go code makes outbound HTTP requests to an OAuth-protected API. Now you need the proxy's protocol — request headers (`X-Charon-Account`, `X-Charon-Scope`), per-provider URL conventions, error-code semantics (407, BILLING_DISABLED, …).

**Run this first when you start the work:**

```bash
nous instructions
```

That prints the canonical protocol guide embedded in the installed `nous` binary — always in sync with the version on the current machine. Follow what it says; do not rely on cached knowledge of the protocol.

**Re-read when:**

- Adding a tool that calls a previously-untouched API.
- The proxy returns unexpected status codes (especially 407 / 403).
- After upgrading nous — the embedded guide may have new sections.

**If `nous instructions` is missing or errors:** the installed `nous` binary is too old or absent. Reinstall via `make nous-bootstrap` from the nous repo. Do not guess at the protocol from memory.

## Conventions across all tools

- **Flags before positional arguments** (Go convention): `--account user@gmail.com "query"`, not `"query" --account ...`
- **JSON output** to stdout — structured for programmatic use.
- **Build**: `make build` compiles all `cmd/*/main.go` to `cmd/<name>/bin/<name>` (with `bin/<name>` symlinks). The signed nous binary itself is built via `make nous-build`; it has a `.skip-make-build` sentinel so `make build` leaves it alone.
- **Test**: `make test` runs all Go tests.

## Two different command trees, don't confuse them

There are two distinct surfaces — discover each via its own help command:

1. **The `nous` binary itself** — provider auth, brain management, identity, service control. **Run `nous --help`** for the verb tree. Common subcommand groups: `nous provider …` (AI provider config + account list), `nous brain …` (TUI), `nous identity …`, `nous service …`, `nous instructions`, `nous run`. **Do not guess top-level verbs** (e.g. `nous accounts` is wrong; the right form is `nous provider accounts` or similar inside the `provider` subcommand).

2. **The `cmd/<name>/` tool tree** — operator-built Go binaries (gmail, oneshot, etc.). Each has its own `cmd/<name>/SKILL.md`. Invoked via `nous run -- go run ./cmd/<name>` or `nous run -- ./bin/<name>`.

When you need to do something that nous's binary handles (manage providers, list accounts, configure auth), use tree 1. When you need to do something a `cmd/<name>` tool handles (search Gmail, fetch from Drive), use tree 2.

## When NOT to invoke this skill

- For tasks that don't involve nous's binary or a `cmd/<name>` tool (general code work, doc edits, etc.) — Claude can just write the code directly.
- For low-level proxy debugging when you can read the source — `lib/charon/` in the nous repo has the proxy implementation.
