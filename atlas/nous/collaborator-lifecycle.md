# Collaborator / recipient lifecycle

How a person's membership in a shared brain is managed across its lifecycle.
The **defended shape + invariants + state diagram** live in the target
`workshop/targets/collaborator-state-machine.md` — read that for the model; this
atlas entry is the code map.

## Two layers

- **GitHub (transport/ACL)**, keyed by login: pending *invitation* or accepted
  *collaborator*. `lib/gh/gh.go` — `InviteCollaborator` (clears stale invite then
  PUT, so re-invite re-sends), `RemoveCollaborator`, `RepoPendingInvitations`,
  `DeleteRepoInvitation`, `CollaboratorPermission`.
- **Crypto (decryption)**, keyed by GPG fingerprint: manifest `recipients:` +
  the `keys` branch pubkey(s). The keys-branch `<login>.asc` is the canonical
  login↔fp link. `verified.yaml` is offline-signature-verification only (not the
  mapping).

## Surface

- **Invite** — `nous brain invite <login>` (`cmd/nous/brain_invite.go`) +
  TUI `lib/tui/brain/invite_collab.go` → `gh.InviteCollaborator`.
- **Join / accept + publish** — `nous brain join` (`cmd/nous/brain_join.go`) +
  TUI `accept_invite.go` → `gh.AcceptInvitation` + `brain.PublishOwnPubkeyToRemote`.
- **Auto-admit** — `lib/brain/autoadmit.go` `AutoAdmitFromKeysBranch` +
  `lib/brain/peerkeys.go` `ImportAllPubkeys`, driven by the brain-sync watcher
  (`lib/brainsync/watch.go`). Appends keys-branch pubkeys to the manifest +
  re-keys; verified.yaml gates drift.
- **Remove (any stage)** — `nous brain recipient remove <fp|login>`
  (`cmd/nous/brain_recipient.go`) + TUI `recipient_remove.go` →
  `brainsync.RemovePerson` (`lib/brainsync/recipient.go`). Recipient path runs
  the full strip (manifest + re-key + verified.yaml + keys branch + collaborator
  + pending invite) via `RemoveRecipient`; non-recipient path (pending /
  collaborator-only) cancels the invite + revokes collaborator + strips any
  keys-branch pubkey. login↔fp resolved via `brain.LoginForFingerprint` /
  `FingerprintForLogin` + peer sidecar.
- **Leave (self)** — `nous brain leave` (`cmd/nous/brain_leave.go`) →
  `brainsync.LeaveBrain`.

## Invariants (see the target for the full list)

Removal clears every store (no resurrection); resolve login↔fp before
destructive deletes; one shared lib impl per op for CLI + TUI; revocation is
forward-only.

## Open hardening

`nous#41` tracks the codex-review findings still to land (key rotation /
one-fp-per-login, concurrent-operator push races, login rename, leave
completeness, verified.yaml resolution order). Cross-brain revocation + ban list
is `nous#37`.
