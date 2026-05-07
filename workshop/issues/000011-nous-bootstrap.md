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

A single `make nous-bootstrap` orchestrates eight idempotent steps. Re-runs on a complete machine are no-ops:

1. **Xcode CLT** — `xcode-select --install` if missing; polls `xcode-select -p` every 30s for up to 20 min.
2. **Homebrew** — official one-liner if missing.
3. **Brewfile** — `brew bundle install --file=Brewfile`.
4. **GPG identity** — delegates to `scripts/identity.sh`. Skippable via `NOUS_BOOTSTRAP_SKIP_IDENTITY=1` for the test harness or for the recovery path (where a key gets imported separately first).
5. **Workflow tools** — delegates to `.openshell/Makefile`'s `bootstrap` (gh auth, openshell CLI, mutagen). Skippable via `NOUS_BOOTSTRAP_SKIP_OPENSHELL=1`.
6. **GitHub SSH key** — generates `~/.ssh/id_ed25519` if missing, registers via `gh ssh-key add` if not already on the user's GitHub account. Required for `gcrypt::ssh://...` brain remotes. Same skip-gate as step 5.
7. **fzf shell hook** — `fzf --update-rc`.
8. **Verify go on PATH.**

Out of scope: peer-repo cloning, dotfile management, shell-RC edits beyond fzf's installer.

### Brewfile contents

- **Core (nous-required):** go, gh, gnupg, pinentry-mac, git-remote-gcrypt, mutagen
- **Dev runtimes:** deno, lua@5.4, luarocks, node
- **Daily CLI:** ripgrep, fzf, bat, zoxide, tree, watch, glow
- **Casks:** claude, font-hack-nerd-font

Excluded by design (install on demand): ruby/chruby, postgresql, zig, elixir, ai tooling (gemini-cli/llama.cpp/whisper-cpp/agent-browser/cliproxyapi), content pipelines (pandoc/tectonic/sox/portaudio/poppler/graphviz/exiftool).

## Plan — M1 (script + manual VM smoke)

- [x] Author `nous/Brewfile`.
- [x] Author `nous/scripts/nous-bootstrap.sh` orchestrator.
- [x] Wire `make nous-bootstrap` into `Makefile.nous`.
- [x] Manual VM smoke-test (tart `tahoe-base`, drive bootstrap via SSH).
- [x] Poll-don't-die for Xcode CLT install (no manual re-run after dialog).
- [x] GitHub SSH-key flow (generate ed25519 if missing, register via `gh ssh-key add`).
- [x] Drop "clone ariadne" from next-steps (ariadne is private; nous vendors what brain needs).
- [ ] Atlas: brief entry under `atlas/` listing the four bootstrap entry points (`make identity`, `make nous-bootstrap`, `make new-brain`, `.openshell` `bootstrap`) and their relationships.

## Plan — M2 (automated test harness)

- [x] Generate throwaway GPG key fixture in `testdata/test-bootstrap/`.
- [x] Add `NOUS_BOOTSTRAP_SKIP_OPENSHELL` + `NOUS_BOOTSTRAP_SKIP_IDENTITY` env-var gates to `scripts/nous-bootstrap.sh`.
- [x] Author `scripts/nous-test-bootstrap.sh` (tart-driven VM end-to-end test).
- [x] Wire `make nous-test-bootstrap` into `Makefile.nous`.
- [x] Run the harness; verify `ROUND-TRIP-OK`; capture findings in `## Log`.

## Plan — M3 (fast iteration path via snapshot)

- [x] Author `scripts/nous-test-snapshot.sh` — produces `tahoe-bootstrapped` (stopped VM with bootstrap pre-applied).
- [x] Author `scripts/nous-test-roundtrip.sh` — clones snapshot, runs round-trip only.
- [x] Wire `make nous-test-snapshot` + `make nous-test-roundtrip` into `Makefile.nous`.
- [x] Add inline phase timing to both test scripts (`phase` helper + summary).
- [x] Run end-to-end; confirm 5x speedup target.

## Revisions

### 2026-05-07 — M2 (test harness) added
Manual VM smoke (M1) validated the bootstrap-up-to-GPG-prompt path but couldn't drive the full round-trip from a non-TTY SSH session (pinentry-mac needs a GUI). User asked for an automated harness instead of relying on manual re-runs from the VM console. M2 builds it using a throwaway test credential checked into `testdata/test-bootstrap/`, with a custom-pinentry shim inside the VM so decrypt is non-interactive. Test artifacts (key, passphrase) only ever encrypt test data inside disposable VMs — see `testdata/test-bootstrap/README.md`.

