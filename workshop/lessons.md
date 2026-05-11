# Lessons

Patterns of what went wrong + rules to prevent repeating. Per AGENTS.md
§4, this file is updated whenever a code review surfaces issues worth
remembering.

---

## 2026-05-09 — nous#14 M4 review

Three Important findings, all caught at the milestone boundary review:

1. **WriteManifest had a documentation/reality mismatch.** Comment
   said "operators can hand-edit, nous won't clobber"; code rewrote
   the entire file on every recipient change. → Add a *separate*
   function for partial rewrites when the doc claims partial-rewrite
   semantics. `RewriteFrontmatter` now exists for the
   recipient-mutation path; `WriteManifest` is provisioning-only.
   **Rule:** when a function has multiple call sites with different
   needs (provisioning vs in-place edit), split early — single
   function with "use this one way" doc-claims is a smell that the
   code doesn't actually deliver.

2. **Push-failure recovery was advertised but not wired.** Error
   message said "re-run after fixing the remote"; re-running
   short-circuited on the desired state being already present
   locally and never retried the push. → Detect unpushed commits
   on re-run and route to a push-only retry path. **Rule:** any
   error message that promises a recovery path must be backed by
   actual logic that exercises that path. Adding a test that
   simulates push-failure-then-rerun would have caught this.

3. **Multi-key armor admit was a delegation-boundary breach.**
   `Inspect` returned only `keys[0]` from a multi-key armor; `Import`
   then admitted *all* keys to the keyring. The verify-fingerprint
   ceremony saw one key but the operator's keyring grew by two —
   silent expansion of access. → `Inspect` now refuses multi-key
   armors; `Import` runs a before/after diff against
   `--list-public-keys` as defense-in-depth. **Rule:** when a
   primitive's job is "let humans verify what's about to happen,"
   ANY discrepancy between what the human sees and what the call
   does is a threat-model bug, not a UX nit. Worth a security-
   boundary test (now `TestInspect_RefusesMultiKeyArmor`).

Other findings (Minor — fixed in same patch but not lesson-worthy on
their own): lookupKey accepting suffix matches with no length floor;
gpg --export missing `--` separator; `confirmKey` ceremony for the
already-imported path was theater not verification.

**Process note:** the milestone code review was dispatched after
M4a + M4c + M4b all landed (single review covering ~1300 lines of
diff). Findings were cheap to address (one hour total). Reviewer
correctly flagged that the cmd-level safeguards (last-recipient,
self-removal, revocation prompt) lacked tests; left as Minor and
deferred. **Rule:** for milestone reviews, prioritize
threat-model-touching and recovery-path findings over coverage gaps;
coverage can be backfilled, but data loss / boundary breaches
shouldn't ship.

---

## 2026-05-10 — nous#14 M5 review

Six Important findings, no Critical. Themes:

1. **State-file writes need atomicity from the first commit, not as a
   review fix.** `SetPrimary` shipped using bare `os.WriteFile` with
   `0o644` perms. Two terminals racing on `nous identity primary`
   could leave a half-written file that subsequent reads choke on
   ("expected 40-char fingerprint, got N chars"). Already had
   `atomicWrite` in `lib/brain/write.go` — should have reached for
   it. → **Rule:** any non-trivial file write (config, state,
   manifest, session) uses tmp + rename + tight perms. Code-search
   for `os.WriteFile\(` during review is a cheap pre-emptive sweep.

2. **Confirmation prompts default-yes is a footgun on EOF.**
   `confirmPersist` used `[Y/n]` with empty-as-yes, and
   `fmt.Fscanln` returns `""` on EOF. A TTY-attached stdin closed
   via ctrl+d → silent persist of a heuristic candidate the operator
   never confirmed. → **Rule:** for state-mutating confirmations,
   require explicit `y`/`yes`, treat empty / EOF / error as decline.
   Default-yes only for read-only "show me more" prompts.

3. **Fail-closed vs fail-open: the docstring isn't enough.**
   `WouldLockOut` (the self-removal safeguard) said in its comment
   "callers should default to blocking on err" — but the
   implementation returned `(false, err)` on gpg outage. A future
   caller that forgets to check `err` would silently SKIP the
   safeguard. → **Rule:** for safety-critical predicates, return
   the SAFE boolean on error (`true` for "would lock out", `true`
   for "is dangerous"), so a caller that ignores the error still
   errs on the right side. Document the choice; let the compiler
   enforce it via the return value.

4. **Audience tags (a)/(h)/(b) must be machine-stable for (a) and
   (b) modes.** `nous identity primary` was tagged `(b)` in the
   atlas but emitted a verbose multi-line prose block on non-TTY
   that an agent couldn't reliably parse. → **Rule:** any subcommand
   tagged `(a)` or `(b)` MUST emit a stable single-line shape on
   non-TTY (e.g. `primary: <fp> (stored)`). The TTY-only verbose
   path is bonus; the agent contract is the floor.

5. **Cosmetic annotation tier vs functional safeguard tier are
   separate concerns and must stay separate.** The `(self)` /
   `(local secret)` annotation is a UI label; `WouldLockOut` is the
   functional safeguard. Conflating them ("if annotation starts
   with (self) then run REMOVE-SELF") was the M5b first-draft bug.
   Operator caught it during live-test when a throwaway key got
   labeled `(self)`. → **Rule:** when you find yourself string-
   matching on a UI tier to drive logic, stop. The logic gets a
   typed predicate; the UI tier is downstream of the predicate, not
   the other way around.

6. **Pre-compute safety markers before the action commits.** Picker
   row showed the recipient annotation but not the would-lock-out
   marker; operator discovered the safeguard only after pressing
   enter. → **Rule:** if a row in a picker can trigger a destructive
   safeguard, the row itself surfaces that fact before selection.
   Don't make the operator pay the round-trip to discover what
   pressing enter will do.

Process notes for M5:
- **Side-quest mid-flight, triggered by operator live-test.** Adding
  the throwaway key during M5b's verification surfaced the
  `(self)` / multiple-secret bug. Triage was fast (operator
  feedback was specific), but the side-quest meaningfully expanded
  M5b's scope (new lib/identity surface, new state file, new
  subcommand). Logged in the commit message as `(side-quest)` per
  AGENTS.md §12. **Rule:** side-quests don't need a ticket if the
  operator is in the loop and the scope is bounded; do log the tag
  in the commit so future grep on `^#14.*side-quest` recovers what
  happened.
- **DRY violation caught in review, not in implementation.** The
  brain-private-recipient heuristic was implemented twice (once in
  `lib/brain/annotate.go`, once in `cmd/nous/identity_primary.go`)
  with near-identical bodies. Reviewer flagged it; lifted to
  `lib/brain.HeuristicPrimary` in the review-fix commit. AGENTS.md
  §6 ("more general and elegant way") would have caught it pre-
  commit; I missed the pause. **Rule:** when implementing the
  second copy of a small algorithm, the pause is mandatory, not
  optional.
