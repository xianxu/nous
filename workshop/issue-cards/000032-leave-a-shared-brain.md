---
id: '000032'
status: done
created: 2026-05-21
updated: 2026-06-02
estimate_hours: 4
actual_hours: 2
---

# Leave a shared brain — `nous brain leave` + TUI `l` key

## Problem

A collaborator on a shared brain has no first-class way to leave.
The only path today is:

  1. Ask an admin (likely a different person) to run `nous brain
     recipient remove` against my fingerprint.
  2. Wait for the manifest update to propagate.
  3. Manually go to GitHub and click "Leave repository."

That's clumsy, prone to delay (depends on someone else), and leaves
a limbo state where I'm still a GitHub collaborator (so the repo
shows up in `nous brain` accessible lists) but my fingerprint is
gone from the manifest (so I can't decrypt anything). The
collaborator-leave gesture should be one operation, owned by the
leaving party.
