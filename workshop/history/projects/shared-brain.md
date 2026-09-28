---
type: project
name: shared-brain
goal: "Take brain from solo-operator to multi-person collaboration — encrypted at rest, near-real-time sync, semantic merge for typed-data conflicts — driven by the wife/me trip-planning forcing function."
done_when: "Wife and I co-author `data/life/travel/2026-08-01-paris.md` end-to-end via `brain-shared-family` over ≥2 weeks of daily use, with ≥3 conflicts resolved by `/brain-resolve` and human-confirmed without data loss."
status: done
operator: xianxu
mvp_scope: [nous#8, ariadne#22, nous#3, nous#10, nous#4, nous#5, nous#12, nous#14]
explicitly_out: [nous#6, nous#7, nous#51]
created: 2026-05-05
updated: 2026-06-02
closed: 2026-06-02

sources:
  - /Users/xianxu/workspace/brain/data/life/42shots/ideas/2026-04-28-01-pensive-collaborative-brain.md
  - /Users/xianxu/workspace/brain/data/life/42shots/ideas/2026-04-28-04-pensive-workspace-as-proto-company.md
  - /Users/xianxu/workspace/nous/workshop/issues/000003-brain-should-be-private-with-encryption-at-risk-in-github.md
  - /Users/xianxu/workspace/nous/workshop/issues/000004-shared-brain-sync-daemon.md
  - /Users/xianxu/workspace/nous/workshop/issues/000005-semantic-merge-skill.md
  - /Users/xianxu/workspace/nous/workshop/issues/000006-cross-brain-reference-syntax.md
  - /Users/xianxu/workspace/nous/workshop/issues/000007-lock-primitive.md
  - /Users/xianxu/workspace/nous/workshop/issues/000008-shared-brain-threat-model.md
  - /Users/xianxu/workspace/charon/workshop/issues/000021-gpg-agent-lifecycle-integration.md
  - /Users/xianxu/workspace/ariadne/workshop/issues/000022-brain-manifest-convention.md
  - /Users/xianxu/workspace/nous/workshop/issues/000051-audit-apple-security-setup.md
  - /Users/xianxu/workspace/ariadne/workshop/issues/000023-schedule-datatype.md
  - /Users/xianxu/workspace/nous/workshop/issues/000012-shared-brain-dogfood.md
  - /Users/xianxu/workspace/nous/workshop/issues/000013-brain-cli-unification.md
  - /Users/xianxu/workspace/nous/workshop/issues/000014-absorb-charon-unified-nous-cli.md
---

# shared-brain

A focused push to take brain from solo-operator mode to two-person collaboration: a written threat model that names the trust boundaries before any migration (`nous#8`), gcrypt encryption at rest (`nous#3`), per-subtree sync with conflict-file semantics (`nous#4`), AI-driven semantic merge that respects prototype-as-merge-contract (`nous#5`), and a unified `nous` CLI/TUI absorbing charon's credential surface for ergonomic recipient management + gpg-agent lifecycle (`nous#14`, supersedes `charon#21`). The forcing function is concrete and small — wife and I co-authoring the 2026-08 Paris trip plan in `brain-shared-family` — and the MVP is sized to that. **Out of MVP:** cross-brain reference syntax (`nous#6` — 42shots-readiness primitive, deferred until ≥1 shared brain is in real use); lock primitive (`nous#7` — pensive's own analysis says daily verbal coordination + good semantic merge may obviate locks entirely; ship only if dogfood reveals friction); **end-of-project Apple-account hardening** (`nous#51` — YubiKey 2FA + Advanced Data Protection — general Apple-ID hygiene, decoupled from the brain bootstrap as of 2026-05-07 when sneakernet replaced iCloud Keychain as the recommended GPG-key-transfer channel). All three are authored as standing issues so the decomposition is captured, but they're not gating completion.

**Why threat model first:** the security boundary is at decryption-on-disk, not per-file or per-agent inside a decrypted brain. That's a deliberate simplification with sharp consequences (brain-private privilege concentration, all agents on a machine read all decrypted brains, granting GPG recipient is easy and revoking is heavy). Writing it down before the migration is what makes the gcrypt design in `#3` defensible, informs the skip-with-hint posture in `#6`, and gates the wife/me consent conversation before `brain-shared-family` is provisioned. Live document at [`atlas/threat-model-shared-brain.md`](../../atlas/threat-model-shared-brain.md).

**Ordering note:** the original pensive build-order was gcrypt → sync → lock → semantic-merge. Flipped here to gcrypt → sync → semantic-merge → (lock, only if needed). Semantic merge is the experiment that tells us whether locks are necessary at all.

**Iterative posture inside #5:** ship whole-file AI-prose merge first (the load-bearing v1) and let dogfood with the travel-plan datatype tell us whether section-aware merge with prototype `merge:` declarations is actually needed. The `done_when` criterion is satisfied by the v1 alone; declarative merge is conditional refinement, not gating.

**Single-GPG-scheme posture (2026-05-06 reshape):** brain-private and brain-shared-\* both use gcrypt with GPG recipient lists — private brains have a one-element list (the user). No separate symmetric passphrase for brain-private. Daily unlock chain is uniform: GPG private key in `~/.gnupg/`, passphrase in macOS login Keychain, fed via pinentry-mac (or pinentry-curses in SSH/headless contexts; auto-detected by `nous/scripts/identity.sh`). Cross-machine GPG-key transfer via **sneakernet** — passphrase-encrypted ASCII export, transferred via an independent channel (AirDrop, encrypted USB, signed message). The earlier iCloud-Keychain-recommended channel was dropped 2026-05-07; see threat-model `## Revisions` 2026-05-06 and 2026-05-07.

## Closeout

**Declared done 2026-06-02.** Everything the project set out to *build* shipped and was validated end-to-end on a one-shot, multi-machine basis: encrypted brains at rest (gcrypt), commit-driven sync, the `/nous-resolve` semantic-merge skill, and the unified `nous` CLI/TUI with the full recipient/identity surface. The headless-VM e2e (`nous#36`), the GitHub-mediated onboarding flow (`nous#26`/`nous#27`), and the leave flow (`nous#32`) each exercise the two-machine path against real GPG + a real GitHub remote. What remains is *operating* the thing over time — which doesn't need the project container open to track.

**Relaxation of `done_when` (2026-06-02):** the literal criterion (wife + me co-author the Paris plan over ≥2 weeks via `brain-shared-family`, ≥3 `/brain-resolve` conflicts, no data loss) is reframed rather than met. Infrastructure-complete + one-shot two-machine validation constitute "done" for the *build* project. The durable continuous-use experiment — and the conflict-stream evidence that would tell us whether `nous#5 M4` (declarative section merge) and `nous#7` (locks) are ever needed — lives on in `nous#12`, now an independent dogfood task under target `shared-brain-infrastructure-and-ui` rather than a project gate. The 2026-06-01 reframing in `nous#12`'s log (the gap is a *durable* daily-use brain, not one-shot sync) is the standing scope for that follow-on.

**Beyond the original MVP (wave 2), folded in here:** scope widened during build from "two-person gcrypt brain" to a general shared-brain *product surface*:
- **Runtime unification** — single `nous serve` foreground daemon (`nous#16`); retirement of the standalone `charon`/`brain-sync` binaries (`nous#20`).
- **Onboarding without sneakernet** — GitHub-mediated recipient onboarding (`nous#26`): `nous brain invite`/`join` publish pubkeys to a `keys` branch, operator auto-imports + auto-admits; three onboarding polishes (`nous#27`).
- **Collaborator lifecycle** — leave a shared brain (`nous#32`); recipient-remove state cleanup + re-invite (`nous#38`/`nous#39`); unified per-brain person removal (`nous#40`). Captured as its own invariant in target `collaborator-state-machine`.
- **Local-only brains** — private brains with no GitHub backing (`nous#33`).
- **Performance + ergonomics** — `git ls-remote` negative-cache for brain-poll (`nous#34`); autosave + `nous push` checkpoint (`nous#30`); async TUI list load + cache (`nous#31`).
- **Headless testing** — scriptable VM brain e2e + non-interactive identity init + `--verified-last8` ceremony (`nous#36`).

**Open follow-ons (not gating; tracked under the two targets, not this closed project):**
- `nous#12` — durable ≥2-week dogfood (relaxed out of the gate, above).
- `nous#37` — system-wide identity revocation (surfaced during #12 setup; prerequisite for a clean durable run).
- `nous#41` — collaborator-lifecycle hardening — **done 2026-06-02** (all 12 codex-review findings closed across M1–M4; merged via PR #2). Tracked under target `collaborator-state-machine`, not this closed project.
- `nous#35` — compacting encrypted brain state (pack/history shrink for faster clones).
- Code-complete but pending operator-side e2e/codesign — `nous#16` (codesign-namespace verify), `nous#20`/`nous#30`/`nous#32` (host e2e). Formal `sdlc close` deferred to one operator verification pass; the build is done.

**Calibration note:** design-heavy `nous#14` milestones (M2/M3) landed 5–10× under estimate — heavy design conversation front-loaded into early segments leaves execution-only milestones cheap. The systematic over-estimate on thorough-spec-with-make-targets work (`nous#3`, `nous#5`) held throughout. Per-milestone actuals in the detail blocks below and in the wave-2 table.

## tasks

- [x] write threat-model document [nous#8 M1]
- [x] wire threat model into #3, #6, project lede [nous#8 M2]
- [x] review threat model with wife before brain-shared-family is provisioned [nous#8 M3]
- [x] brain manifest convention in ariadne AGENTS.md [ariadne#22 M1]
- [x] propagate convention to nous, brain, charon via make refresh [ariadne#22 M2]
- [x] provision new gcrypt'd brain-private repo + mirror content [nous#3 M1]
- [x] ~~mirror-sync helper for the migration window~~ — N/A, cutover landed same-day [nous#3 M1.5]
- [x] paired-device + recipient layout in `keys/` [nous#3 M2]
- [x] ~~second-machine bootstrap dry-run~~ — moved to nous#10 [nous#3 M3 step 3a]
- [x] rename-and-cutover (backup + destructive rename) [nous#3 M3 steps 3b–3d]
- [x] ~~1-week verification window~~ — wall-clock, tracked outside #3 (started 2026-05-06) [nous#3 M3 step 3e]
- [x] ~~cleanup `brain.legacy*` + `xianxu/brain-backup`~~ — gated on nous#10 (local), then 1-month wall-clock (cloud) [nous#3 M3 step 3f]
- [x] second-machine bootstrap dry-run end-to-end [nous#10]
- [x] ~~charon#21 (gpg-agent lifecycle)~~ — absorbed into nous#14 M3-M4 as `nous identity agent` cluster. Charon repo will be archived; charon#21 closes when nous#14 ships the equivalent surface.
- [x] substrate decision spike (Syncthing vs git+daemon) [nous#4 M1]
- [x] brain-sync daemon (commit-driven, charon-pattern CLI) [nous#4 M2]
- [x] conflict-file convention + manual resolve flow [nous#4 M3] — convention finalized in atlas/sync-substrate-decision.md; synthetic conflict test moves to M2's exercise loop
- [x] /nous-resolve v1 — whole-file AI-prose (load-bearing) [nous#5 M1]
- [x] undo path from .brain/merges/ [nous#5 M2]
- [x] subtree-merge charon → nous; both binaries build [nous#14 M1]
- [x] extract lib/tui, lib/agent, lib/service shared libs [nous#14 M2]
- [x] cmd/nous cobra root; subcommand restructuring; absorb charon#21 [nous#14 M3]
- [x] net-new commands: identity cluster, brain recipient w/ safeguards, brain new guided, obs status/doctor [nous#14 M4]
- [x] TUI shell (bubbletea status board + drill-in submenus) [nous#14 M5]
- [x] provision brain-shared-family + place initial trip plan [nous#12 M1]
- [x] onboard wife's machine (bootstrap + GPG + recipient + brain-sync) [nous#12 M2]
- [.] dogfood ≥2 weeks; log every conflict + resolution — relaxed to follow-on, see Closeout [nous#12 M3]
- [.] real-conflict run of /brain-resolve on the dogfood stream — deferred (fix-forward), see Closeout [nous#5 M3]
- [.] *(conditional)* prototype declarations + section-aware merge — deferred (fix-forward), see Closeout [nous#5 M4]
- [x] unified `nous serve` foreground daemon + dev/prod workflow [nous#16]
- [x] retire standalone charon + brain-sync binaries [nous#20]
- [x] new-brain REST endpoint (fresh-account GraphQL lag) [nous#25]
- [x] GitHub-mediated recipient onboarding (invite/join/auto-admit) [nous#26]
- [x] three onboarding polishes (operator marker, clone-side import) [nous#27]
- [x] local-only private brain (no GitHub backing) [nous#33]
- [x] brain-poll negative cache via git ls-remote [nous#34]
- [x] brainsync autosave + `nous push` checkpoint [nous#30]
- [x] TUI list async load + cache [nous#31]
- [x] leave a shared brain — `nous brain leave` + TUI `l` [nous#32]
- [x] headless-VM brain e2e + non-interactive identity [nous#36]
- [x] recipient-remove state cleanup + brain invite re-send [nous#38, nous#39]
- [x] unified per-brain person removal at any lifecycle stage [nous#40]
- *(post-close follow-ons `nous#37`/`nous#41`/`nous#35` are NOT project milestones — they live in the Closeout's "Open follow-ons" prose + the two targets. Listing them as task rows here made `sdlc milestone-close` mis-treat them as project milestones needing detail blocks; see `ariadne` F3.)*
- [ ] buy + pair hardware security keys to Apple ID [nous#51 M1]
- [ ] enable Advanced Data Protection + recovery posture [nous#51 M2]
- [x] ~~move GPG private key from local Keychain to iCloud Keychain~~ — wontfix 2026-05-07 (sneakernet is the chosen bootstrap channel) [nous#51 M3]
- [ ] write Apple-security audit log [nous#51 M4]

## details

<a id="nous-8-m1"></a>
### nous#8 M1 — write threat-model document

**est:** 0.25–0.8h
**actual:** 0.3h
**closed:** 2026-05-05

Document landed at `brain/atlas/threat-model-shared-brain.md` with the eight subsections the issue plan called for: trust-boundary table (host/device/agent/recipient), brain-private privilege-concentration, agent-access posture, per-recipient revocation cost, passphrase-storage with the four modes, selected default, out-of-scope list, open questions. Indexed into `atlas/index.md`.

Selected default for brain-private passphrase storage: **macOS Keychain**. Justified inline in the threat model — the agent-as-threat model already concedes any agent on the device can read brain-private's plaintext, so making the passphrase keychain-fetchable does not give a new capability to that threat. 1Password CLI kept as the configurable alternative for cross-device sync; gpg-agent / pinentry-mac kept as the canonical pattern for `brain-shared-*` (asymmetric) flows but a shape mismatch for brain-private's symmetric passphrase.

Surprise during writing: the per-recipient revocation cost section turned out to be the load-bearing one for the wife/me consent conversation in M3. Revoking access is structurally heavy (rotate keys, re-encrypt, assume past content leaked) — that's a property of any encryption-at-rest scheme, but the social commitment of admitting a recipient is heavier than the bare GPG-add operation suggests. Worth surfacing explicitly before brain-shared-family is provisioned.

<a id="nous-8-m2"></a>
### nous#8 M2 — wire threat model into #3, #6, project lede

**est:** 0.15–0.4h
**actual:** 0.1h
**closed:** 2026-05-05

Three references added: `#3`'s Spec now points at `brain/atlas/threat-model-shared-brain.md` and names the macOS Keychain default; `#6`'s Spec adds a paragraph explaining the skip-with-hint posture as a direct consequence of the decryption-on-disk boundary; this project file's lede now links the doc.

<a id="ariadne-22-m1"></a>
### ariadne#22 M1 — brain manifest convention in AGENTS.md

**est:** 0.5–1h
**actual:** 0.3h
**closed:** 2026-05-05

Extended AGENTS.md §1's "brain is a special peer" block with the brain identification rule and manifest schema (mode, name, recipients/passphrase_source, sync_substrate). Pointer to the threat model carries the depth; the constitution names the convention. ~1 paragraph net add. Carved out of `nous#3` after recognizing the convention is constitutional-level, not nous-internal.

**Scope event 2026-05-06 (post-close):** schema simplified after the single-GPG-scheme reshape (see threat-model `## Revisions` 2026-05-06). Dropped `passphrase_source:` field — daily fetch is now uniformly gpg-agent + pinentry-mac → Keychain on every machine, so the per-brain knob is unnecessary. `recipients:` becomes always-present (private brains have a one-element list). Net change to AGENTS.md is small (~3 lines); landed as a side-quest commit referencing this issue rather than reopening.

<a id="ariadne-22-m2"></a>
### ariadne#22 M2 — propagate convention to downstream peers

**est:** 0.1–0.3h
**actual:** 0.1h
**closed:** 2026-05-05

Ran `make refresh` in nous and charon to pull the updated AGENTS.md. Brain's AGENTS.md is a symlink to nous's, so brain follows automatically — no separate refresh needed. Verified the new "Brain identification" block landed in both nous and charon.

Note: brain's `make refresh` failed at a sandbox-restricted write to `.claude/settings.json`, but that path doesn't affect AGENTS.md propagation since brain symlinks. Worth fixing the refresh script to skip settings.json for symlink-driven downstreams, but not gating.

<a id="nous-3-m1"></a>
### nous#3 M1 — provision new gcrypt'd brain-private repo + mirror content

**est:** 4–12h
**actual:** 2h
**closed:** 2026-05-06

Provisioned `xianxu/brain-private` on GitHub (private; no issues; no wiki) and `~/workspace/brain-private` locally with full git history (47 commits from `brain` + 1 manifest commit). gcrypt remote configured with single-recipient GPG list (the user's fingerprint). Round-trip clone-and-decrypt verified end-to-end (4.80 MiB, 575 objects, all 48 commits present, manifest intact). Legacy `xianxu/brain` and `~/workspace/brain` completely untouched throughout.

The work doubled as build-out of two reusable make targets in `nous/`:

- **`make identity`** — GPG keypair bootstrap; idempotent; configures pinentry-mac; will probe macOS Keychain for an existing `brain-gpg-key` export (per `nous#51` M3 convention) before generating fresh.
- **`make moveto`** — generic "clone repo to a new path with full history; strip auto-origin" primitive.
- **`make cloneto`** — brain-specific: discovers source GitHub owner via `gh repo view`; prompts for target path with `../<source>-private` default; creates target GitHub repo (private) if missing or confirms force-push if it exists; selects GPG identity from the local keyring; delegates to moveto.sh; configures gcrypt remote; authors `.brain/config.md` per ariadne `AGENTS.md` §1; force-pushes.

Surprises:

- **`gpg --quick-generate-key 'name' rsa4096 default 5y` produces a primary [SC] key with no encrypt subkey.** gcrypt then can't use the key as a recipient. Fix is one `gpg --quick-add-key <fp> rsa4096 encrypt 5y` call; built into `identity.sh` going forward, but the dogfood run hit it before the fix landed.
- **`gpg --list-secret-keys --with-colons` emits two `fpr:` lines per key** (primary + encrypt subkey). My initial parser indexed by fpr count, double-counting one identity as two; the second "identity" had no UID and hit `set -u`. Fix: parse by `sec:` block boundaries.
- **GitHub UI shows "root, 14 years ago"** as the wrapper commit's author/timestamp. Not a bug — gcrypt deliberately backdates and anonymizes the outer wrapper to hide push timing/author from the host. Documented in `nous/atlas/nous/gcrypt-brain-encryption.md`.
- **The `make` "overriding commands for target X" warnings** present every invocation in the brain repo. Not blocking; tracked as a future side-quest.

Step-0 (Apple-account hardening) was scoped *out* of this milestone correctly — `nous#51` is end-of-project follow-on. The iCloud-Keychain bootstrap channel works on a default Apple ID because the GPG-key export is itself passphrase-encrypted (per the threat model's layered-defense argument).

Actual 2h vs 4–12h estimate — at the very low end. v2.1 estimator's known-systematic over-estimate on thorough-spec-with-make-targets work continues to hold; worth logging the calibration data point.

<a id="nous-3-m3-cutover"></a>
### nous#3 M3 (steps 3b–3d) — rename-and-cutover

**est:** 0.45–1.2h (cutover-only slice of M3's 0.7–2.25 + 0.45–1.2 estimates)
**actual:** 0.5h
**closed:** 2026-05-06

Cutover landed same-day as M1 provisioning, collapsing M1.5 (mirror window) and 3c (final mirror sync) to N/A. Sequence executed:

1. Cloud backup: `xianxu/brain-backup` created (private, description tags it as deletable after 1 month).
2. Local backup: `~/workspace/brain.legacy` (cp from pre-cutover state).
3. Destructive rename: `gh repo delete xianxu/brain` → `gh repo rename brain-private → brain` → `mv ~/workspace/brain ~/workspace/brain.legacy.original` → `mv ~/workspace/brain-private ~/workspace/brain` → `git remote set-url origin gcrypt::ssh://git@github.com/xianxu/brain.git`.
4. Manifest body refresh post-rename, committed as `c37a67e` in the new encrypted brain (first commit on the new path).

**Scope event:** step 3a (second-machine bootstrap dry-run) was *skipped* before the destructive cutover — accepted risk for personal MVP given the cloud + local backup channels still exist. **Resolved 2026-05-06:** carved out as `nous#10` (separate issue, depends on `#3`), allowing `#3` to close without leaving the dry-run undone. 3f cleanup is gated on `#10`'s completion rather than on a fixed date.

<a id="nous-3-m2"></a>
### nous#3 M2 — paired-device + recipient layout

**est:** 0.2–0.65h
**actual:** 0.3h
**closed:** 2026-05-06

Authored `brain/keys/README.md` (layout doc) + `brain/keys/paired-devices.md` (initial entry: primary MacBook Pro, fingerprint `0ECF6AC0...3872C2F0`, bootstrapped 2026-05-06). Threat-model gained `## Paired devices and recipient layout` section; `atlas/index.md` linked the new `keys/` tree. Deferred materializing `keys/recipients/` until first shared-brain provisioning (`nous#4` M4) — empty directory carries no information, will be created at admission time.

<a id="nous-3-close"></a>
### nous#3 — close

**est (whole issue):** 6h (P50; range 4–12h)
**actual (whole issue):** 11h
**closed:** 2026-05-06

Computed via `xx-issues` SKILL.md `actual_hours` procedure: `active-time.py` over the issue's commit window across `~/.claude/projects/-Users-xianxu-workspace-{nous,brain}` returned 10.82 hr unified-wall-clock attribution to `#3` (mention-weighted). Rounded up to 11.

Calibration: above P50, within range. The ×1.5 familiarity factor on novel gcrypt+GPG work clearly under-priced it; the `make new-brain` side-quest (~1.5h, commit `nous@6c9c8f2`) also extended the tail.

End state: encrypted `xianxu/brain` is the operational private brain; legacy plaintext `xianxu/brain` is gone (renamed to `brain-private`, then renamed back into the encrypted slot); `brain.legacy*` and `xianxu/brain-backup` remain as safety nets pending `nous#10` (second-machine dry-run) and the wall-clock cleanup deadlines in 3f. Atlas + threat-model + project file all in sync.

<a id="nous-10"></a>
### nous#10 — second-machine bootstrap dry-run

**est:** 1.5h (issue's estimate_hours; original framing as iCloud-Keychain-channel test)
**actual:** 1h
**closed:** 2026-05-07

VM-based dry-run completed end-to-end. Used a tart `tahoe-base` clone (`scratch`), driven via SSH from the host. Validated:

- `make nous-bootstrap` from-scratch (substrate + workflow + GitHub SSH-key auto-register).
- Sneakernet of GPG private key (`gpg --armor --export-secret-keys` on host → `scp` to VM → `gpg --import` in VM). Passphrase prompted in SSH terminal via auto-selected pinentry-curses.
- `make new-brain ../brain-vm-test` provisioned a private GitHub repo, encrypted force-push landed cleanly. Round-trip verified: VM commits decrypt on host, host commits decrypt on VM.

**Friction caught + fixed in `nous#11`** (test harness + bootstrap polish, landed same session): Xcode-CLT polling, GitHub SSH-key flow, identity.sh SSH-detect → pinentry-curses, `GPG_TTY=$(tty)` auto-export to `~/.zshrc`, gpg-agent.conf dedup-grep widened. Each fix has an explanatory comment in the touched script.

**Scope decision:** sneakernet adopted as the canonical brain-bootstrap channel; iCloud Keychain dropped as the recommended path. Triggered downstream changes:

- `nous#51` rescoped: M3 (move GPG to iCloud Keychain) → wontfix; M1/M2 (hardware keys, ADP) retained as general Apple-ID hygiene independent of brain.
- Threat model: `## GPG key bootstrap` rewritten with sneakernet as recommended channel; iCloud Keychain explicitly considered-and-rejected. Revision entry 2026-05-07 captures the reasoning trail.
- `identity.sh`: macOS Keychain probe stays, but reframed in comments as opportunistic-detection rather than "the convention."

`brain.legacy*` and `xianxu/brain-backup` cleanup is now unblocked (this milestone gated it). Original wall-clock deadlines stand: local ~2026-05-13, cloud ~2026-06-06.

<a id="nous-5-m1"></a>
### nous#5 M1 — /nous-resolve v1: whole-file AI-prose merge (load-bearing)

**est:** 7 (whole-issue P50; M1 portion ~1–2.5 hr per the issue's decomposition)
**actual:** 0.5h
**closed:** 2026-05-08

Skill landed at `nous/nous/skills/nous-resolve/` (vendored to `.claude/skills/nous-resolve` via `nous/nous/nous.manifest`). Slash command `/nous-resolve <brain-root>` — one brain at a time. SKILL.md (~200 lines) carries the 7-step procedure + the per-element merge taxonomy (scalar/enum/list/key-anchored-list/table/prose). Mechanical helpers: `preserve.py` (~80 lines, snapshot to `.brain/merges/`), `find-conflicts.sh` (~30 lines, discover pairs).

**Naming change from issue spec**: `/nous-resolve` (not `/brain-resolve`). Reasoning surfaced during design review: nous owns brain-building tooling, ariadne is the AI coding substrate. Brain-resolution belongs in nous's namespace alongside `nous-tools` and `charon`. Issue body documents the rationale.

**Design choice — structural awareness via prompt + prototype-as-implicit-schema**: SKILL.md tells the agent how to classify each element and apply default merge rules per type, with prototype semantics overriding heuristics. *No declarative merge engine* in M1. Travel-plan's prototype reads naturally as a per-section schema; the agent reasons from it. M4's declarative `merge:` tags would formalize this if M3 dogfood reveals consistent failures the prompt-guided merger can't be coaxed out of.

**Verified mechanical layer**: `test-synthetic.sh` (~120 lines) builds a synthetic brain with a travel-plan conflict pair, runs the full chain (find → preserve → write → cleanup → commit), asserts each artifact. Green. Agent-driven semantic correctness is M3 territory — the M1 claim is "skill is ready to be exercised on real conflicts."

**Atlas**: `nous/atlas/nous/brain-conflict-resolution.md` documents the surface, taxonomy, and M4 escalation path.

**Actual 0.5h vs est ~1–2.5h**: way under. v3 commit-anchored attribution captures only the post-status-flip focused build window (33.6m); the upstream design conversation about skill-vs-script split, structural awareness, and naming happened during segments anchored to other commits (BRAIN_DIR fix, vendor sync). Procedure-as-truth says report what the script computes; lesson for future: commit during design discussion if you want it attributed.

<a id="nous-5-m2"></a>
### nous#5 M2 — undo path

**est:** 0.4–1h (from issue's M2 estimate decomposition)
**actual:** 1.0h
**closed:** 2026-05-08

`/nous-resolve <brain-root> undo` lands. Implementation is `git revert <merge-sha> --no-edit` against the most recent commit matching `^merge: .* via /nous-resolve$`. One operation restores canonical, restores conflict file, removes snapshot files. **No new helper script** — git revert IS the undo. The merge commit's diff inverse is exactly what undo needs.

**Bonus**: revert commit produces an audit trail in git log; brain-sync's ref-watcher pushes it.

**Trail in SKILL.md `## Trail` section**: documents `git log --grep '^merge: .* via /nous-resolve$'` for finding past merges, manual `rm -rf .brain/merges/<old>/` for pruning, targeted older-revert via explicit SHA. No automated prune yet.

**Test extension**: `test-synthetic.sh` extended to run undo and assert canonical-restored + conflict-file-restored + snapshot-files-removed + revert-commit-landed. Fixture order had to be reshuffled so `git init` runs before `preserve.py` — otherwise the snapshot was committed in the initial fixture and revert wouldn't touch it.

**Side-quest caught**: `set -o pipefail` interaction in test-synthetic.sh where `git log | head -2 | tail -1` killed the script via SIGPIPE silently. Removed the unused variable; replaced `find | wc | tr` with explicit per-file existence checks for the snapshot-files-gone assertion. Both pipefail-safe now.

**Actual 1.0h vs est 0.4–1h**: at the upper end of the range. Most of the time was the SKILL.md procedure write-up + test extension + the pipefail debugging side-quest. The git-revert primitive itself is one line of skill prose; the work was around it.

<a id="nous-14-m1"></a>
### nous#14 M1 — flat-copy charon → nous, rewrite imports, both binaries build

**est:** 1.5–3h (M1 portion of #14's 12–22h whole-issue range)
**actual:** 2.5h
**closed:** 2026-05-09

charon's substantive code absorbed into nous via flat copy (rejected the initial subtree-merge plan once it became clear we'd want to mass-move pieces anyway; archived charon repo preserves git history independently). Layout:

- `cmd/charon/` (proxy + auth + instructions + manifest binary)
- `cmd/nous-security/` (was charon-security; renamed in a follow-on commit since "security" captures purpose, "charon-" was vestigial)
- `internal/charon/` (oauth, providers, proxy, runtime, security, service, tui, vault — 8 subpackages)
- `atlas/charon/` (charon.md, security-audit.md, fresh index.md noting M2's planned reorganization)

**70 import paths rewritten** via `sed`: `github.com/xianxu/charon/internal/...` → `github.com/xianxu/nous/internal/charon/...`. 0 charon-prefix imports remain after rewrite.

**Verification beyond compile**: `go build ./...` + `go test ./...` green across all packages. Binary smoke-tested — `charon manifest` reads operator's actual vault state correctly (Google + Anthropic accounts surfacing); `charon instructions`/`scopes`/`--help` all functional. `nous-security` builds clean post-rename. brain-sync tests still green.

**Skipped from charon** (charon-archive keeps them): `AGENTS.md`, `CLAUDE.md`, `Makefile.workflow`, `workshop/`, `.openshell/`, `construct/`, `bin/`, `docs/`, `LICENSE`, `README.md`, `scripts/`, `test/`, `atlas/{workflow/, index.md}` — vendored ariadne-base or charon-internal.

**Actual 2.5h vs est 1.5–3h**: middle of range. Most time was design conversation (subtree-vs-flat-copy decision, audience-tag refinement, marker resolution rounds) reflected in segment 6's 61m anchored to #14 via the issue-sync commit. Mechanical execution (copy + sed + build/test + rename) was ~10m wall-clock once design was settled.

**Source charon SHA**: `d85363d` (charon's HEAD at copy time). Provenance after archive: `cd ~/workspace/charon-archive && git log d85363d`.

<a id="nous-14-m2"></a>
### nous#14 M2 — extract domain-organized libs from internal/charon

**est:** 1.5–3h
**actual:** 0.2h
**closed:** 2026-05-09

`internal/charon/` disassembled into `lib/` per the lib-first design principle. `internal/` directory removed entirely.

Mapping:
- `internal/charon/tui/` → `lib/tui/`
- `internal/charon/service/` → `lib/service/`
- `internal/charon/security/` → `lib/security/`
- `internal/charon/{oauth, providers, proxy, runtime, vault}/` → `lib/provider/{oauth, providers, proxy, runtime, vault}/`

The whole credential-and-proxy domain (`lib/provider/`) lands under one roof so a future repackage (e.g. charon-only side-binary) can grab it as a unit. `lib/security/` stays sibling — host-security audits are orthogonal to credentials and brains. `lib/tui/` and `lib/service/` are leaf libs available to all consumers.

**Deferred** (net-new code, not relocations — M3-M4 scope):
- `lib/agent/` (gpg-agent ops; charon#21 absorption)
- `lib/identity/` (keypair gen/export/import)
- `lib/brain/` (provisioning/recipient/resolve; sync stays in `lib/brainsync/` for now since renaming would cascade through `cmd/brain-sync/`)

**Verification**: 71 import paths rewritten cleanly via sed. `go build ./...` + `go test ./...` green across all relocated packages. Cross-import rule verified: `lib/provider ⊥ lib/brainsync`, common ground in `lib/{tui, service}`. Atlas captured at `nous/atlas/nous/lib-layout.md`.

**Actual 0.2h vs est 1.5–3h**: dramatically under. Most of the design work (deciding the mapping, lib-first principle) was anchored in earlier `#14` segments; M2's mechanical execution (git mv + sed import-rewrite + tests) was ~10 min. v3's commit-anchored attribution captures the post-design execution window honestly. Calibration insight: when a multi-milestone issue has heavy design conversation early, downstream "execution-only" milestones can land far below estimate without that being a bug — it's the design having paid off.

<a id="nous-14-m3"></a>
### nous#14 M3 — `nous` cobra root + subcommand restructuring

**est:** 3–5h
**actual:** 0.5h
**closed:** 2026-05-09

Shipped across four sub-commits:

- **M3a** (`05211d1`): refactored `cmd/charon` cobra constructors into a new `lib/charoncli/` package. Both `cmd/charon` (legacy entry, slim shim) and the new `cmd/nous` import the same constructors — single source of truth for the provider/auth/instructions/manifest subcommands.
- **M3b** (`fb47554`): `cmd/nous/main.go` (~140 lines) with cobra root and four cluster subcommands. `nous instructions` and `nous manifest` mount `charoncli.InstructionsCmd`/`ManifestCmd`. `nous provider` mounts `charoncli.AuthCmd` with `Use="provider"` (bare cluster command IS the TUI entry, per the spec). `nous identity` and `nous brain` are M4 placeholders that error with helpful "see legacy X" messages.
- **M3c** (`18fdd1e`): real `nous service install/uninstall/start/stop/status` (`cmd/nous/service.go`, ~230 lines). Each subcommand dispatches to BOTH `lib/service` (charon's launchd manager) and `lib/brainsync` (brain-sync's), aggregating output. Sibling-binary discovery resolves `bin/charon` and `bin/brain-sync` paths next to nous.
- **M3d** (`5242e51`): `lib/agent/` foundation — `Identity` + `Keygrip` types and `DiscoverIdentity()` parsing `gpg --with-keygrip --with-colons --list-keys` output. Live-tested against operator's actual keyring (returned correct fingerprint + UID + 2 keygrips for primary key + encryption subkey). Charon#21 M1 absorbed; M2-M3 (prewarm/flush/status verbs) land in M4.

**Deferred from M3's plan** (called out in the ticked items):
- Single-binary daemon mode (`nous serve` running both runtimes in goroutines) — phase-2 work, not gated on this issue's done-when.
- Backwards-compat deprecation shims for `cmd/charon` and `cmd/brain-sync` — both still build and work; removing is its own milestone after operator migration.
- Makefile.nous build-target unification — cosmetic; `go build -o bin/nous ./cmd/nous` works.

**Verification**: `go build ./...` + `go test ./...` green throughout (charon's full test suite passes from new locations, plus new `lib/agent` unit tests). `nous --help` lists all clusters; `nous instructions`/`manifest`/`provider list` return correct output against operator's real vault state. `nous service status` correctly queries both subsystems' launchd state.

**Actual 0.5h vs est 3–5h**: dramatically under. Same calibration insight as M2 — heavy design conversation lived in earlier #14 segments (the design rounds, marker resolutions, audience-tag refinement, lib-first principle). M3's mechanical execution after the design landed: refactor cmd/charon → lib/charoncli (sed-based), create cmd/nous with cobra wiring, write service.go + agent.go from clean slate. Each chunk was small + bounded once the structure was clear. Pattern emerging across the v3 calibration: "design-heavy issues with execution-only milestones can land 5-10x under estimate."

<a id="nous-14-m4"></a>
### nous#14 M4 — net-new commands: identity cluster, brain recipient w/ safeguards, brain new guided, obs status/doctor

**est:** 3–5h
**actual:** 1.5h
**closed:** 2026-05-09

Shipped across four sub-commits (M4a → M4c → M4b — operator's chosen ordering, "M4c first because it's isolated on this machine; M4b last because multi-recipient e2e needs wife's pubkey"):

- **M4a** (`73d61cc` + workspace fix `e0a5dce`): `lib/identity/` (List, ListPublic, Export, Inspect, Import, Last8) and `lib/brain/` reader (Manifest, Read, DiscoverAll). `nous identity {init, export, import, list, agent}` cluster — `init` shells out to `scripts/identity.sh` (200 lines of pinentry-mac/keychain config not worth re-porting yet); `import` is TTY-only with verify-fingerprint ceremony. Workspace fix extracts `lib/workspace.Root()` so brain discovery follows nous's location instead of hardcoding `~/workspace`.

- **M4c** (`8e8571a` + ariadne `fc76e1e` + brain `3813f17`): `nous service doctor` (9 prescriptive checks, each with a specific fix command) + `nous service audit` (tail/grep over `~/Library/Logs/{charon,brain-sync}.log`). Schema cleanup: `mode:` field dropped from `.brain/config.md` (ariadne AGENTS.md updated, lib/brain.Manifest gains `Shared()` derived from `len(Recipients) > 1`, lib/brainsync switches to derived signal, `LegacyMode` preserved on read for round-trip). Threat-model `## Revisions` updated.

- **M4b** (`dc1b358`): `lib/brain/` write side (WriteManifest, RewriteFrontmatter, SetGcryptParticipants, ReadGcryptParticipants — all atomic via .tmp+rename, sorted recipients, no `mode:`). `nous brain {new, list, recipient list/add/remove, resolve}` cluster — multi-recipient provisioning via two-push pattern (script does single-recipient bootstrap → cmd/nous re-keys to full set in a second commit/push so gcrypt re-encrypts). Three safeguards on remove (last-recipient guard, self-removal `--force` gate, revocation-caveat warning). All admit-and-revoke verbs are TTY-only.

- **Review fixes** (`bf9c0ff`): milestone code-review (BASE=00ee027, HEAD=dc1b358 dispatched via superpowers-code-reviewer subagent) flagged 3 Important + 2 Minor. Important: (1) `WriteManifest` clobbered operator-authored body on every recipient change → split into `RewriteFrontmatter` (frontmatter-only) for the recipient-mutation path; (2) push-failure recovery was advertised but unwired → re-runs now detect unpushed commits and retry the push; (3) multi-key armor admitted silently past the verify-fingerprint ceremony → `Inspect` now refuses, `Import` does before/after diffs as defense-in-depth. Minor: lookupKey suffix-match length floor; gpg --export `--` separator. Lessons captured in workshop/lessons.md.

**Decisions worth preserving:**

- **Bash-script deletion deferred.** M4 plan called for "deletes bash scripts" alongside the new Go cluster; we kept `scripts/{identity,new-brain,cloneto}.sh` because they encode 200+ lines each of substrate validation + macOS quirks (pinentry-mac, gpg-agent config, gh repo creation) that aren't worth re-porting until the cluster's surface stabilizes. `nous identity init` and `nous brain new` shell out to them with the new commands handling the multi-recipient ceremony + manifest authoring on top. Full Go port is a follow-up.
- **Two-push pattern for multi-recipient brain new.** Rather than patch `scripts/new-brain.sh` to take a multi-recipient flag, `nous brain new` lets the script do its single-recipient bootstrap unchanged, then re-keys via `lib/brain` + a second `git commit` + `git push`. gcrypt fully replaces the remote object store on each push, so the second push supersedes the first — no leakage of single-recipient blobs. Keeps the script stable and untouched while adding the multi-recipient capability above it.
- **`mode:` field dropped, not deprecated.** Existing manifests with `mode:` parse fine (preserved as `LegacyMode` on read). Writers stop emitting it. The shared-vs-private discriminator becomes a single derived signal (`len(Recipients) > 1`) — removes the duplication invariant that tools had to keep in sync. Threat-model revision documents the change.
- **TTY-only delegation boundary made explicit.** Identity import + brain recipient add/remove all `RequireTTY` via `term.IsTerminal(int(os.Stdin.Fd()))`. The verify-fingerprint ceremony (last-8 hex, OUT-OF-BAND verification, 3-attempt cap, case-insensitive) catches the class of attack where an attacker substitutes their own pubkey before import. Threat model anchored to commit SHAs.
- **e2e for multi-recipient deferred.** `nous brain new --recipient $WIFE_FP` requires wife's pubkey already imported via the verify-fingerprint ceremony, which requires her machine to run `nous identity init && nous identity export` first. Operator opted to ship M4 code-complete and verify the multi-recipient path during the actual brain-shared-family provisioning (nous#12 M1).

**Verification**: 9-check `nous service doctor` all green against operator's real state; `nous identity list` shows joined keyring×brains; `nous brain list` enumerates 2 brains correctly; `nous brain recipient list ~/workspace/brain` shows operator's key annotated `(self)`; TTY-only refusal verified for each TTY-gated verb. Tests added: `TestRewriteFrontmatter_PreservesBody`, `TestRewriteFrontmatter_RefusesMissingFrontmatter`, `TestInspect_RefusesMultiKeyArmor`. Full `go test ./...` green.

**Actual 1.5h vs est 3–5h**: under but reasonable. Lib-first plus the two-push trick made `brain new` simpler than expected (no need to patch the bash script). Identity cluster `init` deferred its full port (saves ~1h). Code review surfaced findings worth fixing — about an hour absorbed there but caught real issues (silent body clobber would have been operator-confusion-bait; multi-key admit was a threat-model breach).

<a id="nous-14-m5"></a>
### nous#14 M5 — TUI shell (brain + provider) + agent-vs-human atlas

**est:** 3–5h
**actual:** 3.5h
**closed:** 2026-05-10

Shipped across three sub-commits + one polish + one review-fix pass:

- **M5a** (`f472fef`): bare `nous brain` launches a bubbletea program on TTY (falls through to cobra help on non-TTY so agents don't get bubbletea escape codes in their transcript). Browse brains under the workspace root → drill into one to see Recipients (joined manifest × gcrypt-participants with mismatch warning), Sync (last commit + ahead/behind upstream), Conflicts (count + per-file preview with `<<<<<<<`/`=======`/`>>>>>>>` marker highlighting). New `lib/brain/status.go` aggregator `LoadStatus`; new `lib/brain/annotate.go` extracted from cmd/nous so CLI and TUI share the (self)/(peer)/(unknown) annotator. New `lib/tui/brain/` package (sibling of charon's `lib/tui/`, not mixed in) with list/detail/conflict_preview models + a root screen-stack.
- **M5b** (`dee4c0d`) + polish (`1e36389`): in-TUI recipient add/remove. Add flow: 4-stage bubbletea ceremony (textinput pubkey path → identity.Inspect → fp/uid/last-8 render → textinput last-8 verify, 3 attempts, case-insensitive → async apply: import + RewriteFrontmatter + SetGcryptParticipants + AddCommitPush → done). Remove flow: picker → last-recipient hard refuse → optional REMOVE-SELF typed phrase (only when removing would leave no decrypt path) → revocation caveat enter-to-confirm (polish simplified this from a typed phrase since the caveat is informational, not a trust event) → async apply. Pure-Go helpers extracted to `lib/brain/recipient.go` (MatchRecipient, CanRemoveRecipient, WithoutRecipient, ContainsRecipient, LocalSecretFingerprints, WouldLockOut). **Side-quest triggered by live-test**: throwaway test key got mislabeled `(self)`. Built primary-identity concept: `lib/identity/primary.go` (state file at $UserConfigDir/nous/primary-identity, Primary/SetPrimary/IsPrimary/ErrPrimaryUnset+ErrPrimaryStale, resolution stored → only-one-secret → unset); `nous identity primary [FP]` subcommand (heuristic resolver, interactive persist confirm, machine-stable non-TTY output); annotator rewritten with (self)/(local secret)/(peer)/(unknown) tiers and brain-aware heuristic fallback (private brain → sole recipient → operator's primary). Self-removal safeguard reworked from "any local-secret key" to `WouldLockOut` (would removing this leave no decrypt path). Same-pass UI fix: TUI + CLI list both display directory basename instead of manifest.Name.
- **M5c** (`ce67eeb`): provider TUI audit. appName() → "nous provider"; bulk "Charon will X" → "nous will X" across admin-key paste/revoke, catalog revoke, picker, scopes, gcp setup; "via charon auth" → "via `nous provider`"; GCP-setup-unwired hint stops pointing at nonexistent `charon gcp setup` command. Internal identifiers (lib/charoncli, X-Charon-Account header, ~/Library/Logs/charon.log, Go types) intentionally left alone. providerCmd.Long gets an explicit audience-tag block. **atlas/nous/cli.md** lands: cluster map, audience-tag scheme (a)/(h)/(b), per-cluster-TUI rationale.
- **Review fixes** (in the M5 close commit): 6 Important findings, no Critical. Atomic + 0o600 SetPrimary write (was bare os.WriteFile w/ 0o644). confirmPersist hardened: explicit y/yes only (was default-yes, EOF would silently persist). WouldLockOut returns (true, err) on gpg outage so a caller that forgets `err` errs safe. Machine-stable single-line output for `nous identity primary` on non-TTY (atlas tags it `(b)` so agents must be able to parse). Picker tier marker — `[⚠ would lock you out]` rendered per row up front so the safeguard is visible before the operator presses enter. DRY'd the brain-aware heuristic into `lib/brain.HeuristicPrimary` (was duplicated in annotator + cmd).

**Decisions worth preserving:**

- **Lib-first carries through to TUI domain code.** `lib/tui/brain/` is a sibling of `lib/tui/` (charon-provider), not a child. Mixing domains in one tui package would force shared state we don't actually share. Each domain's models import their own lib helpers + small lipgloss style files duplicated locally. Cheap duplication beats incorrect coupling.
- **Native bubbletea ceremony rather than tea.ExecProcess shell-out.** Operator chose ambitious path during M5 brainstorm. Worth it: the verify-fingerprint screens render inline with the rest of the TUI, no flash-of-CLI, and the underlying `identity.Inspect`/`identity.Import`/`brain.RewriteFrontmatter`/`brainsync.AddCommitPush` calls are the same primitives the CLI uses — security logic single-sourced in lib.
- **Cosmetic annotation tier and functional safeguard tier are separate concerns.** (self) is a UI label; WouldLockOut is the safety floor. M5b's first draft conflated them ("if annotation starts with (self) then run REMOVE-SELF"), got caught during live-test. Lesson #5 in workshop/lessons.md.
- **Primary identity as an explicit nous concept**, not gpg's `default-key`. State lives at $UserConfigDir/nous/primary-identity; not synced (machine-specific per gpg keyring); not encrypted (a fingerprint is public). Brain-aware heuristic (private brain → sole recipient ∈ local secrets) supplies a strong default; explicit `nous identity primary <FP>` overrides.
- **Machine-stable single-line shape for (a)/(b) subcommands on non-TTY.** Verbose human prose is TTY-only bonus; the agent contract is a single canonical line. Locked in via `atlas/nous/cli.md` audience-tag scheme.

**Verification**: go build ./... + go test ./... green throughout; 9 new lib/brain tests (status aggregator + recipient helpers), 7 new lib/identity/primary tests (against tempdir-redirected $XDG_CONFIG_HOME + isolated GNUPGHOME). Operator live-tested M5a (TUI launches, list + drill-in match `nous brain list` + `nous brain recipient list` cross-check) and M5b (paste throwaway pubkey, complete ceremony, observe re-key + push; remove with safeguards; verify gcrypt re-encrypts). M5c smoke: provider TUI renders "nous provider" titles correctly; `nous brain | cat` (non-TTY) → help; `nous identity primary` on multi-key keyring → heuristic resolves brain recipient correctly.

<a id="wave-2"></a>
### wave 2 — shared-brain product surface (folded in at close)

The MVP (the eight `mvp_scope` issues) shipped a working two-person gcrypt brain. During build the scope widened into a general shared-brain product surface; these issues landed after the project file last froze (2026-05-08) and are folded in here at close. They belong to targets `shared-brain-infrastructure-and-ui` and `collaborator-state-machine`.

| issue | what | est | actual | closed | state |
|---|---|---|---|---|---|
| nous#16 | unified `nous serve` foreground daemon + dev/prod workflow | 6 | 2 | 2026-06-02 | done — M1–M5 landed; M4 daemon-codesign walked back by design (dev-only); operator launchd live-verify pending |
| nous#20 | retire standalone charon + brain-sync binaries | 2 | 1.5 | 2026-06-02 | done — binaries deleted, 9 verbs on `nous`; M4 operator install-run pending |
| nous#25 | new-brain REST endpoint (fresh-account GraphQL lag) | 0.5 | 0.3 | 2026-05-26 | done |
| nous#26 | GitHub-mediated recipient onboarding (invite/join/auto-admit) | 6 | 7 | 2026-05-31 | done |
| nous#27 | three onboarding polishes | 1.5 | 1.2 | 2026-05-31 | done |
| nous#30 | brainsync autosave + `nous push` checkpoint | 8 | 2 | 2026-06-02 | done — M1–M4, 22 unit tests green; live-daemon VM e2e pending |
| nous#31 | TUI list async load + cache | 3 | 3 | 2026-06-02 | done — 25 tests green, ESC 2.88s→0.11s |
| nous#32 | leave a shared brain (`nous brain leave` + TUI `l`) | 4 | 2 | 2026-06-02 | done — M1–M4 shipped; leave-completeness vs invariant #2 (keys-branch strip) handed to nous#41 #12; owner-refuse/happy-path e2e operator-gated |
| nous#33 | local-only private brain (no GitHub backing) | — | — | 2026-05-31 | done |
| nous#34 | brain-poll negative cache via `git ls-remote` | 3 | 1 | 2026-05-26 | done |
| nous#36 | headless-VM brain e2e + non-interactive identity | 4 | 5 | 2026-06-01 | done |
| nous#38 | recipient remove clears all per-brain revoke state | 2 | 2 | 2026-06-01 | done |
| nous#39 | brain invite re-sends on a stale/expired invitation | 1 | 1 | 2026-06-01 | done |
| nous#40 | unified per-brain person removal at any stage | 3 | 3 | 2026-06-01 | done |

**Decisions worth preserving:**
- **Onboarding moved off sneakernet for GitHub-backed brains.** `nous#26` added the `keys`-branch convention: invitee `nous brain join` publishes `<login>.asc`; operator's brain-sync `ImportAllPubkeys` + `AutoAdmitFromKeysBranch` auto-import/admit. Sneakernet (`nous identity export` → `import --verified-last8` → `recipient add`) remains the GitHub-free / explicit-verify path used by the file:// e2e. Don't mix the two.
- **Local key deletion is undone by the remote** — a recipient fp lives in manifest + keys branch + `verified.yaml`; `gpg --delete-keys` gets re-imported on next sync. True removal needs system-wide revocation → `nous#37` (follow-on).
- **`collaborator-state-machine` target** carved out to defend the per-brain membership lifecycle (invite → join → admitted → removed/left) from drift; `nous#41` tracks 12 codex-review hardening findings against it.

[wave 2]: #wave-2
[nous#8 M1]: #nous-8-m1
[nous#8 M2]: #nous-8-m2
[ariadne#22 M1]: #ariadne-22-m1
[ariadne#22 M2]: #ariadne-22-m2
[nous#3 M1]: #nous-3-m1
[nous#3 M2]: #nous-3-m2
[nous#3 M3 steps 3b–3d]: #nous-3-m3-cutover
[nous#3 close]: #nous-3-close
[nous#10]: #nous-10
[nous#5 M1]: #nous-5-m1
[nous#5 M2]: #nous-5-m2
[nous#14 M1]: #nous-14-m1
[nous#14 M2]: #nous-14-m2
[nous#14 M3]: #nous-14-m3
[nous#14 M4]: #nous-14-m4
[nous#14 M5]: #nous-14-m5
