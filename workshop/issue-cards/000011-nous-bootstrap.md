---
id: '000011'
status: done
created: 2026-05-07
updated: 2026-05-07
actual_hours: 4.3
---

# `make nous-bootstrap` — fresh-Mac dev toolchain installer

## Problem

A brand-new Mac with `git clone nous` should reach a working state via one command. Today it can't:

- `make identity` covers GPG only.
- `.openshell/Makefile`'s `bootstrap` covers gh + mutagen + openshell only.
- Go (required for `make build`), the daily CLI loadout (rg/fzf/bat/zoxide), and dev runtimes (node/deno/lua) are assumed-present with no automation.

This also blocks `nous#10` (second-machine bootstrap dry-run) from being a real cold-start test — that issue assumed the dev toolchain was already in place on the target machine.
