# Collaborator / recipient lifecycle

How a person's membership in a shared brain is managed across its lifecycle.
The **defended shape + invariants + state diagram** live in the target
`workshop/targets/collaborator-state-machine.md` — read that for the model; this
atlas entry is the code map.

## Two layers

- **GitHub (transport/ACL)**, keyed by login: pending *invitation* or accepted
  *collaborator*. `lib/gh/gh.go` — `InviteCollaborator` (clears stale invite then
  PUT, so re-invite re-sends; list/delete failures are hard errors — a swallowed
  failure lets the PUT silently no-op, #41 #11), `RemoveCollaborator`,
  `RepoPendingInvitations`, `DeleteRepoInvitation`, `CollaboratorPermission`.
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
  re-keys; verified.yaml gates drift. On a **key rotation** — a login's
  `<login>.asc` overwritten with a new fp — it supersedes: evicts the old fp
  (looked up in the manifest `recipient_logins` map) from `recipients:`, admits
  the new one, updates the map, and surfaces it as `AdmittedRecipient.
  SupersededFingerprint` (the watcher logs "rotated from <old8>"). The verified.yaml
  drift gate still wins first — an operator-pinned login refuses the rotation
  until re-verified (`nous#41` #7/#8).
- **Remove (any stage)** — `nous brain recipient remove <fp|login>`
  (`cmd/nous/brain_recipient.go`) + TUI `recipient_remove.go` →
  `brainsync.RemovePerson` (`lib/brainsync/recipient.go`). Recipient path runs
  the full strip (manifest + re-key + verified.yaml + keys branch + collaborator
  + pending invite) via `RemoveRecipient`; non-recipient path (pending /
  collaborator-only) cancels the invite + revokes collaborator + strips any
  keys-branch pubkey. login↔fp resolved via `brain.LoginForFingerprint` /
  `FingerprintForLogin` + peer sidecar — `FingerprintForLogin` reads the
  keys-branch `<login>.asc` then the peer sidecar, NOT verified.yaml (#41 #3).
  The store-strip sequence (verified → resolve-login → manifest re-key push →
  keys-branch → collaborator) lives in one shared `stripMember`
  (`lib/brainsync/recipient.go`) that both `RemoveRecipient` and `LeaveBrain`
  call, so the two can't drift (target invariant #4).
- **Leave (self)** — `nous brain leave` (`cmd/nous/brain_leave.go`) →
  `brainsync.LeaveBrain`, which routes through `stripMember` so leaving clears
  EVERY store (not just the manifest) — a lingering keys-branch pubkey would let
  a peer's auto-admit resurrect the leaver (#41 #12).

## Concurrency + drift

- **Membership push races** — `brainsync.pushMembershipChange` (`lib/brainsync/recipient.go`)
  wraps every membership mutation's commit+push: on a rejected push (a concurrent
  operator pushed first) it `ResetToRemoteMain` (fetch + `reset --hard origin/main`)
  and re-applies the mutation on the merged state, bounded retries. Membership
  changes are idempotent set-ops so this converges (unlike content edits, which go
  through `Resolve`). `stripMember` (remove/leave) and `autoAdmitBrain` (the daemon)
  both push through it (`nous#41` #6).
- **Login-rename detection** — `brain.DetectLoginDrift` (pure) flags recorded logins
  (`recipient_logins` keys) that are no longer current GitHub collaborators
  (`gh.ListCollaborators`); `nous brain recipient list` surfaces the warning. A
  GitHub login rename leaves the old login orphaned in the keys branch /
  recipient_logins; auto-heal is deferred (`nous#41` #10, detection-only).

## Invariants (see the target for the full list)

Removal clears every store (no resurrection); resolve login↔fp before
destructive deletes (keys-branch canonical, not verified.yaml); one shared lib
impl per op for CLI + TUI; revocation is forward-only.

## Open hardening

`nous#41` tracks the codex-review findings. **M1 landed** (#3 verified.yaml is no
longer a login→fp mapping source, #11 re-invite list/delete are hard errors, #12
leave clears every store via the shared `stripMember`). **M2 landed** (#7/#8 key
rotation / one-fp-per-login via the manifest `recipient_logins:` map + auto-admit
supersede). **M3 landed** (#6 membership push-race pull-rebase-retry,
#10 login-rename detection). **Still to land:** target doc reconciliations (M4).
Cross-brain revocation + ban list is `nous#37`.
