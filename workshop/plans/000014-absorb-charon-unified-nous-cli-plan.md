# nous#14 M5 — TUIs (brain + provider) + agent-vs-human docs

> **For agentic workers:** Most of this milestone is session-warm — the bubbletea conventions, audience-tag scheme, and lib/brain + lib/brainsync surfaces are all fresh in this session's context. Main session is the right venue. Dispatch a code-review subagent at M5 close per AGENTS.md §3.

**Goal:** Land `nous brain` as a bubbletea TUI (list → drill-in → actions), polish `nous provider` (already a TUI via charoncli.AuthCmd), and capture the agent-vs-human help-split + audience-tag scheme in `atlas/nous/cli.md`.

**Spec source:**
- `nous/workshop/issues/000014-absorb-charon-unified-nous-cli.md` (M5 checklist, lines 310–318)
- `nous/lib/tui/` (charon bubbletea patterns: model.go, picker.go, styles.go, tui.go)
- `nous/lib/brain/` (manifest read/write, gcrypt participants, Shared() derivation)
- `nous/lib/brainsync/` (FindSharedBrains, conflict-file discovery)

**User-approved design choices (2026-05-10):**
1. **Ceremony rendering:** port verify-fingerprint ceremony into bubbletea natively (textinput model). Not shell-out.
2. **Drill-in detail:** richer — recipients, sync state, conflict count, **per-file conflict preview** (markers + diff snippets).
3. **Provider TUI scope:** audit + polish — add audience tags inline, fix post-absorption oddities (stale `charon` references), no rebuild.

---

## Architecture

### Brain TUI shape

```
nous brain                              (h) — bare cluster command launches TUI
  ┌─ list view ─────────────────────────┐
  │ ▸ brain               (shared, 2)   │  ← cursor here
  │   brain-shared-family (shared, 2)   │
  │   personal            (private, 1)  │
  │                                     │
  │ [enter] drill in   [q] quit         │
  └─────────────────────────────────────┘

  enter →

  ┌─ drill-in: brain ───────────────────┐
  │ path: ~/workspace/brain             │
  │ shared (2 recipients, syncthing)    │
  │                                     │
  │ Recipients ───────────────────────  │
  │   3872c2f0  Xian Xu (self)          │
  │   a1b2c3d4  Wife (peer)             │
  │                                     │
  │ Sync ─────────────────────────────  │
  │   last commit: 2h ago (4e5f912)     │
  │   ahead 0, behind 0                 │
  │                                     │
  │ Conflicts ────────────────────────  │
  │   (none)         ← or N files       │
  │                                     │
  │ [a] add recipient  [r] remove       │
  │ [c] preview conflicts  [esc] back   │
  └─────────────────────────────────────┘
```

Each action keystroke either:
- Pushes a sub-model (e.g. `a` → recipient-add ceremony model with textinput stages: paste pubkey → confirm 8 hex chars → commit/push spinner)
- Renders a modal (e.g. `c` → conflict preview pager)
- Returns the user to drill-in after completion with a status banner ("recipient added: a1b2c3d4")

### Provider TUI shape

Already mounted in `cmd/nous/main.go:providerCmd()` via `charoncli.AuthCmd`. Scope is audit-and-polish:
- Walk all screens against real Anthropic provider config
- Replace any user-visible `charon`/`charon auth` references with `nous`/`nous provider`
- Add audience tags `(h)` / `(a)` / `(b)` to inline help where missing
- Document the screens in `atlas/nous/cli.md`

No structural rewrite. No new model code.

### File structure

```
cmd/nous/
  brain.go              # MODIFY: brain root cmd gets a Run that launches TUI when no args
  brain_tui.go          # NEW: top-level bubbletea program glue (tea.NewProgram, root model)

lib/brain/
  status.go             # NEW: aggregator — Status(path) returns {Path, Manifest, Recipients,
                        #      LastCommit, Ahead, Behind, ConflictFiles, SyncSubstrate}
  status_test.go        # NEW: table tests against fixture brain dirs

lib/tui/brain/           # NEW package — keep separate from lib/tui (which is charon's
                        # provider TUI). Domain-scoped TUI code lives next to its domain.
  list.go               # list model (browses lib/brain.DiscoverAll output)
  detail.go             # drill-in model (renders lib/brain.Status output)
  recipient_add.go      # add-recipient sub-model: paste pubkey → verify-8-hex → commit
  recipient_remove.go   # remove-recipient sub-model: pick recipient → safeguards → commit
  conflict_preview.go   # pager model over conflict files
  styles.go             # lipgloss styles shared across brain TUI screens
  *_test.go             # bubbletea model tests via teatest where feasible

atlas/nous/
  cli.md                # NEW: agent-vs-human split + audience tags + per-cluster-TUI choice
```

