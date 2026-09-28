---
id: 000029
status: open
created: 2026-05-20
updated: 2026-05-20
estimate_hours: 4
---

# build: cross-repo Go binary support (brain-repo imports nous/lib)

## Problem

`make build` (added in ariadne 4416313, vendored into nous) builds
`cmd/*/main.go` in the *current* repo. For nous, that's gmail +
oneshot. For a brain repo with its own `cmd/<utility>/main.go`,
the operator's binary can use the brain repo's own go.mod
dependencies — but cannot import `lib/charoncli`, `lib/identity`,
or other nous internals without machinery we don't have yet.

The typical brain-repo case (per operator vision 2026-05-20):
small utilities, local stubs that replace MCP-style external
services. Some of these will naturally want to use nous's
provider/charon machinery — e.g., a stub that proxies an LLM
call through charon, or a notification helper that talks to
`lib/notify`. Today, those binaries can't be authored in the
brain repo without one of three workarounds:

1. **Publish nous as a Go module.** Brain repo's `go.mod`
   declares `require github.com/xianxu/nous vX.Y.Z`. Needs nous
   to push tagged releases; brain repo needs `go get` access.
   Probably the cleanest long-term path.
2. **Go workspaces** (`go.work` at `~/workspace/` listing both
   `./nous` and `./brain-foo`). Local-only; not portable across
   machines. Fiddly but doesn't require nous releases.
3. **Vendor nous into brain repos.** Heavy + drift-prone; reject.
