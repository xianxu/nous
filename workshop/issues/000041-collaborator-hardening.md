---
id: 000041
status: working
deps: []
github_issue:
created: 2026-06-02
updated: 2026-06-02
estimate_hours: 5
target: collaborator-state-machine
---

# collaborator-lifecycle hardening (codex review findings)

## Problem

A read-only codex design review of `workshop/targets/collaborator-state-machine.md`
(against the implementing code + threat model) surfaced 12 findings. #1 was a live
resurrection bug, already fixed (`f2cb9de`). The rest are real drift/edge gaps in
the per-brain collaborator lifecycle and are in scope for THIS testing round.
(Cross-brain ban list stays nous#37 — not here.)

## Spec / findings

Code fixes:
- [x] **#1** non-recipient removal didn't strip keys-branch pubkey — FIXED `f2cb9de`.
- [ ] **#3** login↔fp resolution order: keys-branch first (canonical), then peer
  sidecar; stop treating `verified.yaml` as a mapping source (it's
  offline-sig-verification only). `FingerprintForLogin` + `RemoveRecipient`.
- [ ] **#11** re-invite isn't bulletproof: `gh.InviteCollaborator` only deletes
  the stale invite if listing succeeds and ignores delete failures, then PUTs →
  can still no-op (no email). Make list/delete failures hard errors.
- [ ] **#12** `leave` ≠ full removal: `LeaveBrain` clears manifest + collaborator
  but not verified.yaml or the keys-branch pubkey → the leaver's `<login>.asc`
  lingers and another operator's auto-admit can re-add them. Route through the
  same strip path.
- [ ] **#7 + #8** GPG key rotation + one-login→one-fp: an overwritten
  `<login>.asc` (new fp) admits the new fp while the OLD fp stays a recipient →
  stale recipients; no invariant enforces one active fp per login. Needs a
  durable login→fp admit record so auto-admit can remove the superseded fp (the
  keys branch loses the old mapping on overwrite). **Design fork** — see Log.
- [ ] **#6** concurrent operators racing `main` pushes (auto-admit / leave /
  remove / autosave) → committed-but-unpushed membership drift. Add
  pull-rebase-retry on `ErrPushRejected` for membership pushes.
- [ ] **#10** GitHub login rename orphans `<login>.asc` / verified keys / sidecar
  while collaborator ops use the current login. Detect + handle.

Doc reconciliations (in the target):
- [ ] **#4** local keyring vs "clears every store": keyring is operator-managed,
  not auto-cleared (audit confirmed it lingers). Caveat the invariant + decide on
  an optional `--purge-key`.
- [ ] **#5** add the "manifest changed locally, remote not re-keyed (push failed)"
  state to the matrix.
- [ ] **#9** refine the open question: `nous brain join OWNER/REPO` already
  republishes a pubkey, so the accepted-via-web self-cure capability EXISTS — the
  gap is discovery/TUI surfacing, not feasibility.

## Done when

- All findings above either fixed in code or reconciled in the target.
- Build/vet + cmd/nous + lib tests green; `brain-vm-e2e.sh` green (extend where a
  finding is e2e-observable on the file:// path — e.g. rotation, leave-strip).
- GitHub-layer-only effects (re-invite, collaborator) verified in the dogfood.

## Plan

- [ ] M1 — drift-class code fixes: #3, #11, #12 (+ tests).
- [ ] M2 — rotation / one-fp-per-login: #7/#8 (durable login→fp record + auto-admit
  supersede). *(confirm the store fork first.)*
- [ ] M3 — #6 push-rebase-retry, #10 login-rename handling.
- [ ] M4 — target doc reconciliations #4/#5/#9.

## Log

### 2026-06-02

Filed from the codex review (run via `codex exec -s read-only`). #1 already fixed.
**#7/#8 design fork:** handling rotation cleanly needs a durable login→fp record
(the keys-branch `<login>.asc` is overwritten on rotation, losing the old
mapping). Options: (a) new `.brain/members.yaml` (login→admitted-fp) written by
auto-admit + cleared on removal — also unifies login↔fp resolution; (b) extend
the manifest to carry login per recipient; (c) reuse `verified.yaml` (rejected —
operator reserved it for offline sig verification). Leaning (a). Confirm before M2.
