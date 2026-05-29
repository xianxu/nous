# Brain Topology Ladder

How a brain gets from "a folder on my laptop" to "shared, encrypted, on
GitHub" — and why those are *rungs*, not modes. Introduced in `nous#33`.

## Two orthogonal axes

A brain has two independent properties that are easy to conflate:

- **Privacy** — *who can decrypt it.* Derived from the recipient list:
  one recipient = private, 2+ = shared. (See `brain-manifest.md`.)
- **Topology** — *where the ciphertext lives, and whether there's an
  upstream at all.* This is the new axis `nous#33` made first-class.

A local-only brain and a GitHub-backed solo brain are **both private**
(one recipient) but a full rung apart on topology. Before `nous#33` the
TUI labelled both `private` — indistinguishable. Now the label reflects
topology.

## The ladder

```
nous brain new ──▶ local ──[publish]──▶ private ──[invite]──▶ shared
   (git init)                (gh repo)              (collaborator add)
```

| Rung | Condition | What it means |
|------|-----------|---------------|
| **local** | no `remote.origin.url` | a git repo on this device only. gcrypt never engages (it only encrypts on push to a gcrypt remote), so the working tree + objects are **plaintext** — FileVault is the at-rest protection. No daemon watches it (`brainsync` `isWatchable` excludes single-recipient + no-remote). |
| **private** | remote + 1 recipient | gcrypt-encrypted backup on GitHub, solo. |
| **shared** | remote + 2+ recipients | gcrypt-encrypted on GitHub, multiple recipients. |

The ladder is **one-directional in the UI**: there's no unpublish. Once
ciphertext is on GitHub, "going back to local" doesn't un-leak it.

## The verbs

- **`nous brain new <path>`** (no flags) → makes a **local** brain:
  `git init`, go.mod (substrate wiring), manifest (single recipient,
  `sync_substrate: none`, no remote), first commit. No GitHub, no
  network, no GPG passphrase. This is the lightweight default.
  (`lib/brain.InitLocal` + `cmd/nous/brain_new.go` `provisionLocal`.)
- **`nous brain publish [--brain PATH]`** → **local → private**: create a
  private GitHub repo, wire the gcrypt remote (encrypted to the
  operator's key), push. Refuses if a remote already exists.
  (`cmd/nous/brain_publish.go` + `scripts/publish-brain.sh`.)
- **`nous brain invite <login>`** → **private → shared**: GitHub
  collaborator invite + auto-admit on their join. Unchanged by `nous#33`
  — it already worked the moment a remote existed. (`recipient-onboarding.md`.)

`nous brain new --recipient/--fingerprint` still provisions a shared
GitHub brain directly (the multi-recipient path, untouched by `nous#33`)
— a shortcut that skips the rungs. The ladder is the incremental path;
the flags are the all-at-once path.

## TUI surfacing

`nous brain` (the TUI) makes the ladder legible:

- **List**: the per-brain label is the rung (`local` / `private` /
  `shared · N`), derived from has-remote × recipient count
  (`lib/tui/brain/rung.go`).
- **Detail**: a rung-based header and a **state-gated action footer** —
  it offers only the current rung's next gesture (local → `p publish`,
  private → `a invite`, shared → `a`/`r`/`l` manage). Actions that can't
  work at this rung aren't offered.

## Boundary worth stating

A local brain's working tree is plaintext. If the operator drops it into
an externally-synced folder (iCloud, Dropbox), **that host sees
plaintext** — the brain does not manage or model external sync, and the
"host sees only ciphertext" invariant that holds for published brains
does *not* apply. See `brain/atlas/threat-model-shared-brain.md`.

## Pointers

- Manifest schema + privacy axis: `brain-manifest.md`
- gcrypt mechanics (engaged at publish): `gcrypt-brain-encryption.md`
- Recipient onboarding (the shared rung): `recipient-onboarding.md`
- Security posture + threat boundaries: `brain/atlas/threat-model-shared-brain.md`
- Issue: `workshop/history/000033-*` (once archived) / `workshop/issues/000033-*`
