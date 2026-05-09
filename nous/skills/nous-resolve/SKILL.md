---
name: nous-resolve
description: "Resolve brain-sync conflict files via AI-prose merge with prototype-aware structural reasoning. Invoke as /nous-resolve <brain-root>."
---

# nous-resolve

Resolve conflict files left by `brain-sync` (format: `<stem>.conflict-<peer>-<YYYYMMDDTHHMMSSZ>.<ext>`, e.g. `paris.conflict-xianxu-mbp-20260507T221604Z.md`) by reading both versions, the file's prototype if any, and ambient context, then producing a merged version. Pre-merge versions are preserved under `.brain/merges/` as a safety floor; the merged result is committed explicitly.

## Usage

```
/nous-resolve <brain-root>           # resolve conflict files
/nous-resolve <brain-root> undo      # revert the most recent merge
```

`<brain-root>` is the absolute path to the brain repo (must contain `.brain/config.md`). One brain at a time.

When invoked without `undo`, the skill walks the 7-step **resolve procedure** below. Heavy lifting — context loading, structural reasoning, the actual merge — is done by the agent; helper scripts (`preserve.py`, `find-conflicts.sh`) handle deterministic mechanical steps only.

When invoked with `undo`, the skill walks the **undo procedure** at the bottom — `git revert` on the most recent merge commit, restoring canonical + conflict file + removing the snapshot in one operation.

## Procedure

### 1. Validate `<brain-root>`

`<brain-root>/.brain/config.md` must exist. If not, refuse:

```
brain-root must contain .brain/config.md — that's how a brain is identified.
Got <path>; no manifest found.
```

### 2. Discover conflicts

```sh
bash <skill-dir>/find-conflicts.sh <brain-root>
```

Output: tab-separated tuples `<canonical>\t<conflict-file>\t<peer>\t<utc-ts>` per line.

- 0 lines → tell the user "no conflicts in <brain-root>" and stop.
- 1 line → proceed to step 3 with that pair.
- ≥2 lines → list them with `peer` + `utc-ts`, ask which to resolve (or `all` to do them in timestamp order).

### 3. Load context

For each pair to resolve:

- **Both versions in full**: read `<canonical>` and `<conflict-file>`. Print the file lengths and a 1-line diff summary so the user sees the magnitude.
- **The prototype if any**: read `<canonical>`'s YAML frontmatter for a `type:` field. If present, search for the prototype:
  1. `<brain-root>/construct/datatype/<type>.md`
  2. `<brain-root>/../nous/construct/datatype/<type>.md` (sibling-repo lookup)
  3. Walk up from `<brain-root>` looking for any `construct/datatype/<type>.md` ancestor (max 4 levels)
  
  If found, read it. The prototype documents per-section semantics — that's what makes structural merge correct rather than guessed.
- **Recent commits to the canonical**: `git -C <brain-root> log -p --follow -n 5 -- <canonical-relative-path>`. Reveals which sections were recently active and who-changed-what.
- **Inline references**: scan canonical and conflict for markdown links (`[text](path)`) and frontmatter `sources:` / `references:` / `deps:`. For paths inside this brain, read the referenced files (depth 1, no recursion).

Loading depth is your judgment — a one-line scalar conflict doesn't need 5 commits of history; a contested itinerary section does.

### 4. Reason about the merge

This is the load-bearing step. Walk the file from frontmatter through body sections, classifying each element and applying the merge rule.

**Identification taxonomy** (from prototype if available; else from heuristics):

- **`scalar`** — single value (`destination: "Rome"`, `status: planning`)
- **`enum scalar`** — restricted vocabulary (`status: planning|booked|in-progress|done`)
- **`list`** — inline `[a, b, c]` or block `- a\n- b\n- c`
- **`key-anchored list`** — heading-keyed entries like `### 2026-08-01` under `## Itinerary`, or `### nous-3-m1` detail blocks
- **`table`** — markdown table with a discoverable primary-key column
- **`prose`** — heading followed by paragraphs

**Default merge rules per element type** (override with prototype declarations when they contradict):

