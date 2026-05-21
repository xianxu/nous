---
id: 000028
status: open
deps: []
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

## Insight

`bin/nous` becomes the canonical location regardless of source.
Today it's a symlink to `cmd/nous/bin/nous` (built locally).
With releases, the same path is the drop target for the
downloaded binary. The daemon's plist already points at
whatever `bin/nous` resolves to.

The download is a single-file fetch from GitHub Releases —
small (~30MB), HTTPS, signed by Apple Developer ID. Verification
via `codesign --verify --strict bin/nous` after download; refuse
to run if signature doesn't validate (matches the existing
`lib/codesign` runtime check).

## Done when

- `make bootstrap` (after installing deps + identity) has a new
  optional step that:
  1. Detects whether there's a published release matching
     `bin/nous`'s current version (or "latest" on a fresh
     bootstrap).
  2. If yes: downloads to a temp path, `codesign --verify`s it,
     moves to `bin/nous`. Logs the version + signing identity.
  3. If no (no releases yet, or local build already newer):
     prints "skipping binary download — run `make nous-build`
     to compile from source" and continues.
- `NOUS_BOOTSTRAP_SKIP_BINARY_FETCH=1` env var to bypass entirely
  (for dev workflows that always want local builds).
- Symlink convention is dropped: `bin/nous` is the binary itself,
  not a symlink. `make nous-build` produces `bin/nous` directly
  (move the build target). The previous `cmd/nous/bin/nous` path
  goes away (or stays as a build-intermediate).
- `make nous-sign` / `make nous-notarize` pipeline outputs land
  at `bin/nous` consistently (already mostly true; verify).
- `nous service install` and lib/codesign's "am I the signed
  binary?" check continue to work against the new layout.

## Open questions

- **Version resolution.** "Which release should bootstrap fetch?"
  Probably: respect a `VERSION` file in the repo (or `git
  describe`) for source-controlled pinning. Operators who pulled
  nous@v1.2.3 get v1.2.3 binary; nightly main pullers get latest.
- **gh CLI vs raw curl.** `gh release download` is convenient but
  requires `gh` to be authenticated. Raw curl against the public
  releases API works unauthenticated. Probably curl, with `gh`
  fallback.
- **macOS-only restriction.** Today's signed pipeline produces
  Darwin/arm64 binaries. Linux/x86_64 users need to fall back
  to source build. Make `bootstrap`'s binary-fetch step detect
  `uname -sm` and skip on unsupported architectures.

## Out of scope

- Actually publishing the first release. That's an operator
  decision (when to tag + push to releases). This issue is
  just the bootstrap-side machinery.
- A `nous self-update` verb that re-fetches after the
  bootstrap. Separate concern; bootstrap handles cold start.

## Log