**Why `lib/tui/brain/` not `lib/tui/`:** charon's existing `lib/tui/` is provider-domain — `provider_picker.go`, `admin_*.go`, `catalog_*.go`, `scopes.go`. Mixing brain models in would conflate domains. Mirror it as a sibling. `lib/tui/styles.go` patterns get re-used via small helper imports, not by sharing state.

---

## Sub-milestones

### M5a — brain TUI scaffolding + read-only views (~1.5–2h)

Goal: bare `nous brain` launches a working list → drill-in TUI. All read-only.

- [ ] `lib/brain/status.go`: aggregator. Calls `brain.Read` for manifest, `ReadGcryptParticipants` for actual gcrypt keys, shells `git -C <path> log -1 --format="%h %ar"` for last commit, `git -C <path> rev-list --count @{u}..HEAD` and reverse for ahead/behind (handle no-upstream gracefully), `brainsync.FindConflictFiles` for conflict list. Tests against synthetic git repos in tmpdir.
- [ ] `lib/tui/brain/list.go`: bubbletea model. Loads `brain.DiscoverAll()` on init. Cursor + enter → emit `DrillInMsg{path}`. Styling pulls from lipgloss shared via `lib/tui/styles.go`.
- [ ] `lib/tui/brain/detail.go`: drill-in model. On init, kicks off async `brain.Status(path)` via `tea.Cmd` (so the UI shows "loading..." not block). Renders three sections: Recipients, Sync, Conflicts. Recipient annotations: `(self)`, `(peer)`, `(unknown)` — reuse existing logic from `cmd/nous/brain_recipient.go`.
- [ ] `lib/tui/brain/conflict_preview.go`: when conflict count > 0, `c` opens a pager listing conflict files with first 20 lines of each (highlight `<<<<<<<`/`=======`/`>>>>>>>` markers via lipgloss).
- [ ] `cmd/nous/brain_tui.go`: tea.NewProgram glue. Root model owns a state-stack so `enter` pushes detail, `esc` pops to list.
- [ ] `cmd/nous/brain.go`: override `Run` on the brain root cmd to launch TUI when invoked with no args (and stdout is a TTY — otherwise print help, preserves agent-facing help discovery).
- [ ] Live-test: `nous brain` from operator's machine. Walk list → drill-in for `brain` and `personal`. Verify recipient annotations, sync info, conflict count all match what `nous brain list` + `nous brain recipient list` show.

**M5a close criteria:** read-only TUI works against real brains; no actions wired yet.

### M5b — brain TUI actions (~2–3h)

Goal: `a` (add recipient) and `r` (remove recipient) work end-to-end inside the TUI, with the verify-fingerprint ceremony rendered natively.

- [ ] `lib/tui/brain/recipient_add.go`: multi-stage model.
  - Stage 1: textinput for pubkey path or paste-buffer. Validate via `lib/identity.Inspect` (parse armored key, return fingerprint).
  - Stage 2: render the imported fingerprint with last-8-hex highlighted. textinput prompt: "type the last 8 hex chars: ___". Compare case-insensitively against `lib/identity.Last8(fp)`. Mismatch → error banner, retry.
  - Stage 3: spinner while `lib/brain.WriteManifest` + `SetGcryptParticipants` + `git commit` + `git push` run. Stream stderr into a scrollable pane on error.
  - Success → pop back to detail view with banner "added <fp8>".
- [ ] `lib/tui/brain/recipient_remove.go`: stage model.
  - Stage 1: picker over current recipients. Cursor + enter selects target.
  - Stage 2: safeguard prompts inline. Last-recipient-guard → hard refuse banner, esc back. Self-removal → warn banner + "type REMOVE-SELF to confirm" textinput. Revocation-caveat → "type REVOKED-OUT-OF-BAND to confirm". Match the cobra flow exactly.
  - Stage 3: spinner over write + commit + push, same as add.
