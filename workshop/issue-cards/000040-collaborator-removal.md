---
id: '000040'
status: done
created: 2026-06-02
updated: 2026-06-02
estimate_hours: 3
actual_hours: 3
---

# collaborator removal: reliable revoke + remove at any lifecycle stage

## Problem

Dogfood (2026-06-02): removing yingtest42 (a recipient on brain-family) via the
new build did NOT revoke her GitHub collaborator status — she kept access to
`xianxu/brain-family`. And once invited-but-not-accepted, there is no way to
cancel/remove her from `nous brain`. Two distinct bugs.

### Membership state machine (the multiple stores)

A person's membership is spread across stores that can drift:

| Store | Key | Written by |
|---|---|---|
| GitHub collaborator (accepted access) | login | invite (add) / remove / leave |
| GitHub pending invitation | invitation id | invite (create) / `DeleteRepoInvitation` |
| manifest `recipients:` | fingerprint | recipient add, auto-admit, remove |
| keys branch `<login>.asc` / `<FP>.asc` | login / fp | join, recipient add, RevokePubkey |
| verified.yaml | login→fp | verify, remove; **auto-admit does NOT write it** |
| peer sidecar `~/.config/nous/peers/<fp>.json` | fp→login | identity import (`--github-user`) |
| local GPG keyring | fp | import |

Lifecycle: invite → (pending invitation) → invitee accepts (collaborator) +
`nous brain join` (publishes `<login>.asc`) → operator auto-admit
(`AutoAdmitFromKeysBranch`, no verified.yaml write) → manifest recipient.

### Bug A — collaborator revoke silently skipped (ordering)

`brainsync.RemoveRecipient` resolves the GitHub login only at the collaborator
step, AFTER `RevokePubkey` has already deleted the keys-branch `<login>.asc`
that `LoginForFingerprint` reads. Auto-admitted recipients have no verified.yaml
entry either, so both login sources come up empty → `if login != ""` is false →
`gh.RemoveCollaborator` is **skipped with no error**. The peer sidecar
(`identity.LoadPeerMeta(fp).GithubUser`) is never consulted.

### Bug B — no removal for non-recipient lifecycle states

All removal requires a manifest recipient (`MatchRecipient` first). There's no
operation to cancel a **pending invitation** (sent, not accepted) or remove an
**accepted collaborator not yet admitted**, even though `gh.DeleteRepoInvitation`
+ `gh.RemoveCollaborator` exist.
