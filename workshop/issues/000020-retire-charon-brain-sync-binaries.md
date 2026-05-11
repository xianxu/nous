---
id: 000020
status: working
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

## Verb migration map (decided 2026-05-10)

`charon` verbs not yet on `nous` get mounted as follows. Operator
retrains; no compat shim or alias (single operator, acceptable cost).

| `charon …`       | `nous …`              | rationale                                          |
|------------------|-----------------------|----------------------------------------------------|
| `run <cmd>`      | `run <cmd>`           | top-level: heaviest-use verb (proxy env wrapper)   |
| `status`         | `status` (aggregated) | top-level: aggregate proxy + sync daemon health    |
| `vault set/del`  | `vault set/del`       | top-level: keychain abstraction, not per-provider  |
| `arm` / `disarm` | `arm` / `disarm`      | top-level: global session gate, not per-provider   |
| `gcp …`          | `provider gcp …`      | cluster: provider-specific auth helper             |
| `who`            | `provider who`        | cluster: provider introspection                    |
| `stats`          | `provider stats`      | cluster: per-provider request stats                |
| `scopes`         | `provider scopes`     | cluster: per-provider OAuth scope view             |

Already on `nous` (no work): `serve`, `provider` (= AuthCmd),
`provider manifest`, `service install/...`, `instructions`,
`manifest`.

For `nous status`, M2's job is **aggregate proxy + sync daemon
state** (both run in the same `nous serve` process). Broader
status (brain, identity, etc.) is out of scope for this issue —
file a follow-up issue when wanted.

## Plan

- [x] M1: Audit external references to standalone `charon` and
  `brain-sync` binaries. Findings in `## Log`.
- [x] M2: Mounted the 9 missing verbs on `nous` per the table.
  `cmd/nous/main.go`: top-level `run`, `arm`, `disarm`, `vault`,
  `status`. `providerCmd()`: `gcp`, `who`, `stats`, `scopes`.
  `cmd/nous/status.go` is new and aggregates service + proxy +
  session state. Exported `charoncli.RenderSessionStatus` so the
  aggregator can reuse the /session/status probe without
  duplication.
- [x] M3: Deleted `cmd/charon/` + `cmd/brain-sync/` +
  `scripts/test-brain-sync.sh`. Stripped charon/brain-sync from
  `Makefile.nous` build/sign/install loops + uninstall reference.
  Rewrote the ad-hoc ACL note (was claiming "binds to identifier
  only" — codesign actually emits a `cdhash H"..."`-only DR for
  ad-hoc, NOT identifier-only; the rewritten note says so).
  `cmd/nous/doctor.go`: 4 legacy checks → 2 unified ones.
  `cmd/nous/audit.go`: rewrote to tail `~/Library/Logs/nous.log`
  (the unified service's actual log path) instead of the two
  legacy paths nothing writes to anymore. `cmd/nous/service.go`:
  removed orphan `resolveSiblingBinary`, refreshed docstrings.
  `cmd/nous/instructions.go`: refreshed the service-cluster docs.
  `lib/security/check_charon_binary.go` + `check_charon.go` +
  `remedy.go`: renamed `charonInstallPath` → `nousInstallPath`,
  pointed all findings/remedies at `~/.local/bin/nous` and
  `make nous-install`. Test fixtures updated. Build + go vet +
  package tests clean.
- [ ] M4: `make nous-install` from a clean state; verify only
  `nous` lands in `$(NOUS_INSTALL_PREFIX)`. `nous service status`
  shows the unified service. **Requires operator verification on
  the install machine — agent cannot run codesign + launchctl
  inside the sandbox.**
- [x] M5: Atlas updated — `atlas/charon/index.md` and
  `atlas/nous/lib-layout.md` reflect single-binary model. No
  top-level `atlas/index.md` exists; cluster atlases are the
  index here.

**Out-of-scope, follow-up candidates:**
- `lib/charoncli`: `BuildRoot`, `ServeCmd`, `ServiceCmd`, `StatusCmd`
  are now orphaned (their only caller was `cmd/charon/main.go`).
  Go allows unused exported functions; deleting risks breaking grep
  references in agent docs I haven't audited. Worth a focused
  follow-up "trim charoncli orphans" issue.
- `lib/charoncli` → `lib/nouscli` rename. The package is no longer
  charon-specific; nous owns the CLI verbs. Pure-cosmetic rename
  touching every import; defer until there's a coupled change to
  justify the churn.
- `lib/identity` tests hardcode `/tmp` instead of `os.TempDir()`,
  failing under sandboxed runs. Pre-existing; not nous#20 scope.

## Log

**2026-05-10 — M2 + M3 + M5 shipped.** Verb mounting (`cmd/nous/main.go`)
+ new aggregated `nous status` (`cmd/nous/status.go`) + binary
deletions + Makefile/doctor/audit/instructions/service cleanup +
`lib/security` rename. Build clean (`go build ./...`), `go vet ./...`
clean, package tests pass on lib/security, lib/charoncli, lib/brainsync,
lib/provider/.... Pre-existing `lib/identity` test failures (hardcoded
`/tmp`) unrelated to this issue. Smoke-tested: `nous --help` shows
all 9 new verbs at the right paths; `nous status` produces three-line
aggregate; `nous service doctor` reports unified-service checks.

M4 (verify clean `make nous-install` on operator machine) gated on
operator running it — agent cannot exercise codesign + launchctl
from sandbox.

**2026-05-10 — M1 audit complete.** No Go imports cross the
`cmd/charon` or `cmd/brain-sync` boundary (they're main packages).
External references concentrate in:
- `Makefile.nous` lines 55, 67-68, 140-141, 144, 175, 196
  (build/sign/install/banner)
- `cmd/nous/service.go` lines 62, 114-124 (legacy plist migration
  code; `resolveSiblingBinary("charon")` helper)
- `cmd/nous/doctor.go` lines 62-65, 188-210 (four checks for the
  two legacy plists)
- `cmd/nous/instructions.go` line 180, 185 (operator-facing docs
  about sibling-binary discovery)
- `scripts/test-brain-sync.sh` lines 38, 40, 80-82 (execs the
  brain-sync binary directly)
- `atlas/charon/index.md` line 18, `atlas/nous/lib-layout.md`
  lines 69-70

CLI verb surface from `lib/charoncli/charoncli.go` lines 50-63:
charon exposes 14 root subcommands; nous already mounts 5 (Auth,
Manifest×2, Service, Serve, Instructions). 9 verbs need migration
or drop — see verb migration map above.

Filed 2026-05-10. Surfaced from a debugging session about keychain
prompt churn during `make nous-install`. Three-binary signing
contributed to but did not solely cause the prompts — the bigger
cause is ad-hoc signing producing per-build CDHashes, addressed
separately by switching to Developer ID signing (operator's
existing cert). This issue is the cleanup; the Developer ID
switch is a config change, not a code change.