- [ ] **TTY-only enforcement preserved**: the TUI itself only launches in a TTY (tea.NewProgram aborts otherwise), so the boundary is naturally upheld. But add an explicit `isatty` check on `nous brain` (no-args) that returns help instead of launching, so agents don't get a half-rendered TUI in their transcript.
- [ ] Tests: `teatest`-style for each sub-model (golden frames at each stage). Ceremony mismatch test is critical — must refuse with the exact same logic as `cmd/nous/brain_recipient.go:promptVerify`.
- [ ] **Live-test on real machine**: paste own pubkey, complete ceremony, observe re-key + push. Then remove it (via `--force` path through TUI). Verify gcrypt re-encrypts correctly. Run `nous brain recipient list` from CLI afterwards to confirm state matches.

**M5b close criteria:** add + remove flows work end-to-end against a real brain; all four safeguards fire; ceremony rejects wrong-hex input.

### M5c — provider TUI polish + atlas doc (~0.5–1h)

Goal: provider TUI screens audited, atlas doc captures the agent-vs-human design.

- [ ] Smoke-test `nous provider` against operator's Anthropic provider config. Click every screen visible from the entrypoint. Note any user-visible `charon` references or broken flows.
- [ ] Patch user-visible strings: `charon auth` → `nous provider`, `charon` → `nous` where appropriate. Don't rewrite internal package names; just user-facing copy.
- [ ] Add audience tags `(h)` / `(a)` / `(b)` to inline help text in `cmd/nous/main.go:providerCmd` Long description and any sub-cobra commands.
- [ ] `atlas/nous/cli.md` (new):
  - Cluster shape: identity / brain / provider / service + top-level instructions/manifest/menubar
  - Audience tag scheme `(h)` / `(a)` / `(b)`, with the design rationale (subcommand `--help` = agent manual; TUI = human UX)
  - Per-cluster-TUI choice: why only brain + provider have TUIs (humans browse-and-act there; identity ops are prompts; service ops are scriptable)
  - Pointer to issue 000014 for full history; pointer to threat-model `## Revisions` for TTY-only delegation boundary
- [ ] Update `atlas/index.md` with a one-line entry for `nous/cli.md`.

**M5c close criteria:** atlas doc lands; provider TUI's user-visible copy mentions `nous`, not `charon`; audience tags on entrypoints.

---

## Manual test plan (M5 milestone close)

Run after M5a + M5b + M5c. Single sitting against operator's real brains and real Anthropic provider.

1. `nous brain` → list shows all brains under workspace root. Cursor navigation works.
2. Drill into `brain` (private). Verify: 1 recipient (self), sync section, 0 conflicts.
3. Drill into `brain` and add a throwaway test pubkey (any second key you have lying around, or generate one). Complete ceremony. Verify re-keyed manifest pushed.
4. Drill into the same brain, `r` to remove the throwaway key. Trigger safeguards. Confirm removal pushed.
5. Synthesize a conflict (touch two divergent commits in a fixture brain), `c` to preview. Verify marker highlighting renders.
6. `nous provider` → list providers → drill into Anthropic → walk OAuth / token-rotation flow read-only (don't actually rotate). Verify no `charon` strings.
7. `nous brain | cat` (non-TTY) → prints help, doesn't launch TUI. Confirms agent-discovery surface preserved.
8. `nous brain --help` → still shows cobra help with subcommand list. Audience tags present.

Log to issue 000014's `## Log` section as one entry per sub-milestone close, then a combined M5 entry at the end.

---

## Out of scope

- `nous brain resolve` → still a stub (deferred to nous#5 follow-up). Conflict preview in TUI is **read-only**; running the merge stays in `/nous-resolve` skill.
- TUI automation tests beyond model golden frames. End-to-end bubbletea automation is its own rabbit hole.
- Provider TUI structural rewrite. Stays atop charoncli.AuthCmd.
- Identity TUI. Identity ops are sequenced CLI prompts (design decision in issue 000014, point 7).
- Service TUI. Service ops are pure CLI (design decision in issue 000014, point 7).

## Open questions

- **Provider TUI's `nous provider list` cobra subcommand**: keep as `(a)` machine-readable view (JSON-shaped). Worth verifying that the bare TUI launch and the `list` subcommand share state cleanly — if they diverge under concurrent edits, that's a hidden bug. Probably out of M5; flag if observed.
- **conflict preview UX**: 20 lines per file might be too short. Decide during live-test in M5a whether to make it 50 or paginated.
