# Autosave + `nous push` checkpoint

How the brainsync daemon hides git from the operator on the common
path, and the explicit gesture that names a moment.

## The two cadences

Brain sync used to couple commit cadence and push cadence: operator
runs `git commit`, RefWatcher sees the ref change, daemon pushes.
That makes git a user surface — operators are choosing commit
boundaries instead of doing the actual work.

Issue #30 split the cadences:

- **Commit cadence**: 5s debounce on file-content changes inside the
  brain dir. Cheap, local-only, no encryption work. The daemon
  auto-commits modified-tracked files in the background.
- **Push cadence**: 60s debounce on file change OR commit (autosave
  OR manual `git commit` via RefWatcher). One push covers a
  writing-session's worth of activity instead of one push per save.

## What the daemon auto-commits — and what it doesn't

`AutoCommitter` (`nous/lib/brainsync/autocommit.go`) stages and
commits ONLY **modified-tracked** files
(`git diff --diff-filter=M --no-renames`).

Deliberately *out of scope* for autosave (require explicit operator
gesture):

- **Untracked files** (`?? path`): explicit `git add` needed.
- **Deletions of tracked files** (` D path`): explicit `git rm` needed.
- **Renames** (excluded by `--no-renames` in the diff filter).

Rationale: those three classes carry decisions ("is this file
intended to be in the brain?", "do I actually want this file gone?")
that the daemon shouldn't guess. Modifying an already-tracked file,
by contrast, is just continuing prior intent.

When the daemon does see untracked / deleted paths at autosave time,
it logs a one-line hint (deduped against the previous log so the
log doesn't get spammed). The `nous push` CLI prints the same hint
to the operator's terminal.

## Skip cases

`AutoCommitter` skips both commit and push when the brain has a
merge / rebase / cherry-pick in progress
(`MergeOrRebaseInProgress`). The operator is in the middle of
something with uncommitted state we don't want to stomp on.

The same predicate gates `nous push` (refuses with a clear message).

## `nous push` — the operator-facing gesture

```
nous push                          # flush whatever's pending
nous push "finished tokyo draft"   # name the checkpoint
```

The complement to autosave. Walks up from cwd to find the enclosing
`.brain/config.md` (`brain.EnclosingBrain`), commits any modified-
tracked files (with the operator's message if given, else an
autosave message), then pushes — bypassing the 60s debounce.

Works whether or not the daemon is running; operates directly on
the brain's git repo. Git's `.git/index.lock` and ref locks
serialize against a daemon push that might fire concurrently.

v1 explicitly **does not** create empty commits — `nous push "msg"`
when nothing is uncommitted prints a notice that the message wasn't
used and pushes existing commits as-is. Named-empty-commit and
`git tag`–based labeling are deferred.

## Per-brain opt-out

The manifest carries `autosave: on|off` (default on). Set
`autosave: off` in `.brain/config.md` to revert the brain to the
pre-#30 model (manual `git commit`, immediate push on ref change,
no fsnotify watcher).

`brain.Manifest.AutosaveEnabled()` is the single read site; default
is on for any manifest that predates the field.

## Architecture interaction

For brains with autosave on:

- `AutoCommitter` owns the push debouncer for that brain.
- RefWatcher still watches `.git/refs/heads/main`, but events route
  to `AutoCommitter.NotifyRefChange()` instead of triggering an
  immediate `PushBrain`. So manual `git commit` from the operator's
  shell flows through the same 60s window as autosave commits.
- Periodic fetch (5s, see #30 commit `8ddc8dc`) is gated on a cheap
  ls-remote check — see "Pull-side negative cache" below.

For brains with `autosave: off`: behavior reverts to the pre-#30
shape — RefWatcher → immediate push.

## Pull-side negative cache

On encrypted (gcrypt) remotes, `git fetch origin` always invokes
`gpg --status-fd 3 -q -d` to decrypt the manifest, even when nothing
upstream changed — gcrypt's transport can't ask the server "did
anything move?" without downloading and decrypting first. At N
brains × 5s ticks this dominated CPU in the field (gpg-agent at 90%+,
load avg 16+ on a 12-core machine; see #34).

The cheap "did anything change?" probe sidesteps gcrypt entirely:

- gcrypt stores encrypted content on the **regular `refs/heads/master`**
  branch of the underlying git server. The commit SHA on that branch
  *is* the unique fingerprint of the encrypted state.
- `refs/heads/keys` (nous#23 pubkey distribution) moves independently;
  the keys-sync path needs to see that motion too.
- `git ls-remote <gcrypt-stripped-url>` returns both SHAs over the
  standard smart protocol — no gpg invocation, microseconds of CPU.

`Watch()` keeps a `lastSeenRefs map[brain]snapshot` in process memory.
Each tick, per brain: take a fresh ls-remote snapshot; if it matches
last seen, skip the entire per-brain block (no fetch, no keys sync, no
auto-admit). Cache updates only after a clean `PullBrain`, so a
transient fetch error doesn't poison subsequent ticks. ls-remote
errors fall through to the existing fetch path.

Operator-visible at `--verbose`: `brainsync: <repo> no remote changes
(skip)` per skipped brain per tick.

## What's deferred (follow-up issues)

- **Squash-on-push**: collapse consecutive `autosave:`-prefix
  commits into one at push time, so the remote sees clean history
  while local keeps the granular safety net. Targeted at the
  "noisy `git log` on the remote" concern raised in #30 spec.
  Tracked separately.
- **Pluggable substrate**: the "untracked = explicit gesture" rule
  is a seam pointing at a future where the substrate isn't
  necessarily git (syncthing, dropbox, etc.). Not designed here.

## Pointers

- `nous/lib/brainsync/autocommit.go` — `AutoCommitter` state machine.
- `nous/lib/brainsync/watch.go` — wire-up: per-brain committer +
  RefWatcher routing.
- `nous/lib/brain/enclosing.go` — `EnclosingBrain` walk for the CLI.
- `nous/lib/brain/manifest.go` — `AutosaveEnabled()`.
- `nous/cmd/nous/push.go` — the `nous push` command.
- `nous/workshop/issues/000030-autosave-and-nous-push.md` — spec.
- `nous/lib/brainsync/lsremote.go` — `RemoteRawURL` + `LsRemoteRaw`
  for the pull-side negative cache.
- `nous/workshop/issues/000034-brain-poll-ls-remote-negative-cache.md`
  — root cause + fix.
