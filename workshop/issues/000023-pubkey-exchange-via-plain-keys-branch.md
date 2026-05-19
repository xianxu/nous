---
id: 000023
status: open
deps: [000012, 000022]
created: 2026-05-19
updated: 2026-05-19
estimate_hours: 6
---

# Automate pubkey exchange via a plain `keys` branch on the gcrypt repo

## Problem

The shared-brain dogfood (`nous#12`) currently requires a manual
bidirectional pubkey sneakernet between every pair of recipients,
because gcrypt signs every manifest with the producer's GPG key and
the consumer must have that pubkey in their keyring to verify before
decryption. The failure mode is loud — `gpg: Can't check signature:
No public key` mid-clone — but the recovery is annoying:

```
on operator's machine:    nous identity export > xianxu.pub
                          (sneakernet xianxu.pub to peer)
                          (voice/in-person: read fingerprint last-8)
on peer's machine:        nous identity import xianxu.pub
                          (type last-8 to confirm)
                          git clone gcrypt::ssh://...
```

For an N-recipient brain, this is N(N-1)/2 manual exchanges. For a
two-person family brain that's tolerable (one exchange). For anything
larger — or for adding the third+ recipient to an existing brain —
it's a real friction tax that compounds with every addition.

The cost lands disproportionately on adoption: an operator who's
just admitted a peer and pushed shouldn't have to instruct them
through a separate file-exchange ceremony to make `git clone` work.

## Insight

Public keys are public. The only audience that needs them — the
brain's recipient set — is exactly the GitHub-collaborator set we've
already granted read access to. There's no confidentiality argument
for keeping them out of the GitHub repo; the "everyone knows
everyone's pubkey" property is desired, not undesired.

WhatsApp's identity-exchange UX is the right reference: the server
brokers pubkey distribution by default; in-person fingerprint
comparison is an opt-in escape valve for paranoid users. nous can
do the same — automate distribution via the substrate, keep
verify-fingerprint as an *available* ceremony rather than a *required*
one.

## Approach: plain `keys` branch on the gcrypt repo

Each shared-brain GitHub repo carries two parallel ref trees:

```
refs/gcrypt/main      ← gcrypt's encrypted state (existing)
refs/heads/keys       ← plain git branch with pubkeys (new)
```

GitHub doesn't care that one branch is encrypted blob soup and the
other is plain git objects — they're independent ref paths. The
`keys` branch contains:

```
keys/
  <40-hex-fingerprint-1>.asc     # ASCII-armored pubkey
  <40-hex-fingerprint-2>.asc
  ...
README.md                         # optional: "this is the pubkey
                                  # directory for brain X; auto-
                                  # imported by nous on clone"
```

Each ASCII-armored file is a self-contained pubkey: `gpg --import
<fp>.asc` lands it in the recipient's keyring. The fingerprint in
the filename is redundant with the pubkey's content, but useful for
human inspection (`ls keys/` shows who's in the set without
parsing).

### Mechanical wrinkle: gcrypt remote helper intercepts `origin`

`remote.origin.url = gcrypt::ssh://...` means every `git push origin`
goes through gcrypt's helper, which would encrypt the keys branch and
defeat the point. Solution: a second remote (`keys-remote`) with the
plain SSH URL, pushing the keys branch via that remote.

**This is an implementation detail that recipient-management code
must never see.** Operators don't think about it; higher-level
callers don't either. It lives entirely inside the abstraction
introduced below.

### Abstraction: GitHub-as-file-store

The keys branch is conceptually a tiny key-value file store hosted
on the same GitHub repo as the brain. The interface higher layers
see is simple list / put / delete; the implementation handles
everything else (orphan branch creation, plain-remote configuration,
shallow clones, fetch-modify-push retries on conflict, etc.).

