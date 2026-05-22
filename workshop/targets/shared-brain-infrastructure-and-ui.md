---
type: target
slug: shared-brain-infrastructure-and-ui
status: active
created: 2026-05-22
updated: 2026-05-22
sources:
  - /Users/xianxu/workspace/ariadne/docs/vision/2026-05-22-01-pensive-durable-target-pattern.md
  - /Users/xianxu/workspace/brain/docs/vision/2026-05-22-01-pensive-workbench-bet.md
---

# Target: shared brain infrastructure and user interface

A shared brain is the substrate by which two or more people maintain a common workbench: durable notes, agentic interactions, decisions, drafts — kept in sync across machines and accessible to each operator's AI agents. We want this to be a daily-driver for ourselves first, then for collaborators (family, team), then for whoever else can use the conventions.

The infrastructure pieces are in flight (gcrypt-encrypted git on GitHub as substrate, autocommit + auto-push + auto-pull via brainsync, GitHub-mediated trust admission for adding collaborators, drift detection as the safety floor). The user interface pieces are catching up (`nous brain` TUI for join + accept + clone + leave, `nous push` for explicit checkpoints, the diagnostic logging that surfaces what the daemon is doing). What we want is to get to a state where the substrate is invisible during daily use — the operator just saves files, the sync handles itself, and the only operator gestures needed are first-time setup, inviting a collaborator, and leaving when done.

The shared brain is the engineering instantiation of the workbench bet: methods + substrate above the model layer, owned by the operator, portable across agents. If we can run this for ourselves daily and pull family/team onto it, the conventions get tested at the only scale that matters early — small.

## Why now

Models are commoditizing faster than expected; the durable lock-in points are above the model. Owning the workbench (data + methods + conventions) is where the value moves. We've been building the pieces; this is the moment to insist they cohere as a daily-driver experience rather than a developer demo.

Concretely: Emma + Xian's testing this week (autocommit, auto-push, leave, gpg-pinentry on VMs, launchd kickstart) surfaced the rough edges that block non-author operators from using the system. Each rough edge fixed teaches us something the next operator won't have to hit. The infrastructure has matured; the UI hasn't quite. Closing that gap is the next push.

## What this is NOT

- **Not a hosted product.** The whole point is operator-controlled substrate. No central server, no SaaS dependency beyond what's already implicit (GitHub as dumb storage; that's it).
- **Not real-time collaborative editing.** This isn't Google Docs. The sync is eventual-consistency via git; conflict resolution happens through rebase + the file-level conflict-file convention. Multiple operators on the same file at the same second is not the use case.
- **Not multi-tenant inside a single workbench.** One operator per machine; brains can be shared across operators but each operator has their own machine with their own workbench. Single-threaded-human assumption stays.
- **Not a brand-new sync protocol.** git + gcrypt + GitHub. Don't invent a new substrate; use what's there.
- **Not mobile / iOS access.** The substrate doesn't preclude it, but it's not in scope. CLI + TUI on macOS first, Linux next if the operators run there.
- **Not solving the "what if the brain's contents are huge" problem yet.** Large-binary handling, partial clones, sparse checkouts — defer. Get the conventions right for text-heavy brains first.

## Why this target is broader than any one issue

Earlier sessions treated this as a stack of issues — autosave (#30), TUI list async (#31), leave (#32), the docs sweeps, the launchd fixes. Each issue made progress on its own slice. But the cumulative arc — "make a shared brain into a daily-driver UI" — wasn't legible from any single issue. That's the work this target is naming.

Future work that should reference this target: anything that touches the shared-brain operator experience. Anything *purely* about internal mechanics (e.g., a refactor of `lib/brainsync/`) probably doesn't need to. The target is the operator's view; pure internals advance it indirectly but don't need explicit linkage.

## Open questions

- **Cross-brain references.** An operator who has both a personal brain and a family brain may want to link from one to the other. Need a convention for `@brain:family/<path>` references that resolves both for the operator (knows about both brains) and for collaborators in only one (sees a broken link with context). Not committed yet.
- **Notifications.** When a collaborator pushes, does the operator's `nous brain` TUI surface it? A notification on the macOS notification center? A subtle indicator in the list view? Defer until the basic flow is dailied-driven; let the actual annoyance teach us what's needed.
- **The first non-author operator.** Beyond Emma + Xian, who's the first? A family member who isn't a software engineer? That tests UX assumptions we haven't even named yet. Worth lining up before declaring this target `achieved`.
- **Mobile / iOS read-only access.** Sometimes operators want to *read* their brain on a phone, not edit. Read-only iOS could be a small slice that doesn't require a full mobile workbench. Open.
- **Brain hygiene.** As brains grow, when do we archive old content? Move old projects to a sub-brain? The "brain as durable extension of mind" framing implies *don't delete*, but eventually the bytes pile up. Deferred for now.

## Revisions

(none yet — this is the initial seed.)
