---
id: 000041
status: done
deps: []
github_issue:
created: 2026-06-02
updated: 2026-06-02
estimate_hours: 9
target: collaborator-state-machine
actual_hours: 8
---

# collaborator-lifecycle hardening (codex review findings)

## Problem

A read-only codex design review of `workshop/targets/collaborator-state-machine.md`
(against the implementing code + threat model) surfaced 12 findings. #1 was a live
resurrection bug, already fixed (`f2cb9de`). The rest are real drift/edge gaps in
the per-brain collaborator lifecycle and are in scope for THIS testing round.
(Cross-brain ban list stays nous#37 — not here.)

## Spec

**Design fork resolved (2026-06-02, operator-confirmed):** the durable
login→admitted-fp record for #7/#8 lives as an additive inline map in the
manifest — `recipient_logins: {login: FP, ...}` in `.brain/config.md`, co-located
with `recipients:`. Chosen over a separate `members.yaml` (fewer stores → stronger
invariant #2) and over git-history mining (breaks under nous#35 compaction). It
also becomes the primary login→fp resolution source (#3). Full plan:
`~/.claude/plans/parsed-tumbling-mccarthy.md`.

Code fixes:
- [x] **#1** non-recipient removal didn't strip keys-branch pubkey — FIXED `f2cb9de`.
- [x] **#3** login↔fp resolution order: stop treating `verified.yaml` as a mapping
  source (it's offline-sig-verification only). `FingerprintForLogin` +
  `RemoveRecipient`. **M1** shipped keys-branch → peer sidecar; **M2**
  re-prioritized to `recipient_logins` map (authoritative admitted-fp record) →
  keys-branch → peer sidecar.
- [x] **#11** re-invite isn't bulletproof: `gh.InviteCollaborator` only deletes
  the stale invite if listing succeeds and ignores delete failures, then PUTs →
  can still no-op (no email). Make list/delete failures hard errors.
- [x] **#12** `leave` ≠ full removal: `LeaveBrain` clears manifest + collaborator
  but not verified.yaml or the keys-branch pubkey → the leaver's `<login>.asc`
  lingers and another operator's auto-admit can re-add them. Route through the
  same strip path.
- [x] **#7 + #8** GPG key rotation + one-login→one-fp: an overwritten
  `<login>.asc` (new fp) admits the new fp while the OLD fp stays a recipient →
  stale recipients; no invariant enforces one active fp per login. Needs a
  durable login→fp admit record so auto-admit can remove the superseded fp (the
  keys branch loses the old mapping on overwrite). **Design fork** — see Log.
- [x] **#6** concurrent operators racing `main` pushes (auto-admit / leave /
  remove / autosave) → committed-but-unpushed membership drift. Add
  pull-rebase-retry on `ErrPushRejected` for membership pushes.
- [x] **#10** GitHub login rename orphans `<login>.asc` / verified keys / sidecar
  while collaborator ops use the current login. Detect + handle.

Doc reconciliations (in the target):
- [x] **#4** local keyring vs "clears every store": keyring is operator-managed,
  not auto-cleared (audit confirmed it lingers). Caveat the invariant + decide on
  an optional `--purge-key`.
- [x] **#5** add the "manifest changed locally, remote not re-keyed (push failed)"
  state to the matrix.
- [x] **#9** refine the open question: `nous brain join OWNER/REPO` already
  republishes a pubkey, so the accepted-via-web self-cure capability EXISTS — the
  gap is discovery/TUI surfacing, not feasibility.

## Done when

- All findings above either fixed in code or reconciled in the target.
- Build/vet + cmd/nous + lib tests green; `brain-vm-e2e.sh` green (extend where a
  finding is e2e-observable on the file:// path — e.g. rotation, leave-strip).
- GitHub-layer-only effects (re-invite, collaborator) verified in the dogfood.

## Plan

- [x] M1 — drift-class code fixes: #3, #11, #12 (+ tests).
- [x] M2 — rotation / one-fp-per-login: #7/#8 (durable login→fp record + auto-admit
  supersede). *(confirm the store fork first.)*
- [x] M3 — #6 push-rebase-retry, #10 login-rename handling.
- [x] M4 — target doc reconciliations #4/#5/#9.

## Log

### 2026-06-02
- 2026-06-02: closed — All 12 codex-review findings closed: #1 (pre-fixed), #3/#11/#12 (M1), #7/#8 (M2), #6/#10 (M3) in code; #4/#5/#9 (M4) reconciled in the collaborator-state-machine target. Each milestone fresh-eyes reviewed (M1 FIX-THEN-SHIP, M2 SHIP, M3 SHIP, M4 FIX-THEN-SHIP) with findings fixed. New unit tests (FingerprintForLogin_IgnoresVerifiedYaml, Manifest_RecipientLogins, DetectLoginDrift, PushMembershipChange_RetriesOnConcurrentPush) + gpg integration (TestEndToEnd_RotationSupersede); go build/vet + all lib suites (incl. gpg, unsandboxed) green.
- 2026-06-02: closed M4 — #41 follow-on (collaborator-state-machine target). M4 is doc-only: reconciled findings #4 (local-keyring caveat on invariant 2 + optional --purge-key non-default), #5 (Recipient-local-ahead matrix row + footnote), #9 (Accepted-no-pubkey self-cure capability exists, gap is discovery/TUI), plus recipient_logins in lede/matrix + leave-transition full-strip note + ## Revisions. go build/vet + all lib suites (incl. gpg integration) green across the issue.; review verdict: FIX-THEN-SHIP
- 2026-06-02: closed M3 — #41 follow-on (collaborator-state-machine target). #6: pushMembershipChange wraps stripMember + auto-admit; on rejected push, ResetToRemoteMain + re-apply idempotent set-op; refuses on a dirty tracked tree to avoid reset-hard losing unrelated edits. TestPushMembershipChange_RetriesOnConcurrentPush (plain-git) proves convergence not clobber. #10: gh.ListCollaborators + pure brain.DetectLoginDrift (TestDetectLoginDrift) flag renamed/departed logins in `recipient list`. Also resolved M1 review Imp#2 (keys-branch-canonical revoke-login). go build/vet + lib suites incl. gpg integration green. Fresh-eyes review: 1 Critical (dirty-tree reset-hard) fixed, rest correct.; review verdict: SHIP
- 2026-06-02: closed M2 — #41 follow-on under collaborator-state-machine target (not a project milestone). recipient_logins map round-trips (TestManifest_RecipientLoginsRoundTrip); TestEndToEnd_RotationSupersede (gpg) proves rotation evicts old fp + admits new + updates map + operator untouched + idempotent re-run; verified.yaml drift gate preserved (TestEndToEnd_DriftDetection); FingerprintForLogin reads map first; stripMember drops map entry on removal. go build/vet + lib suites green. Fresh-eyes review: no Critical; 2 Important judged acceptable; Minors fixed.; review verdict: SHIP
- 2026-06-02: closed M1 (#3, #11, #12) — #41 is a follow-on under target collaborator-state-machine, not a shared-brain project milestone (project closed) — no project detail block. go build/vet + full suite green (unsandboxed for gpg); TestFingerprintForLogin_IgnoresVerifiedYaml pins #3; InviteCollaborator list/delete now hard-error (#11); LeaveBrain routes through the shared `stripMember` so leave clears every store (#12). The `stripMember` strip itself is e2e-proven via the `recipient remove` assertion in brain-vm-e2e.sh; the *leave wiring* is verified by construction (1-line call) + dogfood, because LeaveBrain is GitHub-coupled (refuses on the file:// e2e origin) so its full flow can't run GitHub-free. review verdict: unknown (no Critical; Important findings dispositioned below).
- 2026-06-02: M1 milestone-review (sdlc judge) dispositions — (Imp#1 leave has no isolated test) → architectural: LeaveBrain is GitHub-coupled; strip proven via recipient-remove e2e + leave live-verification carried to the dogfood (issue done-when already designates GitHub-layer effects for the dogfood). (Imp#2 fp→login revoke-target prefers verified-hint over canonical keys-branch) → pre-existing nous#40 behavior, best-effort; **flagged for M3 #10** (login-rename is where source precedence gets decided). Minors fixed: leave.go error-prefix + stale mid-flow doc comment. Test-hermeticity nit on the #3 test acknowledged (unlikely-login guard).

Filed from the codex review (run via `codex exec -s read-only`). #1 already fixed.
**#7/#8 design fork:** handling rotation cleanly needs a durable login→fp record
(the keys-branch `<login>.asc` is overwritten on rotation, losing the old
mapping). Options: (a) new `.brain/members.yaml` (login→admitted-fp) written by
auto-admit + cleared on removal — also unifies login↔fp resolution; (b) extend
the manifest to carry login per recipient; (c) reuse `verified.yaml` (rejected —
operator reserved it for offline sig verification). Leaning (a). Confirm before M2.
