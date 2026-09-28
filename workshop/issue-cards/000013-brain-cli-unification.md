---
id: '000013'
status: wontfix
created: 2026-05-08
updated: 2026-05-08
estimate_hours: 10
---

# `brain` — unified CLI + TUI for managing brains

## Problem

Brain operations are scattered across multiple shell scripts and a single-purpose Go binary:

- `make new-brain` (bash, single-recipient, hand-edit `.brain/config.md` to admit a peer)
- `make cloneto`, `make moveto` (bash)
- `brain-sync` (Go, watches shared brains, runs as launchd service)
- No tool for `recipient add/remove/list`, no safeguards, no public-key registry view, no fingerprint-verification ceremony

Symptoms surfaced trying to provision `brain-shared-family` for `nous#12 M1`:
- The user couldn't recall the multi-recipient invocation (because there isn't one)
- The "admit a peer" flow is six manual steps including hand-editing `.brain/config.md` and `gcrypt-participants` config
- No safeguard against removing the operator's own fingerprint, dropping the last recipient, or accepting a typo'd pubkey
- Documentation drift between SKILL/atlas/threat-model on the recipient layout vs what the scripts actually produce

The user's directive: "no one's going to remember what command to use. The ergonomics need to be there." Match the **charon pattern** — a single `brain` Go binary with cobra subcommands and a bubbletea TUI shell, replacing the bash + makefile scatter.
