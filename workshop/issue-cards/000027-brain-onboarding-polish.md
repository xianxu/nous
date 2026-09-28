---
id: '000027'
status: done
created: 2026-05-20
updated: 2026-05-20
estimate_hours: 1.5
actual_hours: 1.2
---

# brain: three onboarding polishes (operator marker, clone-side error, new-brain pubkey publish)

## Problem

Three small things surfaced during yesterday's manual repro of
nous#26 that hurt the operator/joiner experience without changing
the core flow:

1. **`nous brain new` only publishes operator's pubkey under the
   legacy `<FP>.asc` convention.** If a joiner runs `nous brain join`
   against a fresh brain before the operator publishes anything
   else, the joiner's `PublishOwnPubkeyToRemote` orphan-creates the
   keys branch with just their `<login>.asc` — operator's key
   isn't there at all, and subsequent clones fail at signature
   verify. The user hit this exact case yesterday; the recovery
   was operator running `nous brain join xianxu/brain-family`
   (republish mode) from their own host.

2. **`nous brain clone` propagates gcrypt's raw "No public key"
   error.** When the keys branch is missing the operator's pubkey,
   the user sees a 6-line gpg/gcrypt error and has to know
   internally what it means. Should detect the case and surface a
   clear "the brain's keys branch is missing the operator's pubkey
   — ask them to run `nous brain join <repo>` to publish it"
   message.

3. **`nous brain list` doesn't mark which brains the current user
   can act as operator on.** The user can run `nous brain invite`
   on any brain that's locally listed, but only the operator
   (personal-repo owner or org-repo Maintain+) can actually invite
   — the rest will get a 403 from GitHub at action time. The TUI
   / list should mark operator brains with a `*` prefix so the
   capability is visible upfront.
