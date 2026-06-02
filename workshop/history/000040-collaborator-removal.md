---
id: 000040
status: done
deps: []
github_issue:
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

## Spec

### Fix A (M1, now) — resolve login BEFORE deleting its source

In `RemoveRecipient`, resolve the login up front (before `RevokePubkey`), trying
all sources: verified.yaml (from `RemoveVerifiedFor`) → keys branch
(`LoginForFingerprint`) → peer sidecar (`identity.LoadPeerMeta`). Carry it to the
collaborator step. When still unresolved on a brain WITH a remote, set
`LoginUnresolved` so the caller warns loudly + prints the manual
`gh api -X DELETE …/collaborators/<login>` hint (no silent skip).

### Fix B (M2) — login-keyed removal spanning all states

A per-brain "remove this person" that works regardless of lifecycle state, keyed
by GitHub login (resolving fp when present):
1. cancel any pending invitation (`RepoPendingInvitations` → `DeleteRepoInvitation`),
2. remove the GitHub collaborator (`gh.RemoveCollaborator`),
3. if the resolved fp is a manifest recipient, do the full recipient removal via
   the shared core.

**Surface — operator decision (see Log):** (a) extend `recipient remove` with a
`--login` mode; (b) a distinct `nous brain collaborator remove <login>` + a TUI
key on pending-invitation rows; (c) both. Per-brain only — fan-out + ban list
stay nous#37.

## Done when

- Removing a manifest recipient revokes their GitHub collaborator reliably
  (login resolved from any of the three sources; loud manual hint if truly
  unresolvable) — no silent skip.
- An operator can remove a pending-invited or accepted-but-not-admitted person
  from `nous brain` (invitation cancelled + collaborator removed).
- CLI and TUI share one implementation.
- e2e: `brain-vm-e2e.sh` asserts collaborator revoke fires for an auto-admitted
  recipient (login resolved without a verified.yaml entry) where feasible;
  GitHub-only paths verified in the durable dogfood.

## Plan

- [x] M1 (Bug A): reorder login resolution before `RevokePubkey` in
  `RemoveRecipient` + peer-sidecar source + `LoginUnresolved` loud surface.
  Build/vet + e2e green (no regression). The GitHub-revoke effect needs a real
  remote → confirmed in the durable dogfood retest.
- [x] M2 lib+CLI (Bug B): `brain.FingerprintForLogin` + `brainsync.RemovePerson`
  (recipient / pending / collaborator-only) + `cancelPendingInvitation`;
  `nous brain recipient remove` accepts a login and acts at any stage. Surface =
  "unified recipient remove" (operator-chosen 2026-06-02). Build/vet + tests +
  e2e green.
- [ ] M2c (Bug B, TUI): remove/cancel action on pending-invitation rows in
  `nous brain` detail view — a small pick→confirm→apply sub-model calling
  `brainsync.RemovePerson(login)`. Deferred from the M2 session (fiddly
  bubbletea; capability already reachable via the CLI).

## Log


- 2026-06-02: closed — unified recipient remove acts at any lifecycle stage (recipient/pending/collaborator) by fp or login; Bug A (silent collaborator-revoke skip) + codex#1 (keys-branch strip on non-recipient path) fixed; build/vet/tests/e2e green. M2c (TUI pending-row) deferred to its own follow-up. --force: deferred M2c + trailers; codex review covered span
### 2026-06-02

Filed from the dogfood. Bug A is an ordering regression in nous#38's
`RemoveRecipient` (login resolved after the keys-branch source is deleted; no
sidecar fallback). Bug B is a genuine gap — removal only ever targeted manifest
recipients. State-machine map captured above. M1 (A) lands now; M2 (B) needs the
surface decision.
