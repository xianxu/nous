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

The `file://` bare repo models GitHub's **data plane** (gcrypt
push/pull, branches). The **control plane** (invitations,
collaborators, the MinimalRepository shape, multi-user tokens) is
modeled separately by the `lib/gh` fake (`gh.NewFake(Conf)`, shim'(gh),
ariadne#71/nous#42): a stateful in-memory `gh.Client` the join/invite
flows run through unchanged. Its `CloneURL` returns a `file://<tmpdir>/`
URL pointing at exactly these bare repos — that's the seam where the
control-plane fake meets this data plane. Grounded against real GitHub
by a build-tagged contract test (see `lib/gh/contract_real_test.go`,
run ~monthly).

What this skips:
- `gh repo create` flow (and the credential-scope question that
  bit the #12 dogfood)
- SSH key distribution to GitHub
- Network-level fault injection (rate limits, transient 5xx)
- Below-the-seam gh endpoint correctness — covered by the fake's
  contract test against real `gh`, not here.

Those belong in the manual VM dogfood (or the gh contract test),
not in the data-plane unit-test suite.

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

## Control-plane simulation via the gh fake (nous#42)

`TestSimulation_OnboardingLifecycle` (`lib/brain/onboarding_simulation_test.go`)
is the worked example of the **shim(X)/shim'(X)** pattern: it drives the *whole*
multi-actor onboarding lifecycle hermetically — invite → accept (GitHub **control
plane**, via `gh.NewFake(gh.Conf{…}).(*gh.Fake)`) plus provision → publish →
auto-admit → clone → decrypt+verify → leave (**data plane**, the `file://` gcrypt
bare repo above). The two planes meet at **`gh.Fake.CloneURL`**: the fake's
`CreateRepo` git-inits the bare repo (`-b main`) and its `CloneURL` for that repo
*is* the gcrypt remote `provisionBrain` uses — the test asserts that equality.

This is the value beyond regression-pinning: any future GitHub-touching feature
can be developed and self-verified against a realistic, scriptable simulation —
operator + joiner over one in-memory GitHub, no network, no VM — before a human
touches the VM dogfood. `gh.Fake` is exported precisely so other packages drive
their own simulations against it (cf. `httptest.Server`). The fake is grounded
against real GitHub by `lib/gh/contract_real_test.go` (`-tags conformance`, run
~monthly); below-the-seam endpoint correctness is pinned by `lib/gh/real_test.go`.

## Credential-lifecycle simulation via the oauth fake (nous#44)

`shim(google-oauth)` (`lib/provider/oauth/`) is instance #2 of the same pattern,
and the first with an **async redirect callback**. The provider-neutral
`Provider` port (`port.go`: `Auth`/`Refresh`/`Revoke`/`CheckHealth` — the union
of what charon's TUI/proxy/charoncli consumers use) has two implementations: the
real adapter (`google.go`, the only thing that talks to Google — HTTP + browser +
the `waitForCallback` channel) and a stateful in-memory `Fake` (`fake.go`,
`oauth.NewFake(Conf)`). Both shape credentials through one **pure core**
(`token.go`: `credentialFromToken`/`applyRefresh`/`parseIDToken`/`mintIDToken`),
so they cannot drift on rotation/sidecar-preservation/verified-email — and the
fake mints ID tokens the real `parseIDToken` actually parses (a model, not a
mock). Charon's GCP token path runs hermetically through the fake
(`lib/charoncli/oauth_seam_test.go`).

What makes the fake a *model* rather than a mock: it executes an explicit
**consumer-POV state machine** (`workshop/targets/oauth-credential-lifecycle.md`
— `NoGrant`/`Active`/`Expired`/`Dead`), and its fault knobs are that machine's
**provider-autonomous edges** — the transitions the issuer makes underneath us
that we only observe late (`RevokeGrant`→`Dead`, `Transient`→`Unknown`,
`DowngradeScope`, `DenyConsent`, `WrongAccount`). Hidden provider state surfaces
as faults on our side; see the shim state-machine pensive (ariadne) for the
R/M/S framing.

**Grounding boundary** (`lib/provider/oauth/contract_real_test.go`, `-tags
conformance`): `Refresh` + the `CheckHealth` read are grounded against real
Google via a Keychain test-account refresh token. The **consent leg** (`Auth` —
non-headless), **`Revoke`** (destructive to the grounding token), and the
provider-autonomous **`→Dead`** edge are fake-only/manual — the harder boundary
nous#42 flagged: don't claim coverage the mechanism can't deliver. The
transition table's grounding column in the target *is* the boundary doc.

The Keychain refresh token is obtained via `cmd/oauth-conformance-provision`
(charon's own consent flow — a Google refresh token is bound to the issuing
client, so it can't be a pasted PAT like gh's). First certified 2026-06-08 against
real Google (nous#49); re-cert ~monthly. See that command's `SKILL.md` for the
provision→certify→re-cert loop (and the nous#48 Microsoft template).

The per-shim **last-certified ledger** across all providers lives in
`atlas/nous/shim-conformance-grounding.md` — the freshness index of this whole
grounding layer.

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
| GitHub invite → accept (control plane) | `gh.Fake.InviteCollaborator` / `PendingInvitations` / `AcceptInvitation` | `TestSimulation_OnboardingLifecycle` |
| GitHub collaborator leave (control plane) | `gh.Fake.RemoveCollaborator` | `TestSimulation_OnboardingLifecycle` |
| Control↔data plane seam | `gh.Fake.CloneURL` == gcrypt remote | `TestSimulation_OnboardingLifecycle` |

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

## Headless VM brain testing (CLI-level, nous#36)

The lib-layer test above simulates multi-peer with per-`GNUPGHOME`
env-switching in one process. A complementary, higher-fidelity layer
runs the *actual CLI* in a real macOS tart VM — the only way to catch
process-level interaction bugs (gpg-agent, pinentry, gcrypt over SSH,
launchd) that the in-process simulation can't.

The blocker it solves: a headless `make tart` VM has no window server,
so `pinentry-mac` can't draw and every passphrase prompt fails. Pieces:

- **`scripts/brain-vm-setup.sh`** — installs a persistent fake pinentry
  (`~/.local/bin/pinentry-brain-test`, returns the throwaway test
  passphrase) and points gpg-agent at it, so gpg/gcrypt run unattended.
  Idempotent; refuses outside a disposable tart VM (admin user +
  `~/.tart-current-repo`) unless `NOUS_BRAIN_VM_FORCE=1`.
- **`.tart/vm-hooks.d/00-gpg-setup.sh`** — auto-runs the above on every
  boot via ariadne#59's vm-hooks convention. So `make tart` from nous
  yields a GPG-unattended VM with no manual step.
- **`--verified-last8 <8hex>`** on `nous identity import` / `nous brain
  recipient add`, and the non-interactive `nous identity init`
  (`--name`/`--email`/`--expiry` or `IDENTITY_*`) — let the otherwise
  TTY-only ceremony be driven over `ssh admin@$(tart ip nous-test) '…'`.
  Security delta recorded in `brain/atlas/threat-model-shared-brain.md`
  (`## Revisions`, 2026-06-01).

Boot-from-nous is deliberate: the VM is a generic brain-client; the
target brain enters via `nous brain clone gcrypt::…` or
`make tart SYNC=../<brain>`. The operator's real key never enters the VM
(it holds only throwaway Ying/Emma identities), which is what makes the
hardcoded-passphrase shim safe.

**`scripts/brain-vm-e2e.sh`** is the VM-free, GitHub-free companion: it
drives the real `nous` CLI through the full two-peer scripted ceremony
(`identity import` / `brain recipient add --verified-last8`, plus the
wrong-last8 rejection) against a `file://` bare gcrypt remote, then proves
a complete `recipient remove` clears every resurrection vector (manifest +
verified.yaml + all keys-branch entries; nous#38). It uses `%no-protection`
throwaway keys so gpg/gcrypt never prompt, so it runs anywhere gpg + git +
git-remote-gcrypt are present — the scriptable-ceremony substrate check
that needs neither a VM nor operator keys.

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
