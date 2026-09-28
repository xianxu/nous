---
id: 000053
status: open
deps: [ariadne#238]
github_issue:
created: 2026-09-18
updated: 2026-09-18
estimate_hours:
card_mirror: '421925b7781eaa42f38a9d708756c33d707c100f' # card fields mirrored from issue-cards; edit via sdlc
---

# atlas: migrate to purpose split (map / journeys / workflow)

## Problem

ariadne#238 splits atlas by purpose:
- **the map** (`atlas/` root and feature folders): short pointers, terminology,
  design reasons
- **user journeys** (`atlas/journeys/`): steps in the user's words plus an
  interruption table of current behavior
- **workflow** (`atlas/workflow/`)

It also sets a sorting rule for existing content: user-visible behavior goes to
`journeys/`, an invariant goes to `workshop/targets/`, a pointer or design reason
stays in the map (short), and prose that restates the code is deleted.

This repo's atlas predates that split.

**Current atlas (2026-09-18):** 2668 lines across: `atlas/charon/charon.md`, `atlas/charon/index.md`, `atlas/charon/security-audit.md`, `atlas/nous/architecture.md`, `atlas/nous/autosave-and-checkpoint.md`, `atlas/nous/bootstrap-entry-points.md`, `atlas/nous/brain-conflict-resolution.md`, `atlas/nous/brain-manifest.md`, `atlas/nous/brain-topology-ladder.md`, `atlas/nous/cli.md`, `atlas/nous/collaborator-lifecycle.md`, `atlas/nous/dev-vs-runtime-mode.md`, `atlas/nous/e2e-integration-testing.md`, `atlas/nous/gcrypt-brain-encryption.md`, `atlas/nous/gmail-tool.md`, `atlas/nous/lib-layout.md`, `atlas/nous/oauth-health.md`, `atlas/nous/recipient-onboarding.md`, `atlas/nous/relationship-to-ariadne.md`, `atlas/nous/shim-conformance-grounding.md`. Not yet surveyed in detail; the first step is to sort these pages and to identify the 2–5 journeys central to this repo's users, or to decide it has no user surface.

## Spec

Apply ariadne#238's convention (AGENTS.md §8; `datatype show journey`):

1. **`atlas/index.md` sections by purpose:** Map, Journeys, Workflow. Every file
   stays linked.
2. **`atlas/journeys/`:** one `journey` page per central journey, listed above,
   in user vocabulary. For each interruption row, say what the user sees today.
   Mark a row *undecided* when no source (code, README, help text, tutorials)
   settles it, and list the undecided rows in the Log for the operator. A repo
   with no user surface can say so in `index.md` and skip this step.
3. **Sort each existing page, section by section**, using the rule above. Move
   invariants into `workshop/targets/`, cut map pages to pointers, and delete
   prose that restates the code.
4. **Fix the contradictions** listed above, and any others found while sorting,
   against the code, which is the source of truth.

## Done when

- `atlas/index.md` is organized by purpose and links every file.
- `atlas/journeys/` covers the central journeys (or `index.md` says why there are
  none). Undecided rows are listed in the Log.
- No map page restates code at length; each moved section is noted in the Log
  with its destination.
- The listed contradictions are resolved.

## Plan

- [ ] Journeys: draft, mark undecided rows, get the operator's review.
- [ ] Sort existing pages and fix contradictions.
- [ ] Rebuild `index.md` by purpose.

## Log

### 2026-09-18

Filed from a brain advisor session as one of the per-repo migrations under
ariadne#238. Order: ariadne#238, then these migrations, then `prd` (ariadne#237).