## Log

### 2026-05-07 — created
Carved out of `nous#10` once user observed that bootstrapping a brand-new Mac requires more than just the GPG key — the dev toolchain itself has to install.

### 2026-05-07 — M1 manual VM smoke
Drove `make nous-bootstrap` end-to-end on a fresh tart `tahoe-base` clone via SSH. Findings:
- ✓ Xcode CLT detected (cirruslabs base pre-installs it).
- ✓ Homebrew install one-liner works under `NONINTERACTIVE=1`.
- ✓ Brewfile applied — all 20 packages installed cleanly.
- ✓ `scripts/identity.sh` detected the sneakerneted key and skipped generation as designed.
- ✗ `identity.sh` errors on no-TTY when *generating* a fresh key (interactive prompt for name/email). Acceptable on a real fresh Mac (user is at the terminal) but blocks pure-SSH automation.
- ✗ `.openshell` `bootstrap` calls `gh auth login` interactively. Same shape: fine for a real user, blocks SSH automation. Drove M2's `NOUS_BOOTSTRAP_SKIP_OPENSHELL=1` gate.
- ✓ fzf shell hook installed cleanly into `.zshrc`.
- Bootstrap exit code 0 once both interactive blockers were handled.

The full gcrypt round-trip wasn't validated under M1 — pinentry-mac needs a GUI session that SSH doesn't provide. M2's custom pinentry shim closes that gap.

### 2026-05-07 — M3 fast path landed
After M2 passed, instrumented `nous-test-bootstrap.sh` with phase timing. Single-run breakdown (154s total):

```
   0s pre-flight + clean prior VM + tart clone
  10s VM boot (until IP)
   6s SSH ready + settle
   7s rsync nous + scp testdata
 125s nous-bootstrap.sh (Brewfile install dominates)  ← 81% of total
   1s GPG setup (pinentry + import + trust)
   1s gcrypt push (encrypt)
   1s gcrypt clone (decrypt)
```

Brewfile install is ~81% of total; the round-trip *we actually validate* runs in 3s. Built `make nous-test-snapshot` to produce a `tahoe-bootstrapped` VM with bootstrap pre-applied (one-time, ~3 min). `make nous-test-roundtrip` clones that snapshot and runs round-trip only — **28s end-to-end (5.5x speedup)**.

Two test targets now:
- `make nous-test-bootstrap` — full cold-start, ~2:34. Run when Brewfile or `nous-bootstrap.sh` changes.
- `make nous-test-roundtrip` — snapshot-based, ~28s. Run during round-trip / pinentry / gcrypt iteration.

`nous-test-roundtrip` warns if the snapshot directory's mtime is older than `Brewfile` or `scripts/nous-bootstrap.sh` — staleness signal without forcing a rebuild.

### 2026-05-07 — M2 harness green
`make nous-test-bootstrap` passes end-to-end on this Mac. Bootstrap layer + gcrypt push (encrypt) + fresh clone (decrypt) + content match all green. Throwaway test key from `testdata/test-bootstrap/` used end-to-end — `gpg: Good signature from "Nous Bootstrap Test" [ultimate]`, marker round-tripped intact.

Bugs caught and fixed during harness build (each one a real SSH-automation gotcha worth keeping in the script):
- **VM count limit:** macOS Virtualization.framework caps live VMs; `tart stop` doesn't always reap the host-side `tart run` process before the next `tart clone` requests a slot. Fix: belt-and-suspenders cleanup (`tart stop` + `pkill -f "tart run $VM_NAME"` + `sleep 1` + `tart delete`).
- **MaxAuthTries:** host's ssh-agent holds many keys; sshd defaults to `MaxAuthTries=6` and rejects the connection after ssh exhausts them before falling back to sshpass's password. Fix: force `PreferredAuthentications=password` + `PubkeyAuthentication=no`.
- **Heredoc-stdin consumption:** the Homebrew install script (or downstream `bash -c`) consumes stdin; running it inside an SSH `bash -s` heredoc swallows the rest of the heredoc, leaving the round-trip section unrun and bash exiting 0 silently. Fix: `< /dev/null` on the bootstrap call.
- **Just-booted sshd flake:** even after a successful login, the next connection occasionally rejects the password. Fix: 5s settle + 3-attempt retries on rsync/scp.

All four fixes are now in `scripts/nous-test-bootstrap.sh` with comments documenting the *why*.

Test runtime: ~4 min from invocation to teardown (Homebrew install + Brewfile fetch dominates). Acceptable as an iterate-and-fix harness; not optimized for CI cadence.
