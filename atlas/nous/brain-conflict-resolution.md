# Brain conflict resolution — `/nous-resolve`

How shared-brain conflicts get resolved. Companion to [`brain/atlas/sync-substrate-decision.md`](../../../brain/atlas/sync-substrate-decision.md), which covers the sync substrate and conflict-file convention.

## The conflict surface

`brain-sync` (see `nous/cmd/brain-sync/`) does file-level conflict resolution — never content-merge. When two peers commit divergent edits to the same file, the loser is renamed to:

```
<stem>.conflict-<peer>-<YYYYMMDDTHHMMSSZ>.<ext>
```

(format defined in `nous/lib/brainsync/resolve.go`). Both peers see both files; one is canonical, one is the conflict file. Manual resolution is "read both, pick what to keep, replace canonical, delete conflict file, commit." That worked as the v1 fallback, but doesn't scale to natural co-authoring.

## `/nous-resolve` — the skill

A Claude Code slash command that resolves conflict pairs via AI-prose merge with prototype-aware structural reasoning.

- **Location**: `construct/skills/nous-resolve/` (vendored to `.claude/skills/nous-resolve` via `construct/base.manifest`)
- **Invocation**:
  - `/nous-resolve <brain-root>` — resolve mode (one brain at a time)
  - `/nous-resolve <brain-root> undo` — revert the most recent `/nous-resolve` merge commit
- **Resolve procedure** (7 steps, see SKILL.md): Validate brain-root → discover conflicts → load context (both versions + prototype + recent commits + references) → reason structurally → show diff + confirm → preserve to `.brain/merges/` → write merged → delete conflict → commit explicitly with structural-choices in body. brain-sync's ref-watcher pushes the commit.
- **Undo procedure**: `git revert <merge-sha> --no-edit` against the most recent commit matching `^merge: .* via /nous-resolve$`. One operation restores canonical, restores the conflict file, and removes the snapshot files. Targeted older reverts (not the most recent) are manual `git revert <sha>` against the relevant commit.
- **Safety floor**: `preserve.py` snapshots both pre-merge versions plus a `meta.json` to `<brain-root>/.brain/merges/<utc-iso>-<canonical-slug>/` *before* canonical is overwritten. Non-conditional. Combined with explicit-commit + git-revert undo, every merge is one command away from rollback.

## Structural awareness

The load-bearing claim of M1: an LLM with the file's prototype + ambient context reads "this is a list of travelers" without anyone telling it. SKILL.md formalizes this into explicit per-element merge rules:

| Element | Default merge |
|---|---|
| scalar / enum scalar | latest by `updated:`, else surface |
| list (inline or block) | set union, dedup, canonical-first |
| key-anchored list (`### 2026-08-01` under `## Itinerary`) | each key independent, recurse |
| table | union by primary key; surface if no key |
| prose | concatenate added paragraphs; surface same-paragraph edits |

When a prototype exists at `construct/datatype/<type>.md`, prototype semantics are the contract — they override heuristics. Travel-plan's prototype already reads naturally as a per-section schema.

## Iteration boundary

`nous#5 M4` (declarative `merge:` rules in prototype frontmatter — `union`, `latest-wins`, `by-key(<field>)`, `ai-prose`) is **conditional**. It ships only if `nous#5 M3` dogfood (real conflicts from `nous#12` wife/me trip planning) reveals the prompt-guided merger consistently dissolving structure. Until then, prototype-as-implicit-schema + structural prompt guidance is the design.

## Failure modes worth knowing

- **Multi-peer conflicts** (`<file>.conflict-peerB-...` AND `<file>.conflict-peerC-...`): resolve in `utc-ts` order, each as a successive merge against the current canonical.
- **Binary files**: skill refuses; needs manual `mv`. AI-prose merge doesn't apply to bytes.
- **Concurrent edit during resolution**: detected via `git status` post-merge; aborts cleanly.
- **User declines at confirm step**: nothing's written, nothing's preserved, nothing's committed.

## Pointers

- Skill: `construct/skills/nous-resolve/SKILL.md`
- Plan: `workshop/plans/000005-semantic-merge-skill-plan.md`
- Issue: `workshop/issues/000005-semantic-merge-skill.md`
- Sync substrate it operates on top of: `brain/atlas/sync-substrate-decision.md`
- Method baseline (v3 actuals): `brain/data/life/42shots/velocity/baseline-v3.md`
