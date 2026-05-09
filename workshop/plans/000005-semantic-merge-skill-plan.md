# nous#5 M1 — `/nous-resolve` v1: whole-file AI-prose merge

> **For agentic workers:** Consult AGENTS.md Section 3 (Subagent Strategy) to determine the appropriate execution approach. Most steps in this plan rely on session-warm context (just-built brain-sync conventions, prototype-as-schema decision); main session is the right venue. The synthetic-test verification step is bounded and could go to a subagent, but small enough that switching contexts costs more than it saves.

**Goal:** ship `/nous-resolve <brain-root>`, a Claude Code skill that resolves conflict files produced by `brain-sync` (`<file>.conflict-<peer>-<utc>.<ext>`) via whole-file AI-prose merge, with structural awareness from the file's prototype, pre-merge preservation as a safety floor, and explicit commit on user-confirmed merge.

**Spec source:**
- `nous/workshop/issues/000005-semantic-merge-skill.md` (M1 plan + load-bearing claim)
- `brain/atlas/sync-substrate-decision.md` (conflict-file convention from #4 M3)
- `brain/data/life/42shots/ideas/2026-04-28-01-pensive-collaborative-brain.md` (semantic merge section)
- `nous/construct/datatype/travel-plan.md` (the guinea-pig datatype)

---

## Architecture

**Skill home:** `nous/nous/skills/nous-resolve/` — same pattern as existing `nous-tools` and `charon` skills. Vendored to consumer repos via `nous/nous/nous.manifest`'s `symlink nous/skills/nous-resolve .claude/skills/nous-resolve` line. Slash command becomes `/nous-resolve` (no prefix system; directory name is the command name).

**Invocation:** `/nous-resolve <brain-root>` — mandatory positional argument. One brain at a time. Brain root must contain `.brain/config.md`; skill refuses otherwise.

**Skill shape — concept-driven (prose) + small mechanical helpers:**

```
nous/nous/skills/nous-resolve/
├── SKILL.md              # agent procedure (load-bearing for merge quality)
├── preserve.py           # ~30 lines: write pre-merge versions to .brain/merges/<ts>-<slug>/
├── find-conflicts.sh     # one-line find wrapper; outputs (canonical, conflict-file, peer, ts) tuples
└── test-synthetic.sh     # creates a sandbox conflict pair, runs the skill, asserts outcomes
```

The agent does context loading, structural reasoning, and the merge itself. Scripts handle deterministic mechanical steps (file detection, preservation snapshot).

---

## File Structure

### `nous/nous/skills/nous-resolve/SKILL.md`

Frontmatter:

```yaml
---
name: nous-resolve
description: "Resolve brain-sync conflict files via AI-prose merge with prototype-aware structural reasoning. Invoke with /nous-resolve <brain-root>."
---
```

Body covers (in order):

1. **When to invoke** — after `brain-sync` produces `<file>.conflict-<peer>-<utc>.<ext>` files; or proactively when the user mentions a conflict.

2. **Procedure** — the 7-step flow:

   1. **Validate `<brain-root>`**: ensure `<brain-root>/.brain/config.md` exists. Otherwise refuse with a clear message.
   2. **Discover conflicts**: invoke `find-conflicts.sh <brain-root>`. Get list of `(canonical, conflict-file, peer, ts)` tuples. If 0, report "no conflicts" and stop. If >1, list them and ask the user which to resolve (or `all`).
   3. **For each chosen pair, load context**:
      - Both versions: read `<canonical>` and `<conflict-file>` in full
      - Prototype: parse `<canonical>`'s YAML frontmatter for `type:`. If present, look for `<repo-root>/construct/datatype/<type>.md` (try `nous/construct/datatype/`, `<brain-root>/construct/datatype/`, then up the tree). If found, read it. If not, note "no prototype" and proceed with heuristics-only merge.
      - Recent commits: `git log -p --follow -n 5 <canonical>` for last-five history of the file
      - References: scan canonical and conflict file for inline references (markdown links, `sources:` / `references:` frontmatter, `[file.md]` patterns). Read those too if they're in this brain.
   4. **Reason about the merge** (the load-bearing step):
      - **Structural identification**: walk frontmatter and body sections. For each, classify: `list`, `key-anchored-list`, `prose`, `table`, `scalar`, `enum`. Use prototype semantics if available; else heuristics.
      - **Per-element merge rule** (default, overridable per prototype):
        - **Lists** (`travelers:`, body `-` bullets): set union, preserve order from canonical, append peer-only items
        - **Key-anchored lists** (e.g. `### 2026-08-01` under `## Itinerary`): merge each key-block independently; recurse on its content
        - **Tables**: detect primary key column if obvious (first column, or named `id`/`name`); union by key; surface ambiguous cases
        - **Scalars**: take latest by `updated:` timestamp if both files have one; else surface to user with both values
        - **Enum scalars** (`status:`): same as scalar; surface if no `updated:` discriminator
        - **Prose sections**: if both peers added paragraphs to the same heading, concatenate (keep both contributions, mark provenance as a brief comment); if both edited the same paragraph (same opening words), surface to user
      - **Prototype as contract**: if prototype declares a section's purpose (e.g. "Travelers is the list of trip participants"), respect that semantics over heuristic.
      - **Surface, don't guess**: any case where automatic merge isn't obvious, present both options + your recommendation to the user.
   5. **Show diff + confirm**:
      - Print a unified diff of `(canonical → merged)` and `(conflict-file → merged)` so the user sees what was kept from each side
      - Summarize the structural choices made (e.g., "merged `travelers:` as union: added bob, kept alice")
      - Ask: "apply this merge? [y/n/show-full-merged]"
   6. **On confirm — pre-merge preservation, write, cleanup**:
      - Run `preserve.py <canonical> <conflict-file>` → snapshots both pre-merge versions to `<brain-root>/.brain/merges/<UTC-iso>-<canonical-slug>/`
      - Write merged content to `<canonical>`
      - Delete `<conflict-file>`
   7. **Commit**: `git -C <brain-root> add <canonical> <conflict-file-deleted> .brain/merges/...`; commit with message: `merge: <canonical> via /nous-resolve` plus a body listing the structural choices made and the preserve snapshot path. brain-sync picks this up on its next ref-watch tick and pushes.

3. **Failure modes & overrides**:
   - User says no at confirm step → restore nothing (canonical and conflict-file unchanged); abort cleanly
   - Mid-merge `Ctrl-C` or skill abort → nothing's written yet (preserve.py runs only on confirm); state unchanged
   - User insists on overriding a prototype rule → honor it; document the override in commit body
   - Multiple conflicts in one file (rare with file-level resolution but possible if the file was modified twice) → resolve in `<utc-timestamp>` order

4. **What this skill doesn't do** (M1 scope):
   - No undo command — that's M2 (`/nous-resolve undo`)
   - No section-level declarative `merge:` rules — that's conditional M4
   - No automatic invocation from `brain-sync` — agent invokes this skill explicitly

5. **Context-loading depth — agent judgment**:
   - SKILL.md gives the loading recipe (both versions + prototype + history + references); agent decides how deep to go
   - For a one-line scalar conflict, full-history context is overkill
   - For a contested itinerary section, the more context the better

### `nous/nous/skills/nous-resolve/preserve.py`

Spec (~30 lines):

```python
#!/usr/bin/env python3
"""Pre-merge preservation. Snapshot both versions of a conflict pair to
.brain/merges/<utc-iso>-<canonical-slug>/ before nous-resolve writes the
merged result. Safety floor: any nous-resolve merge can be reverted by
restoring from this snapshot.

Usage:
    preserve.py <canonical> <conflict-file>

Writes:
    <brain-root>/.brain/merges/<utc-iso>-<canonical-slug>/canonical<ext>
    <brain-root>/.brain/merges/<utc-iso>-<canonical-slug>/peer<ext>
    <brain-root>/.brain/merges/<utc-iso>-<canonical-slug>/meta.json

meta.json shape:
    {
      "canonical": "<canonical-relative-path>",
      "conflict_file": "<conflict-file-relative-path>",
      "peer": "<peer-id-from-conflict-filename>",
      "conflict_ts": "<utc-iso-from-conflict-filename>",
      "preserved_at": "<utc-iso-now>"
    }

Requires: canonical and conflict-file must be in the same brain (have a
common ancestor with .brain/config.md); refuses otherwise.
"""
```

Implementation: stdlib only. Walks parents to find brain root via `.brain/config.md`. Parses peer + conflict-ts from filename pattern `<base>.conflict-<peer>-<utc>Z.<ext>`. Slugifies canonical's relative path for the snapshot dir name (`/` → `-`, drop extension). Writes the two files + meta.json atomically (write to temp, rename).

### `nous/nous/skills/nous-resolve/find-conflicts.sh`

Spec (~10 lines):

```bash
#!/usr/bin/env bash
# Output: one (canonical, conflict-file, peer, ts) tuple per line, tab-separated.
# Usage: find-conflicts.sh <brain-root>
```

Implementation: `find <brain-root> -type f -name '*.conflict-*-*Z.*'`, then per match, extract canonical (strip `.conflict-...` segment), peer, timestamp via parameter expansion. No git ops; purely filesystem.

### `nous/nous/skills/nous-resolve/test-synthetic.sh`

Spec (~50 lines):

```bash
#!/usr/bin/env bash
# Build a synthetic brain in a tmpdir, inject a conflict pair on a
# travel-plan file, drive nous-resolve via the agent (or by simulating
# the skill steps end-to-end), assert:
#   1. .brain/merges/<ts>-<slug>/ contains both pre-merge versions + meta.json
#   2. canonical was rewritten with the merged content
#   3. conflict file was deleted
#   4. git log shows a "merge: ... via /nous-resolve" commit
```

Note: this test exercises the *mechanical* layer (preserve.py + find-conflicts.sh + git ops). The *semantic* layer (does the agent merge correctly?) can only be tested by actually invoking the skill in a session — the M3 dogfood is what calibrates that.

### `nous/nous/nous.manifest` — add one line

```diff
 # ── Nous skills ──────────────────────────────────────────────────────────────
 symlink   nous/skills/nous-tools    .claude/skills/nous-tools
 symlink   nous/skills/charon        .claude/skills/charon
+symlink   nous/skills/nous-resolve  .claude/skills/nous-resolve
```

After updating, run `nous/setup.sh` (no flags = refresh) in any consumer repo to install the new symlink.

---

## Plan — chunked

### Chunk 1: scaffold the skill directory + manifest entry
- Create `nous/nous/skills/nous-resolve/` (directory)
- Add `nous/nous/skills/nous-resolve/.gitkeep` so the dir tracks
- Add manifest line to `nous/nous/nous.manifest`
- Run `nous/setup.sh` in nous itself to install the symlink (validates manifest entry parses)
- Verify `nous/.claude/skills/nous-resolve` resolves (symlink check)

### Chunk 2: SKILL.md
- Write SKILL.md per spec above (frontmatter + 5 numbered body sections)
- Length target: ~150-200 lines. Concise but complete enough that the agent doesn't need to re-derive the merge rules each invocation.

### Chunk 3: preserve.py
- Write per spec above
- Standalone smoke-test: build a fake conflict pair in `/tmp/fake-brain/.brain/config.md` + `/tmp/fake-brain/data/foo.md` + `/tmp/fake-brain/data/foo.md.conflict-peerB-2026-05-08T12:00:00Z.md`
- Run `preserve.py /tmp/fake-brain/data/foo.md /tmp/fake-brain/data/foo.md.conflict-peerB-2026-05-08T12:00:00Z.md`
- Assert: `.brain/merges/<ts>-data-foo/` contains canonical.md, peer.md, meta.json with correct shape

### Chunk 4: find-conflicts.sh
- Write per spec above
- Smoke-test against the same `/tmp/fake-brain/` fixture; assert one tuple emitted

### Chunk 5: test-synthetic.sh
- Build the fixture: empty git repo, `.brain/config.md`, two travel-plan markdown files (canonical + conflict pair)
- Invoke the skill's mechanical steps (preserve.py → write merged content → delete conflict file → git commit)
- Assert artifacts as listed in spec
- Note: the *agent-driven merge step* is mocked here (just write a hand-crafted "merged" content). The agent-merge correctness is M3's territory.

### Chunk 6: end-to-end dry-run inside nous itself
- Pick any brain (or create a sandbox brain): `~/workspace/brain-shared-test/` (might exist from prior nous#4 testing)
- Inject a synthetic conflict pair on a throwaway file
- Invoke `/nous-resolve <brain-root>` from the current Claude Code session
- Walk through the 7-step procedure interactively
- Verify: merge applied, preservation snapshot written, commit lands, conflict file gone

### Chunk 7: tick M1 plan items + close M1 (separate user-driven step)
- Tick all four `[ ]` items under M1 in the issue file
- User runs `make close-issue ISSUE=5 MILESTONE=M1 ACTUAL=<derived> VERIFIED='ran chunk 6 end-to-end on brain-shared-test, merge committed, preservation snapshot intact'`

---

## Out of M1

- **Undo path** (`/nous-resolve undo`) — M2.
- **Real-conflict dogfood** — M3, gated on nous#12.
- **Section-aware declarative merge** (`merge: union` etc. in prototype frontmatter) — conditional M4, ships only if M3 dogfood reveals real failures the prompt-guided merger can't be coaxed out of.

## Open questions / risks

1. **Prototype location discovery**: SKILL.md says "look in `nous/construct/datatype/`" but a brain consuming nous-resolve might not have nous as a sibling. Resolution rule: try `<brain-root>/construct/datatype/<type>.md` first (vendored prototype, if any), then fall back to `<brain-root>/../nous/construct/datatype/<type>.md` (sibling lookup). If neither, no prototype — heuristics only.

2. **Multi-peer conflicts**: file-level resolution can produce `<file>.conflict-peerB-...md` AND `<file>.conflict-peerC-...md` simultaneously if three peers diverge. M1 procedure: resolve in timestamp order, treating each conflict file as a successive merge against the current canonical.

3. **Binary files**: brain content is markdown by convention, but if a brain has images / PDFs and one gets a conflict file, the AI-prose merger doesn't apply. M1 behavior: detect non-markdown extensions; refuse with "binary conflict needs manual resolution; choose canonical or peer with `mv`."

4. **Commit author identity**: the merge commit is authored as the local user. brain-sync's identity (`peer-id` from `.brain/config.md` or git config) is reflected in the commit message body, not the author. This is consistent with brain-sync's own convention.
