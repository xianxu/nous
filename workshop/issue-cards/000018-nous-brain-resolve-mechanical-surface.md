---
id: '000018'
status: done
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
