---
id: '000030'
status: done
created: 2026-05-21
updated: 2026-06-02
estimate_hours: 8
actual_hours: 2
---

# brainsync: autosave + `nous push` checkpoint

## Problem

Today, the operator drives sync explicitly: they run `git commit`
in a brain, RefWatcher sees the ref change, brainsync pushes.
Commit cadence = push cadence = explicit human gesture.

This breaks the "brain as extension of thinking" model in two ways:

1. **Cognitive overhead.** Operators are constantly choosing
   commit boundaries while doing the actual work (writing,
   editing, dropping new files into the brain). Git is the
   substrate, not the user surface — but it leaks through.
2. **Lost work on idle gaps.** Anything saved-but-not-committed
   is invisible to peers and unrecoverable if the laptop dies.

The substrate should hide git from operators in the common path:
they save files in their editor, the daemon takes care of
committing, batching, and pushing. The explicit human gesture
moves from `git commit && git push` to an optional
`nous push "label"` — a *checkpoint* that names a moment.