```go
// lib/brain/filestore — generic plain-branch storage on a GitHub
// repo, suitable for any unencrypted metadata that needs to ride
// alongside a gcrypt-encrypted brain. Today's only caller is pubkey
// exchange; future callers might be member rosters, brain-level
// settings, etc.
type Store interface {
    // List returns name → content for every file in the store.
    // Refreshes from the remote before returning.
    List(ctx context.Context) (map[string][]byte, error)

    // Put writes or overwrites a file. Atomic from the operator's
    // perspective: either the new content is published or an error
    // is returned. Fetches latest before pushing; retries on
    // non-fast-forward.
    Put(ctx context.Context, name string, content []byte) error

    // Delete removes a file. Idempotent — succeeds silently when
    // the file isn't present.
    Delete(ctx context.Context, name string) error
}

// Open returns a Store backed by `branch` on the brain's GitHub
// repo. Configures the plain-remote if it isn't already set,
// initializes the branch as an orphan if it doesn't exist on the
// remote, maintains a local working copy under
// `<brainRoot>/.git/nous-filestore/<branch>/`.
func Open(brainRoot, branch string) (Store, error)
```

Implementation properties:

- **Local working copy hidden under `.git/`** so it doesn't appear
  in the operator's working tree or get accidentally committed to
  the gcrypt-encrypted main branch.
- **Shallow clone (depth=1, single-branch)** — the keys directory
  is small, no need for full history. Saves bandwidth + disk.
- **Conflict retry**: if `push` is rejected (someone else pushed
  in the gap between fetch and push), Re-fetch, re-apply, retry up
  to 3 times before erroring. Operator-invisible.
- **No exposed git/remote vocabulary**: callers use string filenames,
  byte slices, and `context.Context`. They never see "branch",
  "remote", "commit", "push".

### Caller usage (after the abstraction)

```go
// nous brain recipient add — committing a new pubkey to the keys store:
store, err := filestore.Open(brainRoot, "keys")
if err != nil { return err }
defer store.Close()

armor, err := identity.Export(newFingerprint)
if err != nil { return err }

if err := store.Put(ctx, newFingerprint + ".asc", []byte(armor)); err != nil {
    return fmt.Errorf("publish pubkey: %w", err)
}
```

```go
// brain-sync periodic fetch — auto-import any new pubkeys:
files, err := store.List(ctx)
if err != nil { return err }
for name, content := range files {
    if strings.HasSuffix(name, ".asc") {
        _, _ = identity.Import(string(content))  // idempotent at gpg's level
    }
}
```

The git branch, the plain remote, the orphan-branch initialization,
the shallow clone, the conflict retries — none of those show up in
the calling code. That's the contract.

## Flow changes

### `nous brain new <path> --fingerprint <peer>`

After provisioning the gcrypt remote and authoring `.brain/config.md`:

1. Configure `remote.keys-remote.url` = the plain SSH URL of the
   same GitHub repo.
2. Create a `keys` branch (orphan; no shared history with main).
3. Commit `keys/<operator-fp>.asc` + `keys/<peer-fp>.asc` to the
   keys branch.
4. `git push keys-remote keys:refs/heads/keys`.

### `nous brain recipient add <brain> <pubkey>`

After the existing verify-fingerprint ceremony + gcrypt manifest
rewrite + push:

1. Check out the `keys` branch (or fetch + create local tracking
   branch if not present).
2. Commit `keys/<new-fp>.asc`.
3. Push to `keys-remote`.

### `nous brain recipient remove <brain> <fp>`

After existing remove logic:

1. Check out `keys` branch.
2. Delete `keys/<fp>.asc`.
3. Push to `keys-remote`.

### New: peer clone flow

Peers run something like:

```sh
nous brain clone gcrypt::ssh://git@github.com/owner/brain.git
```

Which internally:

1. Resolves the plain URL from the gcrypt URL (strip `gcrypt::`
   prefix).
