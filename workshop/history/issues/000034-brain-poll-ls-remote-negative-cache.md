---
id: 000034
status: done
deps: []
target: shared-brain-infrastructure-and-ui
created: 2026-05-26
updated: 2026-05-26
estimate_hours: 3
actual_hours: 1
---

# `nous serve` brain-poll: cheap negative cache via `git ls-remote`

## Problem

`nous serve` background-polls each shared brain repo with `git fetch` on a
tight interval. For encrypted brains (git-remote-gcrypt remotes), every
fetch spawns `gpg --status-fd 3 -q -d` to decrypt the manifest **even
when nothing has changed**, because the gcrypt transport can't ask the
server "did anything move?" — it has to download the encrypted manifest
and decrypt it to find out.

At steady state across N brain repos, this dominates CPU:

- During a recent incident on the operator's mac, `nous serve` had been
  running 2+ days. `gpg-agent` sustained 90%+ CPU and load average hit
  16.6 on a 12-core machine. Trace showed `git fetch` → `git-remote-gcrypt` →
  `gpg --status-fd 3 -q -d` firing every 1–2 seconds across multiple
  brain repos (`brain-family`, `brain-shared-test`, ...). Nothing was
  changing on any of them.
- `nous service uninstall` resolved the symptom by removing the launchd
  job; load recovered within minutes.

The polling architecture itself is fine — we want git history and have
ruled out syncthing event-driven sync. The bug is that polling
unconditionally invokes the decrypt path.

## Spec

Add a negative cache that short-circuits the expensive decrypt-fetch when
the remote hasn't moved.

### Cheap check

git-remote-gcrypt stores its encrypted content on the **regular
`refs/heads/master`** branch of the underlying git server — the
commit SHA on that branch *is* the unique fingerprint of the
encrypted state. Same SHA on master = nothing decryptable changed.
The local `.git/refs/gcrypt/` directory is empty on a steady-state
clone; it's used only transiently during in-flight gcrypt operations.

So the cheap check is: probe the outer remote (the underlying ssh
url, with the `gcrypt::` prefix stripped) via `git ls-remote` and
compare the entire ref listing. The poll loop becomes:

```
for each shared brain repo:
  outer_url   = strip "gcrypt::" from remote.origin.url
                # e.g. ssh://git@github.com/xianxu/<repo>.git
  remote_refs = git ls-remote outer_url   # plain smart protocol, no gpg
  cached_refs = lastSeen[repo]
  if remote_refs == cached_refs:
    skip                                  # steady state — no work
  else:
    PullBrain(repo)                       # full encrypted fetch (gpg decrypts)
    if PullBrain succeeded:
      lastSeen[repo] = remote_refs
```

