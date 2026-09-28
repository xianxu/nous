---
id: 000018
status: done
deps: [nous#14]
created: 2026-05-10
updated: 2026-05-10
estimate_hours: 1
actual_hours: 0.7
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

- [x] `lib/brainsync/conflicts.go`: public `ConflictFiles(brainRoot string) ([]Conflict, error)`
      with `type Conflict struct { Canonical, ConflictFile, Peer string; At time.Time }`.
      Walks the brain tree skipping `.git` and `.brain`, parses each
      matching filename, derives the canonical path.
- [x] `lib/brainsync/conflicts_test.go`: parser tests + tempdir walk test.
- [x] `cmd/nous/brain_misc.go`: replace `newBrainResolveCmd` stub.
      Tabular by default; `--json` flag for machine output.
- [x] `nous/skills/nous-resolve/find-conflicts.sh`: prefer
      `nous brain resolve --json` when the binary is on PATH; keep
      the legacy `find` path as fallback.

## Log


- 2026-05-10: closed — lib/brainsync.ConflictFiles + tests green; nous brain resolve text + --json modes verified against operator's brain; bash test-synthetic.sh in nous/skills/nous-resolve green end-to-end (find-conflicts via the new Go binary path + preserve + git ops + undo). FORCE=1: v3 attribution would mis-bucket #18 work (the script's transcript snapshot predates this session's commits; manual estimate 0.7h)
- 2026-05-10: closed. `lib/brainsync.ConflictFiles(brainRoot)` ships with `Conflict{Canonical, ConflictFile, Peer, At}` (relative paths, full UTC time); walks the brain skipping `.git`/`.brain`; conflictFileRE anchored against the brainsync convention with three filename shapes covered by `conflicts_test.go` (with-ext, extensionless, near-miss/timestamp-malformed). `cmd/nous/brain_misc.go:runBrainResolve` replaces the stub: refuses non-brain paths, emits tab-separated absolute-path lines by default (matching the legacy `find-conflicts.sh` contract so downstream skill prose stays unchanged), `--json` flag emits structured form with relative paths + RFC3339 timestamps for agent consumers. `nous/skills/nous-resolve/find-conflicts.sh` now prefers `nous brain resolve` via `command -v nous`, falls back to the legacy `find` + python parser when the binary is missing (older installs, CI sandboxes). Output shape identical either way. Verification: `bash test-synthetic.sh` green end-to-end (find-conflicts + preserve.py + git ops + undo); `go test ./...` green; smoke against operator's real brain shows empty text + `[]` JSON (clean brain). actuals 0.4h vs 1h est — well below because lib/brain's status.go already had a sibling regex that informed the lib/brainsync version.
