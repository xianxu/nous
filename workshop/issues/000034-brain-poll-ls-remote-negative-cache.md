---
id: 000034
status: open
deps: []
target: shared-brain-infrastructure-and-ui
created: 2026-05-26
updated: 2026-05-26
estimate_hours: 3
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

git-remote-gcrypt stores its state in the **outer** git repo as one or
more refs under `refs/gcrypt/<remote-name>` (pointing to the commit that
holds the encrypted pack list). `git ls-remote` against the outer SSH
remote returns those SHAs over the standard git smart protocol — no gpg
involved, a few KB of network, microseconds of CPU.

The poll loop becomes:

```
for each shared brain repo:
  outer_remote = origin's underlying ssh url   # ssh://git@github.com/xianxu/<repo>.git
  remote_refs  = git ls-remote outer_remote refs/gcrypt/*
  cached_refs  = lastSeen[repo]
  if remote_refs == cached_refs:
    skip                                       # steady state — no work
  else:
    git -C repo fetch origin                   # full encrypted fetch (gpg decrypts)
    lastSeen[repo] = remote_refs
```

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

- [ ] **M1 — ls-remote helper**
  - [ ] Function `lsRemoteGcryptRefs(repoPath string) (string, error)`
        that resolves origin's outer ssh url and runs
        `git ls-remote <url> 'refs/gcrypt/*'`, returns a stable
        serialization of the result.
  - [ ] Unit test against a fixture repo or mocked git command.

- [ ] **M2 — Integrate into the poll loop**
  - [ ] Identify the poll-loop site in `cmd/nous/...` or
        `lib/brain/sync/...` (brain-sync code, wherever the fetch tick
        lives post-#16 unification).
  - [ ] Add in-memory `lastSeen map[string]string` to the poller.
  - [ ] On each tick: ls-remote first; skip fetch on match.
  - [ ] Log a debug line when skip fires (so we can verify steady-state
        cost in `nous serve -v` output).

- [ ] **M3 — Verification**
  - [ ] Repro the incident shape locally: install nous launchd job,
        observe baseline CPU + gpg child-process rate.
  - [ ] Build + reinstall with the fix.
  - [ ] Observe: gpg-agent stays cold, no `gpg --status-fd` children
        in steady state, load average normal.
  - [ ] Force a remote change on one brain repo, observe one fetch
        fires for that repo only.

## Log
