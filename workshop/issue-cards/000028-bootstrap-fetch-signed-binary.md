---
id: 000028
status: open
created: 2026-05-20
updated: 2026-05-20
estimate_hours: 4
---

# bootstrap: fetch signed nous binary from GitHub releases (when releases exist)

## Problem

Today, an operator runs:

```
make bootstrap            # install deps, generate keys
make nous-build           # compile bin/nous (puts it at cmd/nous/bin/nous, symlinked to bin/nous)
nous service install      # daemon up
```

Step 2 requires a Go toolchain + ~30s of compilation. For end
users who just want to *use* nous (not develop it), that's
friction with no payoff. They don't care about source-level
control; they want a working binary.

Once nous publishes signed release binaries via GitHub
Releases (with `make nous-sign` + `make nous-notarize` from
the existing pipeline), `make bootstrap` could download +
verify + drop into `bin/nous` directly, skipping `make nous-build`.

Power users / developers still run `make nous-build` to override
with their local build; the symlink layout makes this seamless.
