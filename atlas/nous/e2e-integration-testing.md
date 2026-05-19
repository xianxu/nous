# End-to-end integration testing

The story this doc tells: how the lib-level e2e test in
`lib/brain/integration_test.go` is architected, why it works at the
layer it does, and how to extend it to cover more of nous/charon's
mechanisms over time.

The triggering insight (2026-05-19): the multi-recipient flow added
in nous#23 was too tangled to manually verify on every change —
operator + two peers + three GPG homedirs + a gcrypt remote + a
keys branch + a brainsync pull/push cycle. After landing, the first
real run caught a production bug (`gcrypt-participants` not synced
on clone). The test paid for itself before it had run a second time.

## Layer choice — why lib, not CLI

There are three plausible layers for a nous e2e test:

| Layer | Setup cost | Coverage | What it catches | What it misses |
|---|---|---|---|---|
| **Library** (lib/brain etc.) | low — Go test, no subprocess for nous itself | High for core logic | Multi-peer flows, gcrypt round-trips, manifest invariants | CLI flag parsing, cobra wiring, prompt UX |
| **CLI** (cmd/nous via os/exec) | medium — fork+exec per verb, parse stdout | Same core + the cobra layer | Argument parsing bugs, TTY-required ceremony, env handling | gh-API specifics |
| **Full system** (tart VM + real GitHub) | high — VM provision, gh repo create, network | All of the above + gh / launchd / TCC | Distribution-grade bugs | Slow; flaky on network |

Most bugs live in the core logic. The CLI is a thin cobra wrapper;
argv-parse mistakes are usually caught by running the command once
manually. The full-system layer catches "GitHub changed an API" or
"launchd plist key required reverification on macOS X.Y" — rare
and worth its own (manual / VM-based) test path.

