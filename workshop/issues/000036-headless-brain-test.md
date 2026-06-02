---
id: 000036
status: working
deps: [ariadne#59]
github_issue:
created: 2026-06-01
updated: 2026-06-01
estimate_hours: 4
---

# scriptable headless brain testing in tart VM

## Problem

Testing brain operations (e.g. admitting yingtest42 to brain-family, then
having that throwaway identity clone/edit/push) in a tart VM currently only
works via `make tart-gui`: a headless VM has no window server, so
`pinentry-mac` can't draw its passphrase dialog and every GPG/gcrypt op
(decrypt, re-key, sign) fails. `tart-gui` works but the Screen Sharing dance
is cumbersome.

We want the headless `make tart` (SSH) VM to be a first-class brain test
environment, fully scriptable from the host — no GUI, no passphrase typing.

Two blockers:
1. The `make tart` VM is never made GPG-ready (`tart-vm-setup.sh` does
   oh-my-zsh + symlinks only).
2. The recipient/identity ceremony commands are TTY-gated
   (`term.IsTerminal(os.Stdin)` in `cmd/nous/brain_recipient.go`,
   `cmd/nous/identity.go`) — they refuse to run non-interactively, which is
   exactly the scripted case.

## Spec

The VM only ever holds **throwaway** identities (yingtest42 = `Ying Test`
`…DD4F88C4`; emmatest42 = `Emma Test` `…E968B484`) — both `xianxu+…@gmail.com`
test keys created inside tart VMs; the operator's real key
(`0ECF…C2F0`) never leaves the host. So baking a fixed test passphrase into
the VM is safe by construction.

**Delivery: boot from nous only.** The VM is a generic brain-client launched
via `make tart` from nous. This keeps the hook in one place (nous) — no
brain-repo changes, no manifest vendoring. The target brain enters the VM via
`nous brain clone gcrypt::ssh://…/brain-family.git` (faithful clone path) or
`make tart SYNC=../brain-family` (COW mount for quick edit/push).

**Unattended GPG** reuses the proven fake-pinentry shim from
`scripts/nous-test-bootstrap.sh:206-221`: a pinentry program wired into
`gpg-agent.conf` that always returns the test passphrase, so even gcrypt's
internal gpg calls run non-interactively. Written to a **persistent** path
(not `/tmp`, which the cold-reboot wipes) so it survives reboots and supports
manual one-time invocation too.

**Scriptable ceremony.** A `--verified-last8 <8hex>` flag on
`nous identity import` + `nous brain recipient add`: when set, it satisfies the
verify-fingerprint ceremony in-process (compare to the key's actual last-8;
mismatch → error, same as failing the prompt) and lifts the TTY gate. When
unset, behavior is byte-for-byte unchanged (interactive, TTY-gated). DRY via a
`verifyLast8` wrapper around the existing shared `promptVerify` helper.
`nous identity init` gains a non-interactive path (name/email via flags or
`IDENTITY_*` env; passphrase via the shim).

**Security note (also → threat-model `## Revisions`):** the TTY-only gate was
a deliberate threat-model decision (admission is a delegation boundary).
`--verified-last8` deliberately shifts the OOB-verification responsibility from
"a human at a TTY" to "the caller supplied the correct last-8" — a real
loosening, intended for scripted/test contexts. The OOB check must happen
*before* the script runs. It is not a blanket `--force`: the correct last-8 is
still required.

## Done when

- `make tart` from nous → run the GPG hook → all GPG/gcrypt ops in the headless
  VM are non-interactive (no GUI, no passphrase prompt).
- `nous identity import … --verified-last8 X` and `nous brain recipient add …
  --verified-last8 X` run with no TTY; wrong last-8 errors; absent flag keeps
  the strict interactive ceremony unchanged (unit-tested).
- `nous identity init` runs non-interactively (flags/env + shim passphrase).
- A host-driven e2e script admits a throwaway identity to a test brain and
  round-trips an edit, driven entirely over `ssh admin@$(tart ip nous-test)`.
- Threat-model `## Revisions` records the `--verified-last8` delegation.
- Outcome logged to nous#12 as the repeatable dogfood dry-run.

## Plan

### M1 — VM GPG-unattended (depends ariadne#59)
- [ ] `scripts/brain-vm-setup.sh` — idempotent: persistent pinentry shim +
  `gpg-agent.conf` + `GPG_TTY`; passphrase from
  `testdata/test-bootstrap/test-key.passphrase`.
- [ ] `.tart/vm-hooks.d/00-gpg-setup.sh` — thin wrapper `exec`ing the script
  (consumes ariadne#59's run-parts convention).
- [ ] Verify on a real `make tart` from nous: GPG decrypt/sign work with no
  prompt.

### M2 — scriptable verify-fingerprint ceremony
- [ ] `verifyLast8` wrapper around `promptVerify`; `--verified-last8` flag on
  `nous identity import` + `nous brain recipient add`; lift TTY gate only when
  the flag is set.
- [ ] Unit tests: match / mismatch / empty-falls-back-to-prompt; gate-lift only
  with flag.

### M3 — non-interactive `nous identity init`
- [ ] `--name/--email/--expiry` (or `IDENTITY_*` env) path; lift TTY gate when
  inputs present; passphrase via shim. Verify keygen unattended in the VM.

### M4 — e2e + docs
- [x] Threat-model `## Revisions` note (brain `2a3d82b`); `atlas/` update
  (nous `e3ade8b` — e2e-integration-testing.md).
- [ ] `scripts/brain-vm-e2e.sh` — self-contained, GitHub-free CLI-level e2e
  against a `file://` bare gcrypt remote with two throwaway per-`GNUPGHOME`
  identities: `identity init` (non-interactive) → `export` → `import
  --verified-last8` → `brain recipient add --verified-last8` → gcrypt clone
  (unattended via shim) → edit → push → pull → assert the peer's edit
  decrypts. Durable regression net; runs in CI/VM without GitHub.
- [ ] Live VM smoke (the runbook below) — boot from nous, confirm the
  `00-gpg-setup.sh` hook fires + `identity init` runs unattended. The deeper
  VM clone→push round-trip (needs the VM identity's GitHub access) is
  **nous#12**'s onboarding scope, not this issue.
- [ ] Log to brain `data/project/shared-brain.md` (nous#12) as the dry-run.

## Test plan

M2/M3 carry colocated unit tests (PURE comparison + flag plumbing). M1 is
verified by `scripts/brain-vm-e2e.sh` (the shim drives gpg/gcrypt unattended)
+ the live VM smoke. M4's e2e is the CLI-level integration net — process-level
(gpg-agent, gcrypt) so it lives in a script, not a `go test`.

**Live VM runbook** (boot from nous on this branch):
- VM shell (after `make tart`): `nous identity init --name "Ying Test" --email
  "xianxu+yingtest@gmail.com"` → no prompt, no TTY error (the dev-aliases
  `nous` function builds-on-demand; no manual build needed) → `nous identity
  export > ~/ying.pub`.
- Host: `scp admin@$(tart ip nous-test):ying.pub /tmp/ying.pub`, then
  `nous brain recipient add ~/workspace/brain-shared-test /tmp/ying.pub
  --verified-last8 <last8> </dev/null`.
- If building a binary explicitly, use `~/repo/bin` (the dev slot, first on
  PATH) — NOT `~/.local/bin` (reserved for `make nous-install`'s signed prod
  binary).

## Log

### 2026-06-01

Created. Carved from the shared-brain dogfood (nous#12) — this is the
infrastructure that makes the two-machine dry-run repeatable and scriptable
rather than a manual GUI session. Depends on ariadne#59 (generic vm-hooks.d
convention) for the zero-touch hook. Design decisions (boot-from-nous,
fake-pinentry shim, `--verified-last8`) settled in the originating brainstorm.
