---
id: 000039
status: open
deps: []
github_issue:
created: 2026-06-02
updated: 2026-06-02
estimate_hours: 1
---

# brain invite: re-invite must re-send (clear expired invitation)

## Problem

Re-inviting a collaborator whose invitation has expired silently does nothing —
no fresh invitation, no GitHub email. Observed (2026-06-02, dogfood): emmatest42's
invite to brain-family expired (GitHub repo invitations expire after 7 days);
`nous brain invite emmatest42` reported success but sent nothing. Only after the
expired invitation aged out of the pending list (visible on a later `nous brain`)
did a subsequent invite actually send.

Root cause: `nous brain invite` → `gh.AddCollaborator` = `PUT /repos/{owner}/{repo}/collaborators/{login}`. GitHub treats that as a **no-op (204, no email) when an invitation already exists for the login** — including an expired one. So the re-PUT can't re-send.

Both the CLI (`cmd/nous/brain_invite.go`) and the TUI (`lib/tui/brain/invite_collab.go`) call `gh.AddCollaborator` directly → same CLI/TUI drift risk as the nous#38 remove path.

## Spec

A re-invite should always send a fresh invitation. Add a shared `gh` composite
that both CLI and TUI call:

- `gh.DeleteRepoInvitation(owner, repo, id)` — `DELETE /repos/{owner}/{repo}/invitations/{id}` (the owner-side delete; distinct from the existing invitee-side `DeclineInvitation`).
- `gh.InviteCollaborator(owner, repo, login, permission) (InviteResult, error)`:
  1. list `RepoPendingInvitations(owner, repo)`,
  2. delete any whose `Invitee.Login` matches (case-insensitive) — pending OR expired,
  3. `AddCollaborator` (PUT) → now guaranteed to create a fresh invitation + email.
  `InviteResult.ReplacedStale` is true when step 2 deleted something, so callers
  can say "replaced a stale/expired invitation" vs "sent a new invitation".

Wire both `cmd/nous/brain_invite.go` and `lib/tui/brain/invite_collab.go` through
`gh.InviteCollaborator` so they can't diverge.

Out of scope: detecting already-accepted collaborators (PUT is a harmless no-op
there); the GitHub permission semantics are fuzzy enough to not bother in v1.

## Done when

- A second `nous brain invite <login>` after an expired/pending invitation sends
  a fresh invitation (GitHub email arrives) — first try.
- CLI and TUI share `gh.InviteCollaborator`.
- The CLI reports whether it replaced a stale invitation.

## Plan

- [x] M1: `gh.DeleteRepoInvitation` + `gh.InviteCollaborator` (clear-stale → PUT);
  wire CLI + TUI; message tweak. Build/vet + touched-package tests green; the gh
  calls hit the live API, so the first-try re-invite is verified in the durable-VM
  dogfood (the user will confirm).

## Log

### 2026-06-02

Filed from the dogfood: expired-invitation re-invite was a silent no-op
(PUT-collaborator is idempotent against an existing invitation record). Fix:
delete the stale invitation first, then PUT, via a shared gh composite both
entry points use.
