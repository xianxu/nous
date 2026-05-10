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
