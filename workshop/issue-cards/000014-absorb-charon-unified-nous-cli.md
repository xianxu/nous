---
id: '000014'
status: done
created: 2026-05-08
updated: 2026-05-10
estimate_hours: 16
actual_hours: 32.11
---

# Absorb charon — unified `nous` CLI + TUI

## Problem

nous-substrate operations are split across two repos and multiple binaries:

- **`charon`** repo: AI-credential proxy (`charon serve`), provider/OAuth management (`charon auth`), instructions (`charon instructions`), manifest, plus a separate `charon-security` macOS menubar app, and for host security practice check.
- **`nous` repo**: workflow conventions, the `brain-sync` Go daemon, brain provisioning bash scripts (`new-brain.sh`, `cloneto.sh`, `moveto.sh`), the `/nous-resolve` skill. and we currently use bunch of make targets for manual operation, with some workflow missing. 

Both serve nous (the operator's AI-coding tool); both have daemon + TUI shapes + other command line interface; both manage credentials (charon: API tokens, OAuth; brain: GPG keys); both want gpg-agent lifecycle management (`charon#21` literally about gpg-agent; brain's gcrypt encrypt/decrypt leans on it); both use cobra + bubbletea + lipgloss; both ship as launchd services. There's real overlap, and parallel infrastructure means double-built TUI patterns + service plumbing + credential abstractions.

User's directive: merge them. charon's repo gets archived. The combined surface lives at `nous/cmd/nous/` (one binary), with subcommands that emerge from use cases rather than from translating the existing tools.

This supersedes `nous#13` (brain CLI unification, narrower-in-scope).
