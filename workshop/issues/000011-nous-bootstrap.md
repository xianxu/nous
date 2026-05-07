---
id: 000011
status: working
created: 2026-05-07
updated: 2026-05-07
---

# `make nous-bootstrap` — fresh-Mac dev toolchain installer

## Problem

A brand-new Mac with `git clone nous` should reach a working state via one command. Today it can't:

- `make identity` covers GPG only.
- `.openshell/Makefile`'s `bootstrap` covers gh + mutagen + openshell only.
- Go (required for `make build`), the daily CLI loadout (rg/fzf/bat/zoxide), and dev runtimes (node/deno/lua) are assumed-present with no automation.

This also blocks `nous#10` (second-machine bootstrap dry-run) from being a real cold-start test — that issue assumed the dev toolchain was already in place on the target machine.

## Spec

A single `make nous-bootstrap` orchestrates three layers, each idempotent (re-runs on a complete machine are no-ops):

1. **Substrate** — Xcode CLT (`xcode-select --install` if missing); Homebrew (official one-liner if missing); `brew bundle install --file=Brewfile`.
2. **Identity** — delegates to existing `scripts/identity.sh` (GPG keypair + gpg-agent.conf + iCloud Keychain import path).
3. **Workflow** — delegates to existing `.openshell` `bootstrap` (gh auth, openshell CLI, mutagen tap); `fzf` shell-hook installer; verify go env.

Out of scope: peer-repo cloning (ariadne, brain — handled separately by `make new-brain` and explicit clones), dotfile management, shell-RC edits beyond fzf's own installer.

### Brewfile contents

- **Core (nous-required):** go, gh, gnupg, pinentry-mac, git-remote-gcrypt, mutagen
- **Dev runtimes:** deno, lua@5.4, luarocks, node
- **Daily CLI:** ripgrep, fzf, bat, zoxide, tree, watch, glow
- **Casks:** claude, font-hack-nerd-font

Excluded by design (install on demand): ruby/chruby, postgresql, zig, elixir, ai tooling (gemini-cli/llama.cpp/whisper-cpp/agent-browser/cliproxyapi), content pipelines (pandoc/tectonic/sox/portaudio/poppler/graphviz/exiftool).

## Plan

- [ ] Author `nous/Brewfile`.
- [ ] Author `nous/scripts/nous-bootstrap.sh` orchestrator.
- [ ] Wire `make nous-bootstrap` into `Makefile.nous`.
- [ ] Smoke-test on this Mac (re-run; expect all-green / no installs).
- [ ] Cold-test on a VM as part of `nous#10`.
- [ ] Atlas: brief entry under `atlas/` listing the four bootstrap entry points (`make identity`, `make nous-bootstrap`, `make new-brain`, `.openshell` `bootstrap`) and their relationships.

## Log

### 2026-05-07 — created
Carved out of `nous#10` once user observed that bootstrapping a brand-new Mac requires more than just the GPG key — the dev toolchain itself has to install. `nous#10`'s VM dry-run will exercise this script as part of its cold-start.
