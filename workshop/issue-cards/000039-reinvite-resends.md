---
id: '000039'
status: done
created: 2026-06-02
updated: 2026-06-02
estimate_hours: 1
actual_hours: 1
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
