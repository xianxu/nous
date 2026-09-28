---
id: 000053
status: open
created: 2026-09-18
updated: 2026-09-18
estimate_hours:
github_issue:
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