- **Scalars**: take latest by `updated:` timestamp if both files have a meaningful one. Otherwise surface the conflict to the user with both values + your recommendation.
- **Enum scalars**: same as scalars.
- **Lists**: set union, dedup on string equality, preserve canonical's order, append peer-only items at the end.
- **Key-anchored lists**: each key is independent. If both peers added different keys (different dates, different anchors): both kept. If both peers modified the same key's content: recurse on that block.
- **Tables**: detect primary key (first column, or named `id`/`name`/`key`). Union rows by key, preserving canonical's order; append peer-only rows. If no primary key is discoverable, treat the whole table as prose.
- **Prose**: if both peers added paragraphs to the same heading, concatenate (canonical first, then peer's additions; mark peer's contribution with a brief inline marker like `<!-- from peer -->` if confusion is likely). If both peers edited the *same* paragraph (same first ~10 words), surface to user with both versions.

**Prototype as contract**: when the prototype documents a section's purpose ("Travelers is the list of trip participants"), trust the prototype over heuristics. The prototype is what makes "Travelers" a `list` even if a peer accidentally wrote it as a paragraph.

**Surface, don't guess**: any case where the merge isn't obvious — both edited the same scalar with no `updated:` discriminator, both modified an unknown section, prose paragraphs that overlap in subject — present both options + your recommendation. The pensive's failure mode is the LLM dissolving structure; surfacing prevents that.

### 5. Show diff + confirm

Show the user:

1. Unified diff `canonical → merged`
2. Unified diff `<conflict-file> → merged`
3. Bullet summary of structural choices ("merged `travelers:` as union: kept alice, added bob"; "Itinerary: kept all of canonical's days; added peer's 2026-08-04 entry")
4. Any items you flagged as ambiguous and resolved by judgment (so the user can override)

Ask:

```
Apply this merge?
  [y]es — preserve, write, commit
  [n]o — abort, leave files untouched
  [s]how-full — print the full merged content before deciding
```

### 6. On confirm — preserve, write, cleanup

In order, no ambiguity:

```sh
python3 <skill-dir>/preserve.py <canonical> <conflict-file>
# writes <brain-root>/.brain/merges/<utc-iso>-<canonical-slug>/{canonical<ext>, peer<ext>, meta.json}
```

Then write merged content to `<canonical>`. Then `rm <conflict-file>`. Order matters: preserve runs before any destructive write.

### 7. Commit

```sh
git -C <brain-root> add <canonical> .brain/merges
git -C <brain-root> rm <conflict-file>   # already deleted on disk; this stages the deletion
git -C <brain-root> commit -m "merge: <canonical-relative> via /nous-resolve

peer: <peer-id-from-conflict-filename>
conflict-ts: <utc-ts-from-conflict-filename>
preserved at: .brain/merges/<utc-iso>-<canonical-slug>/

structural choices:
- <choice 1>
- <choice 2>
..."
```

`brain-sync` will pick the new commit up via its ref-watcher and push.

If multiple pairs were chosen for resolution in step 2, repeat steps 3–7 per pair, in `utc-ts` order.

## Undo procedure

When invoked as `/nous-resolve <brain-root> undo`, revert the most recent merge committed by this skill. The safety floor: any `/nous-resolve` merge can be undone with one command, restoring canonical + conflict file + removing the snapshot directory.

### 1. Validate `<brain-root>`

Same as resolve step 1: `<brain-root>/.brain/config.md` must exist.

### 2. Find the most recent merge commit

```sh
git -C <brain-root> log --grep '^merge: .* via /nous-resolve$' -1 --format='%H %ai %s'
```

If empty: report "no `/nous-resolve` merge commits in this brain — nothing to undo." Stop.

If found: print the commit summary so the user sees what's about to be reverted:
- Subject line
- Date
- Body (peer, conflict-ts, structural choices) via `git -C <brain-root> log -1 --format=%B <sha>`

### 3. Confirm with user

