---
id: 000018
status: working
deps: [nous#14]
created: 2026-05-10
updated: 2026-05-10
estimate_hours: 1
---

# `nous brain resolve` — wire the mechanical conflict-find surface

## Problem

`cmd/nous/brain_misc.go:newBrainResolveCmd` is stubbed — returns
"not yet wired pending lib/brainsync surface refactor; tracked as
nous#5 follow-up." The `/nous-resolve` Claude Code skill bypasses
it and calls `lib/brainsync` directly, which works fine.

Two reasons to wire it now:

1. **Agent-facing surface parity.** `nous brain` cluster help
   advertises `resolve` as a subcommand. Agents reading
   `nous brain resolve --help` see a real verb, but invoking it
   today returns an error. That's UX debt.
2. **Symmetry with M5a's read-only conflict surface.** `LoadStatus`
   already walks conflict files for the TUI drill-in. Exposing the
   same data as a scriptable `nous brain resolve <path>` lets the
   skill (or any other automation) discover conflicts via a stable
   CLI shape instead of grepping `find` output.

The semantic merge stays in the skill. This issue is only about
the mechanical list-and-emit step.

## Done when

- `nous brain resolve <brain-path>` exits 0 with structured output
  (default: tabular; `--json` flag for agents). Each entry shows
  `<canonical> <conflict-file> <peer> <timestamp>` derived from the
  `<stem>.conflict-<peer>-<YYYYMMDDTHHMMSSZ>.<ext>` convention.
- New public `lib/brainsync.ConflictFiles(brainRoot string) ([]Conflict, error)`
  with a `Conflict` struct. Tests cover the parser against the
  three filename shapes (with ext, without ext, nested directory).
- `nous/skills/nous-resolve/find-conflicts.sh` updated to optionally
  prefer the new command (still falls back to its own `find` so the
  skill works in older repos without the new binary).
- go build + go test ./... green.

## Plan

### M1 — public lib + cobra wiring

- [ ] `lib/brainsync/conflicts.go`: public `ConflictFiles(brainRoot string) ([]Conflict, error)`
      with `type Conflict struct { Canonical, ConflictFile, Peer string; At time.Time }`.
      Walks the brain tree skipping `.git` and `.brain`, parses each
      matching filename, derives the canonical path.
- [ ] `lib/brainsync/conflicts_test.go`: parser tests + tempdir walk test.
- [ ] `cmd/nous/brain_misc.go`: replace `newBrainResolveCmd` stub.
      Tabular by default; `--json` flag for machine output.
- [ ] `nous/skills/nous-resolve/find-conflicts.sh`: prefer
      `nous brain resolve --json` when the binary is on PATH; keep
      the legacy `find` path as fallback.

## Log

