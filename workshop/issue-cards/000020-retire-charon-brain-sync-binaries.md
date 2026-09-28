---
id: '000020'
status: done
created: 2026-05-10
updated: 2026-06-02
estimate_hours: 2
actual_hours: 1.5
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