```
Revert this merge?
  [y]es — restores canonical, restores conflict file, removes .brain/merges snapshot
  [n]o — abort

Note: if commits landed AFTER the merge that depend on the merged content,
git revert may produce conflicts. In that case the revert is aborted and
the brain is left untouched; resolve manually.
```

### 4. On confirm

```sh
git -C <brain-root> revert <sha> --no-edit
```

`git revert` produces a new commit whose diff is the inverse of the merge commit. Result:
- canonical → pre-merge content (restored)
- conflict file → restored at its original path
- `.brain/merges/<ts>-<slug>/canonical.<ext>`, `peer.<ext>`, `meta.json` → removed (git tracks files, may leave empty parent dir; harmless, optionally `rmdir` it)

If `git revert` reports conflicts (subsequent commits modified the same lines), it leaves the working tree mid-revert. Abort with `git -C <brain-root> revert --abort`, tell the user to resolve manually, and stop. Don't try to auto-fix.

### 5. brain-sync handles the push

The new revert commit triggers brain-sync's ref-watcher; it pushes to origin on the next cycle. No explicit push needed.

## Trail — finding old merges

Snapshots persist under `.brain/merges/<utc-iso>-<slug>/` until manually pruned. Useful queries:

```sh
# All /nous-resolve merge commits in this brain
git log --grep '^merge: .* via /nous-resolve$' --format='%h %ai %s'

# Merges in the last week
git log --grep '^merge: .* via /nous-resolve$' --since '1 week ago'

# Merges of a specific path
git log --grep '^merge: data/life/travel/.* via /nous-resolve$'

# Snapshots on disk (newest first)
ls -1 <brain-root>/.brain/merges/ | sort -r | head
```

To revert a specific *older* merge (not the most recent), pass the SHA explicitly: `git -C <brain-root> revert <older-sha> --no-edit`. The skill's `undo` subcommand only handles the most-recent case; older targeted reverts are a manual `git` operation.

To prune old snapshots: `rm -rf <brain-root>/.brain/merges/<utc-iso>-<slug>/`. No automated prune yet; revisit if `.brain/merges/` becomes large.

## Failure modes

- **User declines at step 5**: leave canonical and conflict-file untouched. No commits. No preservation snapshot (preserve.py runs only on confirm).
- **Ctrl-C mid-procedure**: nothing's written until preserve.py runs at step 6; abort is safe.
- **User overrides a prototype rule**: honor it; mention the override explicitly in the commit body's "structural choices" list.
- **Multiple peers for one canonical** (`<file>.conflict-peerB-...` AND `<file>.conflict-peerC-...`): resolve in `utc-ts` order, treating each as a successive merge against the current canonical.
- **Binary file** (`.png`, `.pdf`, etc.): refuse — "binary conflict needs manual resolution. Choose canonical or peer manually with `mv`, then commit." AI-prose merge doesn't apply to bytes.
- **Same canonical edited by another agent during the procedure**: detected by `git status` post-merge — if the working tree shows changes to canonical that aren't yours, abort and ask the user to re-run.

## What this skill doesn't do

- **Section-aware declarative `merge:` rules** in prototype frontmatter — conditional M4, ships only if the M3 dogfood reveals real structural failures the prompt-guided merger can't be coaxed out of.
- **Auto-invocation from brain-sync** — brain-sync writes conflict files; you invoke this skill explicitly when you see one.
- **Targeted undo of older merges** — the `undo` subcommand reverts only the most recent `/nous-resolve` commit. Older targeted reverts are manual `git revert <sha>` operations.
- **Automated snapshot pruning** — `.brain/merges/<ts>-<slug>/` directories persist until manually removed. Add a `prune` subcommand if it becomes a real problem.

## Notes

- Prototype semantics are the contract. If a prototype declares `## Travelers` as the trip-participants list, treat it as a list even if a peer wrote it as prose — that's a structural error to merge into list shape, not a competing format to preserve.
- When in doubt, surface to the user. The `done_when` for the `/brain-resolve` work is "human-confirmed without data loss" — confirmation is cheap, silent dissolution is expensive.
- The merge commit message body is the audit trail. Future-you reading `git log` should see what was kept from each side and why.