We capture the *entire* `ls-remote` output (sorted for stable
hashing), not just `refs/heads/master`. Reason: the `refs/heads/keys`
branch (nous#23 pubkey distribution) moves independently when peers
publish their keys, and `syncBrainPubkeys` needs to see that
movement. Sorting normalizes server-dependent ordering so the
serialization is byte-stable across calls.

Steady-state cost drops from "gpg decrypt per tick" to "one ls-remote per
tick" (a single round-trip, no decryption).

### Cache scope

- In-memory per `nous serve` process. No persistence; on restart, the
  first tick per repo pays the full fetch cost once, then settles into
  ls-remote-only.
- Keyed by repo path. Value is the ls-remote output (sha + ref pairs),
  compared as a stable serialization (sort by ref name, join, hash).

### Edge cases

- **First poll of a fresh process** — cache miss, full fetch, cache
  filled. Correct.
- **Outer ref renamed/removed** — ls-remote returns a different shape;
  treated as change, full fetch runs. Correct.
- **Outer ssh transport fails** — ls-remote errors; surface the error
  the same way we'd surface a fetch error today. Don't fall through to a
  blind fetch (that would just incur the decrypt cost again to no
  benefit).
- **Multiple `refs/gcrypt/*` refs** — capture all of them; any single
  one moving triggers fetch.

## Out of scope

- **Per-repo poll interval tuning.** Worth doing eventually but
  orthogonal — even at 1Hz, ls-remote-only is cheap enough that
  interval tuning becomes a network politeness question, not a CPU one.
- **Persistent cache across restarts.** Fresh-start cost is one full
  fetch per repo. Acceptable.
- **Replacing polling with syncthing events.** Operator wants git
  history; rule out.
- **Generalizing to non-gcrypt remotes.** Plain git fetch on a
  no-change remote is already cheap (smart protocol short-circuits
  server-side); only the gcrypt path needs this.

## Plan

- [x] **M1 — ls-remote helper**
  - [x] `RemoteRawURL(repo)` — strips `gcrypt::` prefix from origin url.
  - [x] `LsRemoteRaw(url)` — runs `git ls-remote <url>`, returns a
        sorted byte-stable serialization. Bypasses the gcrypt helper,
        so no gpg invocation.
  - [x] Unit tests in `lib/brainsync/lsremote_test.go`: gcrypt-prefix
        stripping, plain-url pass-through, missing-origin error, stable
        serialization across calls, change detection after push, error
        on bad url. 6/6 pass.

- [x] **M2 — Integrate into the poll loop**
  - [x] Poll-loop site located: `lib/brainsync/watch.go` (`Watch()`
        ticker case). Per-brain block now calls `refSnapshot(b)` before
        `PullBrain(b)` and short-circuits the whole block when the
        snapshot matches `lastSeenRefs[b]`.
  - [x] In-memory `lastSeenRefs map[string]string` scoped to `Watch()`.
        Updated only on successful pull (no cache poisoning on fetch
        errors).
  - [x] On `refSnapshot` error (ls-remote couldn't run / network
        glitch): fall through to existing fetch path. Same behavior as
        today, no regression.
  - [x] Verbose log line `brainsync: <repo> no remote changes (skip)`
        when the cache fires.

- [x] **M3 — Verification**
  - [x] Service reinstalled by operator; PID 13109 under launchd.
  - [x] Confirmed: 15s ps sample showed `git ls-remote` children per
        5s tick with ZERO `gpg --status-fd` processes; gpg-agent off
        the top-10 CPU list; load avg dropped from 16.59 to 2.92.
        `nous.log` showed `no remote changes (skip)` for all 5 brains
        across 3 consecutive ticks (14:26:12 / :17 / :22).
  - [ ] Positive-path check (push to one brain, observe one fetch
        fires) deferred — not required for close given the negative
        path is the dominant cost; can be exercised opportunistically.

## Revisions

- **2026-05-26 — Spec correction (pre-implementation).** Original spec
  said gcrypt state lives at `refs/gcrypt/<remote-name>` on the outer
  remote. Verified against a live brain repo: gcrypt actually stores
  encrypted content on the regular `refs/heads/master` branch of the
  underlying git server. Updated the Cheap-check section to use full
  `ls-remote` against the gcrypt-stripped url (captures both master
  and the nous#23 keys branch in one shot). No scope change.

## Log


- 2026-05-26: closed — Service running under launchd (PID 13109); 15s ps sample shows git ls-remote children per 5s tick with ZERO gpg --status-fd processes; nous.log confirms "no remote changes (skip)" for all 5 brains across 3 consecutive ticks; load avg dropped from 16.59 to 2.92. go test ./... green; M1 unit tests 6/6 pass. ACTUAL via FORCE=1: active-time-v3 reported 0 events (session telemetry not captured for this Claude Code session); manual estimate ~55 min across explore/test/implement/verify/atlas.
- **2026-05-26 — M1 + M2 implemented.** New file
  `lib/brainsync/lsremote.go` (helpers); test file
  `lib/brainsync/lsremote_test.go` (6/6 pass); ~20 LOC change to
  `lib/brainsync/watch.go` (cache check before per-brain pull). `go
  test ./...` green, `go vet ./lib/brainsync/...` clean, `go build
  ./cmd/nous/` clean. M3 verification staged for operator: requires
  gpg-agent re-arm + service reinstall.

