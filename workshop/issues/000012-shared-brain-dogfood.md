---
id: 000012
status: open
deps: [000004]
created: 2026-05-08
updated: 2026-05-08
estimate_hours:
---

# shared-brain dogfood — wife/me forcing-function

## Problem

The shared-brain project's `done_when` criterion is wife and me co-authoring `data/life/travel/2026-08-01-paris.md` end-to-end via `brain-shared-family` over ≥2 weeks of daily use, with ≥3 conflicts resolved by `/brain-resolve` and human-confirmed without data loss. That validation gate is the milestone that proves the substrate (`nous#4`) and the merge skill (`nous#5`) actually work in practice — not synthetic tests. Carved out of `nous#4 M4` so #4 can close on shipping the daemon while the dogfood is tracked as its own portfolio item.

## Spec

The MVP validation slot for the shared-brain project. Three operational pieces:

1. **Provision `brain-shared-family`** — gcrypt-encrypted brain admitted to two recipients (me + wife). Same flow as `nous#3`'s `brain-private` provisioning but multi-recipient. Place an initial `data/life/travel/2026-08-01-paris.md` (or shared-family equivalent) seeded with whatever notes already exist.

2. **Onboard wife's machine** — `make nous-bootstrap` on her Mac; her GPG keypair via `make identity` (or import if she already has one); admit her recipient to `brain-shared-family`; install the brain-sync daemon. This is the first real cold-start of `nous#11`'s bootstrap toolchain on a *different operator's machine* — surfaces friction that the VM dry-run (`nous#10`) couldn't.

3. **Run the dogfood for ≥2 weeks** — both peers edit the Paris plan as natural use evolves. Log every conflict that surfaces, how it was resolved (manual until `nous#5 M1` lands, then `/brain-resolve`), and whether the resolution preserved intent.

The dogfood is the load-bearing experiment. It tells us:
- Whether `nous#4`'s file-level-conflict-only semantics hold up against real edit patterns. If one of us routinely overwrites the other's section because we read a stale file before editing, the convention is wrong.
- Whether `nous#5`'s whole-file AI-prose merge handles the bulk of real conflicts gracefully (load-bearing v1 claim).
- Whether `nous#5 M4` (declarative section merges) is actually needed, or if the AI path covers everything we hit.
- Whether `nous#7`'s lock primitive is necessary, or if daily verbal coordination + good semantic merge obviates it.

## Done when

- `brain-shared-family` exists with both me and wife as recipients, syncing on both machines.
- The Paris trip plan is co-authored over ≥2 weeks of daily use.
- At least 3 conflicts have been resolved by `/brain-resolve` (i.e. after `nous#5 M1` ships) and human-confirmed without data loss.
- A log of every conflict that arose during the window — root cause, resolution path, verdict (clean / acceptable / wrong) — exists in this issue's `## Log` or in a linked artifact under `brain/data/`. This log is the evidence that informs whether to ship `nous#5 M4` and `nous#7`.

## Plan

### M1 — provision `brain-shared-family` + place initial trip plan

- [ ] Create `brain-shared-family` via `make new-brain` (or analog) with both my fingerprint and wife's fingerprint as gcrypt recipients.
- [ ] Round-trip clone-and-decrypt verified end-to-end on at least one machine.
- [ ] Seed `data/life/travel/2026-08-01-paris.md` with whatever notes already exist (or an empty travel-plan instance if starting fresh).
- [ ] `.brain/config.md` declares `mode: shared`, `recipients: [me, wife]`.

### M2 — onboard wife's machine

- [ ] Wife's Mac runs `make nous-bootstrap` to completion.
- [ ] Wife's GPG keypair set up (via `make identity` or import).
- [ ] Wife's fingerprint added as a recipient to `brain-shared-family` (already in M1's recipient list — this milestone confirms the round-trip works on her machine).
- [ ] Wife's machine clones `brain-shared-family` via gcrypt; round-trip edit + commit + sync verified.
- [ ] brain-sync daemon installed on her machine via `brain-sync service install`.

### M3 — dogfood ≥2 weeks

- [ ] Both peers edit the Paris plan over ≥2 weeks of natural use.
- [ ] Every conflict logged: timestamp, files involved, root cause (e.g. simultaneous edit, stale read, etc.), resolution path (manual / `/brain-resolve`), verdict (clean / acceptable / wrong), preservation outcome (any data loss?).
- [ ] After window closes, evaluate the log: did file-level convention hold? Does `/brain-resolve` cover the failure modes? Are locks (`nous#7`) needed?

## Notes

- **Soft dep on `nous#5` M1+M2**: ideally `/brain-resolve` ships *before* the dogfood window starts so the very first conflict gets exercised through the skill. If M3 starts before #5 M1 lands, conflicts are resolved by hand for the interim — usable but less informative for skill calibration.
- **Out of scope**: lock primitive (`nous#7`); cross-brain reference syntax (`nous#6`); brain-shared-* beyond family (e.g. brain-shared-work). All deferred until family-brain is real.
- **Forking risk**: if wife or I prefer not to dogfood for the full ≥2 weeks (e.g. trip planning concludes earlier), shorten the window but require ≥3 real conflicts. The conflict count is the load-bearing evidence; calendar duration is a secondary gate.

## Log

### 2026-05-08 — created
Carved out of `nous#4 M4` after recognizing the dogfood is a portfolio milestone with its own provisioning + onboarding + multi-week-window structure, distinct from #4's daemon-ships scope. Tracking it here lets `nous#4` close cleanly on M1–M3 (substrate + daemon + convention + manual resolve) while the dogfood is the standalone validation gate for the project's `done_when`.
