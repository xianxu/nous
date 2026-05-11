---
id: 000020
status: open
deps: [000016]
created: 2026-05-10
updated: 2026-05-10
estimate_hours: 2
---

# Retire standalone `charon` + `brain-sync` binaries

## Problem

After nous#16 M2, the runtime daemon is a single process: `nous serve`
runs the credential proxy and brain-sync watcher as goroutines under
one context (`cmd/nous/serve.go:1`). The launchd service
(`com.42shots.nous`) invokes only `nous serve`.

Yet `cmd/charon/` and `cmd/brain-sync/` still exist as standalone
binaries. `make nous-install` continues to build, sign, and install
all three:

```
Makefile.nous:139  scripts/sign.sh bin/nous
Makefile.nous:140  scripts/sign.sh bin/charon
Makefile.nous:141  scripts/sign.sh bin/brain-sync
Makefile.nous:144  for name in nous charon brain-sync; do install ... done
```

Cost of the residue:

1. **Three CDHashes for the keychain to track.** Each standalone
   binary has a unique designated requirement (ad-hoc DR is
   `cdhash H"…"`-only — verified 2026-05-10 against shipped bin/).
   ACLs written by one don't match the others, contributing to the
   prompt churn the operator hit during `make nous-install`.
2. **Signed-binary surface area.** Three signed executables in
   `$(NOUS_INSTALL_PREFIX)` with `charon`-namespace keychain access
   when only one (`nous`) should have it.
3. **Misleading mental model.** Operator can `charon arm` / `charon
   disarm` against a binary that doesn't run the daemon — those CLI
   verbs only make sense when routed through `nous`.

## Done when

- `cmd/charon/` and `cmd/brain-sync/` are deleted (or reduced to
  thin shims that `exec` into `nous <verb>` if any external scripts
  still call them — audit first).
- `Makefile.nous` `nous-install` target builds and signs only `nous`.
- `make nous-dev` still works (single foreground `nous serve`).
- `nous doctor` checks updated to reflect single-binary install
  (`cmd/nous/doctor.go:62-63` references `charonInstalled` and
  `brainSyncInstalled` — collapse to one check).
- `make nous-uninstall` doesn't try to remove binaries it never
  installed.
- README / atlas updated where the three-binary model was documented.

## Spec

Subagent-friendly audit pass first:

1. **What still calls `charon` and `brain-sync` directly?** Grep
   the whole tree (including AGENTS.md, atlas/, scripts/, docs/).
   Likely categories:
   - Operator-facing CLI verbs (`charon arm`, `brain-sync watch …`)
     — these should route through `nous` subcommands.
   - Test fixtures / integration tests that exec the standalone
     binary — fold into `nous`-invoking variants.
   - `make nous-dev` legacy paths.
2. **Decide: delete or alias?** If nothing external references
   them, delete `cmd/charon/main.go` and `cmd/brain-sync/main.go`.
   If anything user-facing depends on the binary name, leave a
   2-line `main.go` that just calls `os.Args[0] = "nous"; exec
   /usr/local/bin/nous <verb>` and signal-passes — but only if
   we can't migrate the callers.
3. **Strip the install plumbing.** Edit `Makefile.nous`:
   - Drop the `charon` + `brain-sync` lines from the build, sign,
     and install loops (lines 131-146).
   - Update the banner comments (lines 111-115) that still mention
     "signs nous + charon + brain-sync".
4. **Fix the misleading ad-hoc note** while editing the Makefile:
   lines 152-158 claim ad-hoc ACLs bind to `identifier
   "com.charon.cli"` only — they actually bind to `cdhash H"…"`
   per binary, which is the worse case. Update the note to be
   accurate.
5. **Doctor + status checks.** `cmd/nous/doctor.go:62-63` and
   `cmd/nous/audit.go` reference both legacy log files
   (`charon.log`, `brain-sync.log`) and check both legacy service
   labels. After nous#16 M2 these are written by `nous serve` to
   the same paths (compat), but verify.

## Plan

- [ ] M1: Audit external references to standalone `charon` and
  `brain-sync` binaries. Document findings in `## Log`.
- [ ] M2: Migrate any remaining callers to `nous <verb>` form, or
  decide to ship thin-shim binaries (with rationale in this issue).
- [ ] M3: Strip `cmd/charon/` + `cmd/brain-sync/` (or reduce to
  shims), update `Makefile.nous` install/uninstall/banner, fix
  the misleading ad-hoc ACL note, update `doctor.go` checks.
- [ ] M4: Verify `make nous-install` followed by `nous service
  status` works on a freshly-uninstalled machine. Confirm only
  `nous` is signed and installed.
- [ ] M5: Atlas update — anywhere the three-binary model is
  documented gets updated to single-binary.

## Log

Filed 2026-05-10. Surfaced from a debugging session about keychain
prompt churn during `make nous-install`. Three-binary signing
contributed to but did not solely cause the prompts — the bigger
cause is ad-hoc signing producing per-build CDHashes, addressed
separately by switching to Developer ID signing (operator's
existing cert). This issue is the cleanup; the Developer ID
switch is a config change, not a code change.
