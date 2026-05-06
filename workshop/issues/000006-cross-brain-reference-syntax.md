---
id: 000006
status: open
deps: [nous#3]
created: 2026-05-05
updated: 2026-05-05
estimate_hours: 4
---

# cross-brain reference syntax

## Done when

- A reference of the form `@brain:<name>/<path>` is recognized by the agent runtime as pointing to an artifact in another brain repo.
- Resolution works when the reader has decryption keys (i.e., is a recipient on the target brain). Resolved references behave like ordinary file links — the agent can read the artifact and use it as context.
- Resolution degrades gracefully (skip-with-hint) when the reader is *not* a recipient. The agent must not crash, must not silently elide, and must not attempt to follow refs into brains it cannot decrypt.
- Atlas entry documents the syntax and the resolution semantics so authors and agents converge on the same form.

## Spec

Source: `brain/data/life/42shots/ideas/2026-04-28-04-pensive-workspace-as-proto-company.md`.

Once a person holds keys to multiple brains (personal + family + 42shots + …), artifacts in one brain naturally want to point at artifacts in another. A pensive in personal brain may reference a product spec in shared brain; a project file in shared brain may reference a pensive in personal brain. A bare relative path is wrong because the target lives in a sibling repo, not under the same root.

The pensive proposes `@brain:<name>/<path>` as the explicit reference form. The leading `@brain:` makes the cross-brain hop visible at parse time, so the resolver can look up the brain by `<name>`, find its checkout location (typically `~/workspace/brain-<name>` or the like), and resolve the rest of the path within that tree.

Two failure modes both have to be handled cleanly:

1. **Reader has the brain checked out and decrypted.** Reference resolves; agent reads the file as context.
2. **Reader is not a recipient on the target brain (no keys, possibly no checkout).** Reference fails informatively. The agent gets a "this reference points into brain `<name>` which you don't have access to" hint, *not* a stack trace, and *not* silent elision. Not trying to decrypt is the right posture — the agent should not see encrypted bytes as evidence of anything other than "not for me."

The skip-with-hint posture is a direct consequence of the trust-boundary model in `brain/atlas/threat-model-shared-brain.md`: the encryption boundary is at decryption-on-disk, so a brain we don't have keys for is genuinely unreadable, not a permissions bug to surface. Treating "no keys" as a structured "not for me" rather than an error is how we keep cross-brain references useful in mixed-recipient settings without leaking information about *what* the unreadable brain contains.

Per-person `brains.md` registry (mapping brain names to clone URLs and checkout paths) is deferred until it bites. v1 can hard-code the common case (`@brain:family` → `~/workspace/brain-shared-family` or wherever the user keeps it) and surface "unknown brain name" cleanly.

Out of scope: reference rot detection (when a referenced artifact moves or is deleted). Address with either a CI-style check or lazy-fix-on-encounter — decide once real usage exists.

## Estimate

Range: **3–6 hr**. Best guess: **~4 hr**.

*Produced via `brain/data/life/42shots/velocity/estimate-logic-v2.1.md` against `baseline-v2.1.md`. Method A only.*

| Milestone | Primitive | Design (×0.5) | Impl (×1.0 familiarity) | Total |
|---|---|---|---|---|
| M1 — syntax + parser | Greenfield Go (small) | 0.25–1 | 0.3–0.8 | 0.55–1.8 |
| M2 — resolver + skip-with-hint | Smaller Go + branching logic | 0.1–0.4 | 0.4–1 | 0.5–1.4 |
| M3 — agent / dispatcher integration | Cross-cutting refactor | 0.1–0.5 | 0.2–0.5 | 0.3–1 |
| M3 — round-trip test | Verification | 0.05–0.15 | 0.3–0.8 | 0.35–0.95 |
| Atlas docs (×2 entries) | Atlas/docs | 0.05–0.2 | 0.1–0.4 | 0.15–0.6 |
| **Subtotal** | | 0.55–2.25 | 1.3–3.5 | 1.85–5.75 |
| **+30% design buffer** | | +0.15–0.7 | n/a | +0.15–0.7 |
| **Total** | | | | **~2–6.5 hr** |

Familiarity ×1.0 (parser + resolver are very familiar Go patterns). Spec-quality discount ×0.5 — moderate; the syntax is sketched but not fully bound. The `<name>` resolution pattern (mapping name → checkout path) is the part most likely to grow scope if a registry is needed sooner than the issue defers it.

## Plan

### M1 — syntax + parser

- [ ] Define the syntax precisely: what characters are legal in `<name>` and `<path>`, anchor rules, escaping.
- [ ] Implement a parser that recognizes `@brain:<name>/<path>` in any text the agent reads (markdown body, frontmatter values, prose).
- [ ] Document the syntax in `atlas/` and in the relevant datatype docs (pensive, project, etc.).

### M2 — resolver with skip-with-hint

- [ ] Discover candidate brain checkouts on disk by scanning for `.brain/config.md` manifests under the workspace root (per the threat-model's *Brain identification* convention). Build an in-memory map: `manifest.name` → checkout path.
- [ ] Resolve `<name>` against this map (not against directory names). A brain renamed or moved on disk still resolves correctly via its manifest.
- [ ] Resolve the path within the checkout; return the file contents to the caller.
- [ ] If the brain isn't checked out (no manifest found with that name), or the file doesn't exist, or decryption fails: return a structured hint, *not* the raw error or empty content.
- [ ] Wire into the agent's read-this-context flow so cross-brain refs in prose actually behave as references.

### M3 — agent / dispatcher integration

- [ ] Update agent prompts / dispatcher logic to recognize the `@brain:` form when synthesizing references during writing (don't write bare relative paths for cross-brain pointers).
- [ ] Update brain-resolve, project-authoring, and similar skills to follow `@brain:` refs into other brains when building context.
- [ ] Verify by authoring a test pensive in personal brain that references an artifact in a shared brain; round-trip read.

## Log

### 2026-05-05

- Issue spec'd from `brain/data/life/42shots/ideas/2026-04-28-04-pensive-workspace-as-proto-company.md`. M4 of the shared-brain project. Not in MVP — deferred until cross-brain references actually exist (i.e., once ≥1 shared brain is in real use).
