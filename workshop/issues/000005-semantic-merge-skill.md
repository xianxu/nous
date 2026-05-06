---
id: 000005
status: open
deps: [nous#4]
created: 2026-05-05
updated: 2026-05-05
estimate_hours: 7
---

# semantic merge skill

## Done when

- `/brain-resolve` skill exists and resolves conflict files produced by issue #4's sync daemon, with travel-plan as the first dogfood datatype.
- The load-bearing capability — whole-file AI-prose merge with full brain context (related artifacts, parley chats, recent commits) in scope — works on real conflicts from the wife-trip-planning forcing function (#4 M4).
- A merge always preserves both pre-merge versions in `.brain/merges/<timestamp>-<path>/` so a wrong merge is one command away from undo.
- Human confirmation is required before the merge commit lands. Daemon proposes; daemon does not autonomously commit.
- Prototype-level `merge:` declarations + section-aware merge ship **only if** dogfood reveals the AI-prose path doing something dumb on structured sections (e.g., dissolving a `travelers:` list into a paragraph). Conditional, not gating.

## Spec

Source: `brain/data/life/42shots/ideas/2026-04-28-01-pensive-collaborative-brain.md` §Semantic merge.

This issue is **iterative** — we ship a load-bearing v1 and let dogfood with the family-trip forcing function tell us what's actually needed beyond it. Travel-plan (`nous/construct/datatype/travel-plan.md`) is the guinea-pig datatype: it's the artifact wife and I will both edit, it has a representative mix of structured fields (frontmatter, day-keyed itinerary, bookings list, travelers list) and free-form prose (Tentative plans, Logistics, Open questions), and it's the thing the trip-planning use case actually uses.

**The load-bearing claim:** whole-file AI-prose merge with full brain context in scope is qualitatively different from `diff3`, and probably handles the bulk of real conflicts gracefully on its own. The LLM has the prototype, the parley chats, recent commits, and related artifacts all in context — it knows "this is a list of travelers" without anyone telling it. Most travel-plan conflicts will be mundane (both peers edited Itinerary on different days; one added a booking while the other was editing Open questions) and an LLM with the file in scope reconciles them sensibly.

**The conditional refinement:** when M1 (whole-file merge) ships and we run it on real conflicts, we'll see whether the LLM ever does something dumb on a structured section. The pensive's classic case — both peers add a traveler, LLM sloppily writes a paragraph instead of unioning the list — is the kind of failure we'd want declarative section-level rules to prevent. If that failure shows up in dogfood, ship M3 (prototype `merge:` declarations + section-aware enforcement). If it doesn't, M3 is over-engineering.

Non-determinism is real (LLMs aren't deterministic), so human-confirm is a correctness guarantee, not ceremony. Pre-merge preservation in `.brain/merges/` (M2) makes the confirmation cheap to reverse and is *not* conditional — it's the safety floor for any version of merge.

Sketched vocabulary the pensive proposes for declarations, kept here for when M3 lights up:

```
travelers:        merge: union
status:           merge: latest-wins
bookings:         merge: by-key(leg)
open-questions:   merge: union
itinerary:        merge: by-key(date)
prose:            merge: ai-prose
```

Out of scope for this issue: lock auto-acquisition during resolution (lives in #7 once locks exist, and locks themselves are deferred). The skill should be designed so adding lock integration later is a small wiring change, not a redesign.

## Estimate

Range: **5–11 hr**. Best guess: **~7 hr**.

*Produced via `brain/data/life/42shots/velocity/estimate-logic-v2.1.md` against `baseline-v2.1.md`. Method A only.*

| Milestone / component | Primitive | Design (×0.5) | Impl (×1.5 familiarity) | Total |
|---|---|---|---|---|
| M1 — `/brain-resolve` skill scaffold + dispatcher | Single skill | 0.15–0.5 | 0.3–0.75 | 0.45–1.25 |
| M1 — conflict-file detection + parser | Smaller Go | 0–0.15 | 0.3–0.75 | 0.3–0.9 |
| M1 — diff + confirm UI flow | TUI screen (light) | 0.25–1 | 0.45–1.5 | 0.7–2.5 |
| M1 — pre-merge preservation in `.brain/merges/` | Smaller Go | 0–0.15 | 0.3–0.75 | 0.3–0.9 |
| M2 — undo path + atlas docs | Smaller Go + docs | 0.1–0.4 | 0.3–0.6 | 0.4–1 |
| M3 — dogfood (focused per-conflict analysis) | Verification + log | 0.1–0.3 | 0.75–1.5 | 0.85–1.8 |
| M4 — *(50% probability)* prototype declarations + section-aware merge | Greenfield + datatype edit | 0.5–1.5 | 0.6–1.5 | 1.1–3 |
| M4 — *(50% probability adjustment)* | apply ×0.5 | 0.25–0.75 | 0.3–0.75 | 0.55–1.5 |
| **Subtotal (M1–M3 + M4 weighted)** | | 0.85–3.25 | 2.7–6.6 | 3.55–9.85 |
| **+30% design buffer** | | +0.25–1 | n/a | +0.25–1 |
| **Total** | | | | **~4–11 hr** |

M4's range is conditional (won't ship unless dogfood reveals real failures); applied a 50% probability to its weighted contribution. If M4 is skipped entirely, the lower band drops by ~0.5 hr; if M4 ships in full, the upper band rises by ~1.5 hr. Familiarity ×1.5 because skill-building patterns are familiar from charon work but the merge prompt + diff-confirm flow is new.

## Plan

### M1 — `/brain-resolve` v1: whole-file AI-prose merge (load-bearing)

- [ ] Skill scaffold under `nous/.claude/skills/` (or wherever skills live in this repo).
- [ ] Detect conflict files (Syncthing-style or git-conflict-style, per #4 M3).
- [ ] Whole-file AI-prose merge: read both versions, the prototype, and ambient context (related artifacts, parley chats); produce a merged version; show diff to the human; confirm; write.
- [ ] Pre-merge preservation: copy both pre-merge versions to `.brain/merges/<ISO-timestamp>-<path-slug>/` before the commit. Non-conditional — safety floor.
- [ ] Verify on a synthetic travel-plan conflict.

### M2 — undo path

- [ ] One-command `/brain-resolve undo` that restores the most recent merge from `.brain/merges/`.
- [ ] Document the trail: how to find merges from a week ago, how to expire old ones.
- [ ] Pulled in early (rather than last) because undo is the safety net that makes non-deterministic AI merges acceptable in the first place.

### M3 — dogfood with travel-plan as guinea pig

- [ ] Run M1 on every real conflict that arises in the wife/me Paris-trip dogfood (issue #4 M4).
- [ ] Log each conflict, the merged output, and a verdict: *clean* (LLM did the obvious right thing), *acceptable-but-prose-y* (LLM did something fine but more verbose than ideal), or *wrong* (LLM dissolved structure or invented content).
- [ ] After ~5–10 conflicts, evaluate: do we need M4? If `wrong` count is zero or rare, stop here. Travel-plan-only annotation is also an option (M4 scoped down).

### M4 — *(conditional)* prototype merge declarations + section-aware merge

Only ship if M3 dogfood shows real failures the LLM can't be coaxed out of with prompt tweaks.

- [ ] Extend datatype frontmatter / body schema to carry per-section `merge:` declarations.
- [ ] Define the vocabulary precisely: `union`, `latest-wins`, `by-key(<field>)`, `ai-prose`. Document each.
- [ ] Annotate `travel-plan.md` first (the guinea pig). Other datatypes annotated as their own conflicts start biting — incremental, not all-at-once.
- [ ] Resolver respects declared rules per section; falls back to AI-prose for sections without a declaration.
- [ ] Surface irreducible contradictions clearly (don't auto-pick a winner when neither rule nor LLM has confident judgment).
- [ ] Verify against the specific failures M3 surfaced.

## Log

### 2026-05-05

- Issue spec'd from `brain/data/life/42shots/ideas/2026-04-28-01-pensive-collaborative-brain.md`. M3 of the shared-brain project.
- Reordered ahead of issue #7 (lock primitive): if AI-merge handles conflicts well, locks may not be needed at all (per pensive's own analysis).
- Reframed as iterative, with travel-plan as guinea-pig datatype. Whole-file AI-prose merge is the load-bearing v1 (M1); section-aware merge with prototype declarations (M4) is conditional on dogfood findings, not gating MVP. Insight: an LLM with the prototype + ambient context already knows "this is a list of travelers" without declarative help; declarations are insurance against specific failure modes, ship them when those modes are observed.
