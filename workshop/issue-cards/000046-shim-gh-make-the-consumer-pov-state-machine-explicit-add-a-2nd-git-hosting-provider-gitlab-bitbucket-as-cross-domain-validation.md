---
id: 000046
status: open
created: 2026-06-08
updated: 2026-06-08
estimate_hours:
github_issue:
---

# shim(gh): make the consumer-POV state machine explicit + add a 2nd git-hosting provider (gitlab/bitbucket) as cross-domain validation

## Problem

`shim(gh)` (nous#42) ships a stateful fake behind a provider-neutral port, but
its consumer-POV state machine is **implicit** — it lives inside `fakeState` and
the contract test's ad-hoc invariants, never written down. Constructing
`shim(google-oauth)` (nous#44) surfaced that the shim pattern is more than
port + stateful fake: the fake is an **executable model of the provider's hidden
state machine**, and the consumer-POV machine (S) deserves to be a first-class,
explicit artifact. See `ariadne/workshop/pensive/2026-06-08-01-pensive-shim-state-machines.md`
for the R / M / S framing and the "hidden provider state manifests as faults"
finding.

Two gaps to close, in order:

1. **gh's S is implicit.** The invite/collaborator lifecycle
   (`NotInvited → InvitePending → Collaborator(perm)` + decline/delete/remove),
   the nous#25 new-account **visibility lag** (`NotVisible → Visible`, a
   provider-autonomous transition fired on GitHub's clock, materialized on our
   next `/users/<login>` lookup — gh's analogue of OAuth's clock-driven
   `Active → Expired`), and the `PUT collaborators` no-op-against-existing-
   invitation peculiarity (nous#41 #11) are all real states/edges that exist only
   in code, not in a spec.
2. **gh's port is validated against one real backend (GitHub only).** A single
   real provider lets provider-specific quirks masquerade as the abstraction.
   The pattern's durability claim needs the port + S to hold against a *second*
   git-hosting provider (GitLab or Bitbucket).

**Sequencing (load-bearing):** Do NOT start this until explicit-S has proven out
on nous#44 (Google + a second OAuth provider). Per ariadne#71's own
"don't generalize from n=1" rule, retrofitting gh to explicit-S before the
meta-pattern is proven would be generalizing from a single unfinished instance.
`deps: [nous#44]` enforces this. gh is then the deliberate **cross-domain
validation**: oauth is a credential-lifecycle machine; gh is a control-plane CRUD
machine — proving one S-formalism fits both is the evidence #71 needs before
promoting "explicit user-side state machine" to a fixed design decision.
