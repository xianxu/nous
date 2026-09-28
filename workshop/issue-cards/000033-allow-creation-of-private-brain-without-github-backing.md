---
id: '000033'
status: done
created: 2026-05-26
updated: 2026-05-31
actual_hours: N/A
---

# allow creation of private brain without github backing

## Problem

Every brain today is born GitHub-backed: `nous brain new` (and
`scripts/new-brain.sh`) bundle `gh repo create --private`, the gcrypt
remote wiring, and the first push into creation. There is no way to
make a brain that is *purely local* — a git repo on this machine with
no upstream at all.

Wanted: a **local-only private brain** — `git init` + `.brain/config.md`,
no remote, no network, no GitHub auth. FileVault (device FDE) is the
at-rest protection; gcrypt is moot because nothing is pushed anywhere.
The user may sync the folder by external means (iCloud, Dropbox) at
their own discretion — that is explicitly *outside* the brain's
guarantees (see Non-goals).

This is the lightweight default for "just give me a brain," and it must
sit cleanly under the existing privacy abstractions rather than being a
bolt-on.