So the e2e suite lives at the library layer: `t.TempDir()` for the
"remote" (file:// bare repo), `/tmp/ngpg-*` for each peer's
`GNUPGHOME`, direct calls into `lib/brain` / `lib/brainsync` /
`lib/identity` / `lib/brain/filestore`. Real gpg subprocesses, real
git subprocesses, real gcrypt. ~10-20s wall clock per test.

CLI-layer tests would be a small set of "does this verb parse args
correctly" smoke tests — not the right place for multi-peer flows.
Full-system tests stay manual (the `nous#12` dogfood walkthrough)
until they become regular enough to automate.

## Simulation mechanics

### Multi-peer via per-peer `GNUPGHOME`

A `testPeer` carries its own GPG homedir, fingerprint, and armored
pubkey. Operations switch the process's `GNUPGHOME` env via the
`withPeer(t, p, fn)` helper — `lib/identity` and gpg subprocesses
read the env at call time, so the active peer's keyring is whatever
the current `withPeer` block specifies.

Trade-offs:
- **Pro**: a single test process simulates N peers without needing
  N subprocesses or N goroutines. Operations are linear; easy to
  reason about.
- **Pro**: lib calls work unmodified — no need to refactor
  `lib/identity` to accept `GNUPGHOME` as a parameter.
- **Con**: not parallel-safe. Tests using `withPeer` must not call
  `t.Parallel()` — env is process-global. Within one test process,
  one peer at a time.
- **Con**: gpg-agent caches per homedir. Switching homedirs spawns
  a new agent rather than reusing — small per-test overhead, but
  acceptable.

### GPG homedirs at `/tmp/ngpg-*`

macOS unix sockets cap at 104 chars. `gpg-agent`'s socket path is
`$GNUPGHOME/S.gpg-agent` (plus suffix). `t.TempDir()` lands under
`/var/folders/.../T/...` which routinely blows that limit on first
invocation, surfacing as a confusing "socket bind failed" error.

The workaround `lib/identity` already uses: short paths under
`/tmp/ngpg-<label>-`. The integration test inherits this pattern
(`setupPeer`'s `os.MkdirTemp("/tmp", "ngpg-"+label+"-")`). In
sandboxed environments where /tmp is restricted (Claude Code's
default), the test skips at setup; it runs cleanly on a normal
operator machine.

### Bare repo at `file://` as "GitHub"

`initBareRepo` does `git init --bare` in `t.TempDir()`. gcrypt
works over `file://` URLs the same way it works over `ssh://` —
the remote helper doesn't care about the transport, just that it
can push/pull packs.

What this skips:
- `gh repo create` flow (and the credential-scope question that
  bit the #12 dogfood)
- SSH key distribution to GitHub
- Network-level fault injection (rate limits, transient 5xx)

Those belong in the manual VM dogfood, not in the unit-test
suite.

### Cleanup

`t.Cleanup` on each setup helper:
- `setupPeer` kills `gpg-agent` for the homedir (the agent holds
  the socket open and can prevent `RemoveAll` from succeeding),
  then `os.RemoveAll(home)`.
- `t.TempDir()` cleans up the bare repo and any per-test working
  trees automatically.

No global state leaks between tests. Each test starts cold.

## Pattern: how to add a new e2e scenario

Read `TestEndToEnd_MultiRecipientAndFileSync` as the template. The
shape of any new scenario is:

```
1. Setup
   - initBareRepo(t)
   - setupPeer(t, <name>, <email>) per simulated actor

2. Act
   - withPeer(t, peer, func() { … operate via lib calls … })
   - Compose pre-existing helpers (provisionBrain, admitRecipient,
     cloneBrainViaPeerkeys, writeBrainFile, readBrainFile, …).

3. Assert
   - readBrainFile to verify content sync
   - assertPubkeyInKeyring to verify pubkey distribution
   - Inspect brain manifest via brain.Read for state checks
```

When a new helper is needed:

- Single use → inline in the test.
- Two uses → extract to a helper in `integration_test.go`.
- Cross-test → consider whether `lib/brain` itself wants the
  helper as a public function (the library benefits from the
  cleaner API too).

When a new scenario reveals a real bug (as the first run did with
`gcrypt-participants`):

1. Don't paper over it in the test. Surface the failure cleanly.
2. Fix at the right layer (usually the library, not the test).
3. The test then serves double duty: regression net + working
   spec of the expected behavior.

## Mechanisms covered today

| Mechanism | Function | Test |
|---|---|---|
| GPG keygen | `setupPeer` → `gpg --batch --generate-key` | One per peer |
| GPG import / export | `identity.Import`, `identity.Export` | Operator imports peer pubkeys |
| Brain provision (single-recipient) | `brain.WriteManifest`, `SetGcryptParticipants`, `brainsync.AddCommitPush` | `provisionBrain` |
| Brain provision (multi-recipient) | `brain.RewriteFrontmatter`, manifest re-key, second push | `admitRecipient` (modeled as add-after-init) |
| Recipient add | `admitRecipient` helper | Two recipients added |
| Pubkey publish | `brain.PublishPubkey` | After each `admitRecipient` |
| Pubkey bootstrap (pre-clone) | `brain.BootstrapPubkeys` | `cloneBrainViaPeerkeys` |
| Pubkey auto-import (post-pull) | `brain.ImportAllPubkeys` | Step 5 (peerA picks up peerB) |
| Brain clone via gcrypt | `git clone gcrypt::…` | `cloneBrainViaPeerkeys` |
| gcrypt-participants sync after pull | `brain.SyncGcryptParticipantsFromManifest` | Triggered via `brainsync.PullBrain` |
| File sync push | `brainsync.AddCommitPush` | Steps 7, 10 |
| File sync pull | `brainsync.PullBrain` | Steps 8-9, 11 |

## Mechanisms not yet covered

The aspirational scope is "every key behavior in nous/charon has
an e2e test that documents what right looks like." Open items, in
rough priority order:

1. **Recipient revocation** — operator removes a peer. Verify:
   removed peer can't decrypt new pushes; their pubkey is
   deleted from the keys branch; remaining peers' pubkey list
   shrinks on next sync.
2. **Conflict resolution** — two peers edit the same file
   concurrently; `nous brain resolve` produces a sensible merge.
   Covers `lib/brainsync/conflicts.go` + `lib/brainsync/resolve.go`.
3. **brain-sync watcher loop** — the real auto-discovery + per-
   brain goroutine model from `lib/brainsync/run.go`. Today's
   test calls `PullBrain` / `ImportAllPubkeys` directly; a full
   watcher test would start `Run` in a goroutine, observe its
   reconcile loop, and verify state across ticks.
4. **Charon proxy + agent identification** — fork a `nous serve`
   subprocess, issue a credential request, verify the proxy's
   peer-DR check works. Sits on `lib/provider/proxy` +
   `lib/provider/runtime`.
5. **Arm / disarm + keychain ACL** — exercise the codesign-based
   namespace routing from `lib/codesign` + `lib/provider/vault/
   keychain`. Requires a signed binary to test the prod-namespace
   side; can test the dev-namespace side easily.
6. **Service install / uninstall** — launchd plist generation +
   `launchctl` interactions from `lib/service`. macOS-only;
   touches operator-level state, so opt-in only.
7. **Notification dispatch** — `lib/notify`'s three backends.
   The unit tests already cover the pick logic; an e2e test
   would verify the actual subprocess invocations
   (terminal-notifier, osascript) produce real banners. Probably
   manual rather than automated.
8. **Security audit checks** — `lib/security/check_*`. Most
   checks need a configured macOS host to be meaningful; would
   need an isolated environment that simulates SIP / TCC /
   FileVault state. Heavy. Probably stays as manual via `nous
   security check` against a known-state machine.

For each, the question to ask: does the bug-class this would catch
actually happen often enough that the test pays for itself?
nous#23's multi-recipient flow met that bar; some of the items
above (charon proxy, notification) may not until the surface
becomes a recurrent change point.

## Operating notes

- Run: `go test ./lib/brain/ -run TestEndToEnd -v -timeout 60s`
- Skip on `-short` so day-to-day `go test ./...` stays fast.
- Skip on non-darwin/linux (subprocess paths assume POSIX gpg).
- Skip when `gpg`, `git`, or `git-remote-gcrypt` aren't on PATH.
- Expected wall clock: 10-20s per test. gpg keygens dominate;
  amortizing them across multiple sub-scenarios in one big test
  is a design choice (one TestEndToEnd_* per story rather than
  one per micro-assertion).

## When this doc gets stale

This atlas should be revised when:

- The simulation layer changes shape (e.g., we add a CLI-layer
  test suite, or migrate to a proper test-double substrate).
- A mechanism in "not yet covered" moves to "covered" — update
  the table.
- A pattern emerges that diverges from the today's template
  (e.g., we discover we need a separate test process per peer
  for some scenario; the `withPeer` env-switch model wouldn't
  cover it).

The integration test file itself (`lib/brain/integration_test.go`)
is the authoritative how-it-works; this atlas is the why-and-how-
to-extend.