2. `git clone --branch keys --single-branch <plain-url> /tmp/keys`
3. `gpg --import /tmp/keys/keys/*.asc`
4. `rm -rf /tmp/keys`
5. `git clone gcrypt::...` (the actual brain).
6. After clone, copy the gcrypt URL to `remote.origin.url` AND set
   up `remote.keys-remote.url` so future fetches pick up new
   recipients.

If the operator prefers raw `git clone`, that still works — they
just have to manually fetch + import the keys branch first. The
`nous brain clone` wrapper is the convenience path.

### Periodic key refresh

The brain-sync watcher (nous#22's auto-discovery loop) can be
extended to fetch the keys branch alongside the gcrypt main branch
on every tick. When new pubkeys appear in `keys/`, auto-import
them. Cost: one extra `git fetch` per brain per cycle, negligible.

This means: when a new recipient is added by anyone, every existing
recipient picks up the new pubkey within ~60s without operator
intervention.

## Verify-fingerprint ceremony — becomes opt-in

Today the ceremony is mandatory at `nous identity import` and
`nous brain recipient add`. After this change:

- **At `nous brain recipient add`**: still required. Admitting a
  recipient is a delegation-of-trust event that should remain
  explicit.
- **At first clone (peer side)**: the auto-import path skips the
  ceremony. Trust is delegated to whichever party added them as a
  recipient (which they trust by definition — they wouldn't be
  joining the brain otherwise).
- **New verb `nous brain recipient verify <fp>`**: an explicit
  command that prints the imported pubkey's fingerprint and offers
  a side-by-side compare against an OOB-provided value. The escape
  hatch for paranoid users.

This loosens the boundary slightly — a malicious operator who has
push access to the keys branch could insert a fake pubkey for a
peer and impersonate them. But the threat surface for that is the
same as for impersonating a recipient on the gcrypt side: requires
push access to the GitHub repo AND a valid recipient key already.
If the operator is compromised, the keys branch is the least of
the problems.

For the family-brain case, this is the right trade-off: convenience
by default, verification on demand.

## Done when

- [ ] `lib/brain/filestore` exposes a clean List/Put/Delete API.
      No caller — including peerkeys, the brain commands, the TUI,
      or tests — references git branches, remotes, commits, or
      pushes outside the filestore package.
- [ ] `nous brain new <path> --fingerprint <peer>` publishes the
      operator + initial peer pubkeys on the first push (via
      peerkeys, which composes filestore + identity).
- [ ] `nous brain recipient add <brain> <pubkey>` publishes the
      new pubkey when admitting.
- [ ] `nous brain recipient remove <brain> <fp>` removes the
      pubkey when revoking.
- [ ] `nous brain clone <gcrypt-url>` is a new subcommand that
      handles the two-fetch dance + key import + actual clone.
- [ ] Brain-sync watcher periodically fetches the keys branch and
      auto-imports new pubkeys.
- [ ] `nous brain recipient verify <fp>` exists as the opt-in
      verify-fingerprint ceremony.
- [ ] The TUI's "Share with peers" section simplifies to: `nous
      brain clone <gcrypt-url>` and a one-liner about auto-import.
- [ ] Threat-model doc (`brain/atlas/threat-model-shared-brain.md`)
      Bootstrap section is rewritten: pubkey-exchange happens via
      the keys branch; the verify-fingerprint ceremony is opt-in
      via `nous brain recipient verify`.
- [ ] nous#12's Phase 1-3 instructions collapse from "sneakernet
      wife.pub + ceremony" to "operator shares fingerprint OOB;
      everything else is automatic."

## Out of scope

- **Migration of existing shared brains.** A brain provisioned
  before this lands won't have a keys branch. `nous brain init-keys
  <brain>` could be a one-shot migration helper that creates the
  keys branch from currently-known pubkeys (everyone listed in the
  manifest who has a corresponding `~/.gnupg` entry on the
  operator's machine). Defer until needed.

- **Cross-brain pubkey reuse.** A pubkey added to one brain isn't
  automatically known to other brains. Each brain's keys branch is
  independent. Could be solved by a per-machine `keys/` cache that
  unions all brain keys directories, but adds complexity for
  unclear payoff at family scale.

- **Non-GitHub substrates.** Syncthing-backed brains, git-daemon-
  hosted brains, or fully-offline brains don't have an obvious
  "second branch" channel. For those, fall back to the existing
  sneakernet flow. The keys-branch design is GitHub-substrate-
  specific, matching where nous actually deploys today.

- **Keyserver fallback.** Was considered as the alternative design
  (publish pubkeys to keys.openpgp.org, import by fingerprint).
  Rejected for nous's family-scale case: depending on an external
  keyserver is heavier than depending on the same GitHub repo
  that's already hosting the brain. Keyserver is fine as a future
  bolt-on if cross-brain or cross-org pubkey discovery becomes
  desirable.

## Spec

### Storage layout on the `keys` branch

Orphan branch (no shared history with `main`), single directory:

```
keys/
  0ECF6AC06E9BB6C5B928F10B5D6885D83872C2F0.asc
  XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX.asc
README.md   # generated; explains the directory's purpose
```

Filename = full 40-char uppercase fingerprint, `.asc` extension.
File body = output of `gpg --armor --export <fp>` (the same blob
`nous identity export` produces today).

`README.md` is operator-facing; explains the directory's purpose
and pointers to nous documentation. Auto-generated on first
keys-branch creation; not regenerated on subsequent pushes (so
operator edits stick).

### Library additions

- **`lib/brain/filestore/`** (new package, see Abstraction above):
  generic GitHub-branch-as-file-store with the `Store` interface
  (List / Put / Delete). All git plumbing — second remote, orphan
  branch, shallow clone, fetch-push retries — lives here and only
  here.
- **`lib/brain/peerkeys.go`** (new, thin): convenience helpers
  layered on top of filestore. `PublishPubkey(brainRoot, fp)`,
  `RevokePubkey(brainRoot, fp)`, `ImportAllPubkeys(brainRoot,
  ctx)`. These are 5-10 lines each — they're just filestore calls
  with `.asc` filename conventions, plus invoking
  `identity.Import` on the results.
- **`lib/brain/status.go`**: extend Status with a
  `LocalPubkeysKnown int` (count of pubkeys imported from the
  filestore on last sync) for the TUI to show "all peers' pubkeys
  imported" vs. "fetching N..." on first run.

### New CLI verbs

- **`nous brain clone <gcrypt-url>`**: the convenience clone path.
  Resolves plain URL, fetches keys, imports, clones.
- **`nous brain recipient verify <brain> <fp>`**: opt-in verify-
  fingerprint ceremony for paranoid users.
- **`nous brain init-keys <brain>`** (out of scope above, but
  worth listing for tracking): one-shot migration of an existing
  shared brain to the keys-branch layout.

### Changes to existing CLI verbs

- **`nous brain new`**: extends to set up the keys remote + push
  initial pubkeys to the keys branch.
- **`nous brain recipient add`**: extends to also commit + push
  the new pubkey to the keys branch.
- **`nous brain recipient remove`**: extends to also delete from
  the keys branch.

### TUI changes

The "Share with peers" section in `lib/tui/brain/detail.go`
simplifies to:

```
Share with peers
  nous brain clone gcrypt::ssh://git@github.com/owner/brain.git
  (pubkeys auto-imported from the keys/ branch on first clone;
   verify any peer's identity later with `nous brain recipient
   verify <fp>`)
```

## Plan

- [x] M1: **`lib/brain/filestore/` foundation**. Pure abstraction
      with no nous-specific knowledge — generic
      "GitHub-repo-branch as file store" library. Implements Open
      / List / Put / Delete with all the git plumbing hidden:
      plain-remote configuration (gcrypt:: prefix stripping),
      orphan-branch init when remote branch absent, shallow clone
      (depth=1, single-branch) when present, fetch-modify-push,
      conflict retries (3-attempt withRetry). Workdir lives at
      `<brainRoot>/.git/nous-filestore/<branch>/` — hidden from
      the gcrypt-encrypted working tree. Committer identity
      inherited from brain's git config. Unit tests use a local
      bare-repo fixture as the "remote" (file:// URLs) — zero
      GitHub or network calls. 10 tests, all passing.

- [x] M2: **`lib/brain/peerkeys.go` thin layer**. Convenience
      helpers (PublishPubkey, RevokePubkey, ImportAllPubkeys) that
      compose filestore + identity packages. Behaviors uniquely
      owned by peerkeys: filename convention (uppercase fp + .asc
      suffix), non-.asc filter in ImportAllPubkeys, idempotent
      delete on missing entries. Dedicated unit tests skipped —
      peerkeys is pass-through glue; end-to-end coverage starts
      in M3 (wire into `nous brain new`).

- [ ] M3: Wire into **`nous brain new`** (operator + initial peer
      pubkeys published via filestore on the first push). Verify
      manually: a peer can fetch the keys branch and gpg-import
      both pubkeys.

- [ ] M4: Wire into **`nous brain recipient add` / `remove`**.
      After admit-and-push, peerkeys.PublishPubkey; after revoke,
      peerkeys.RevokePubkey. Existing dogfood tests adapted to
      assert the keys branch sees the change.

- [ ] M5: **`nous brain clone <gcrypt-url>`** subcommand. Resolves
      plain URL, uses filestore to fetch + import all peer pubkeys,
      then `git clone gcrypt::...`. Familiar shape for operators
      who know git.

- [ ] M6: **Brain-sync watcher integration**. Per-brain ticker
      also fetches the keys branch (via filestore) and runs
      ImportAllPubkeys. New entries get added to keyring within
      one tick (≤60s) without operator action. Log new pubkeys at
      Info; silent on no-change.

- [ ] M7: **`nous brain recipient verify <brain> <fp>`** verb.
      Read pubkey from keyring, render fingerprint side-by-side
      against OOB-provided value. No state change. The opt-in
      ceremony that replaces the mandatory verify-fingerprint at
      `nous identity import` for keys-branch-discovered pubkeys.

- [ ] M8: TUI "Share with peers" simplification + threat-model
      doc rewrite + nous#12 Phase 1-3 collapse.

M1 is the foundation; M2-M7 each consume the abstraction without
needing to know about git internals. M8 is the documentation sweep.

The interface separation is load-bearing: M3-M6 should all read as
"call PublishPubkey here" / "call ImportAllPubkeys here" — not
"checkout the keys branch, write a file, commit, push to
keys-remote". If those operations leak into the higher-level code
during implementation, that's a sign the filestore API needs to
grow (likely Refresh or Snapshot) rather than letting the
abstraction crack.

## Test plan

End-to-end on the tart VM, mirroring the #12 dogfood:

1. On host: `nous brain new ~/workspace/brain-shared-test --fingerprint $EMMA_FP`.
   Verify keys branch was pushed (`git ls-remote ssh://git@github.com/xianxu/brain-shared-test.git refs/heads/keys`
   shows a commit).
2. On VM: `nous brain clone gcrypt::ssh://git@github.com/xianxu/brain-shared-test.git`.
   Verify: keys branch fetched first, both pubkeys imported (`gpg
   --list-keys` shows xianxu + emma), gcrypt clone succeeds without
   "No public key" error.
3. On host: add a third recipient via `nous brain recipient add`.
   Verify keys branch updated.
4. On VM: wait for brain-sync's next tick. Verify the new
   recipient's pubkey is in the local keyring without manual
   intervention.
5. On VM: `nous brain recipient verify <new-fp>`. Verify the
   ceremony renders the fingerprint and accepts a matching last-8.

## Notes

The 2026-05-19 dogfood iteration surfaced this issue: cloning
emma's brain from her tart VM hit the "No public key" wall mid-
clone, and the recovery path was operator-side sneakernet of
xianxu's pubkey + ceremony on emma's side. The "Share with peers"
TUI section was updated to spell out the bidirectional exchange
explicitly (commit `db60592`), but that's tactical — the
strategic answer is to remove the friction, not document it.

The MITM threat model on the keys branch is bounded: an attacker
who can push to the keys branch is already a GitHub collaborator
with push access, which means they already have keys-branch write
capability AND gcrypt-side write capability (assuming they have a
recipient GPG key in the manifest). The marginal harm of also
being able to push fake pubkeys is real but additive on a threat
they already have. WhatsApp's "verify on suspicion" UX model is
the right fit.

## Log

### 2026-05-19 — M2 landed
`lib/brain/peerkeys.go` — 80-line glue layer. Three exported
functions:

- `PublishPubkey(ctx, brainRoot, fp)` — exports the pubkey from
  the local GPG keyring, opens the brain's keys filestore, Puts
  `<UPPERCASE-FP>.asc`. Idempotent at the filestore layer
  (identical content → no commit).
- `RevokePubkey(ctx, brainRoot, fp)` — opens the keys filestore,
  Deletes `<UPPERCASE-FP>.asc`. Doesn't touch the local keyring
  (operator's concern).
- `ImportAllPubkeys(ctx, brainRoot) (imported, errs, err)` —
  Lists every file in the keys filestore, filters for `.asc`
  suffix, runs `identity.Import` on each. Per-file Import errors
  collected into `errs` without aborting; only a Store.List
  failure surfaces as `err`.

The filename convention (`<UPPERCASE-FP>.asc`) is owned here. All
git vocabulary — branches, remotes, pushes, commits — stays
encapsulated in M1's filestore package. Callers from M3 onward
only touch fp strings and bytes.

Dedicated unit tests skipped: peerkeys is glue; end-to-end
coverage starts in M3 when `nous brain new` wires it into the
brain-provision flow.

### 2026-05-19 — M1 landed
`lib/brain/filestore/` foundation. Interface is exactly the
Spec'd shape: `Open(brainRoot, branch) → Store`; `Store.List /
Put / Delete / Close` on byte-slice contents. No git verbs leak
out of the package.

Implementation notes that didn't make it into the spec but are
worth knowing for downstream callers:

- `readPlainOriginURL` strips the `gcrypt::` prefix; for a brain
  whose origin already lacks the prefix (rare; non-encrypted
  brain) it's a no-op. Errors when origin is unset entirely.
- `ensureWorkdir` discriminates three cases via `ls-remote`:
  workdir exists → reuse; branch exists remotely → shallow clone;
  branch absent → init local empty repo with branch as HEAD,
  push happens on first Put.
- `refresh` is called pre-flight by every operation (List / Put /
  Delete). On orphan-branch first run, fetch fails with "couldn't
  find remote ref" — caught + treated as "no remote state yet."
- Push retries are simple: any error → re-fetch + retry, up to
  3 attempts. Overcorrects vs. distinguishing non-fast-forward
  from real failures, but the recovery path (re-fetch + re-apply)
  is the same for both, so the simplicity is worth it.
- Flat namespace: Put rejects names containing `/` or `\`. Nested
  directories aren't part of the contract today (the only caller
  in sight is `keys/<fp>.asc`, which is flat).

Tests (10 total, all passing) — use file:// bare repos for the
"remote" side, no network or GitHub:
- URL prefix handling (gcrypt:: stripped vs. plain unchanged)
- Open errors on missing remote
- Put/List/Delete roundtrip
- Put idempotence on identical content (no empty commits)
- Put overwrites existing content
- Delete idempotence on absent files
- Path-separator rejection
- Multi-client visibility (publisher → consumer sees content)
- Refresh propagation (new entries picked up on next List)
