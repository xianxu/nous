---
id: 000046
status: open
deps: [nous#44]
github_issue:
created: 2026-06-08
updated: 2026-06-08
estimate_hours:
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

## Spec

Following the explicit-S form validated by nous#44:

- **Make gh's S explicit** as a `target` artifact (an invariant defended from
  drift): states, transitions, the operation/observation that fires each, the
  **provider-autonomous transitions** (the fault set — visibility lag,
  list-invitations failure, the PUT no-op peculiarity), and per-transition
  grounding status. Provider-neutral (GitHub/GitLab/Bitbucket share the control-
  plane lifecycle; the wire is the variant).
- **Recast `shim'(gh)`'s fault knobs** (`FailListInvitations`, the shadow/visibility
  flag, …) as the named R-autonomous transitions of S — principled, not ad-hoc.
- **Recast the dual-backend contract test** from ad-hoc invariants into an
  S-**transition-coverage table** bisimulating S against the real backend(s) on
  the observable quotient.
- **Add a 2nd git-hosting provider** (GitLab or Bitbucket — pick during design)
  behind the same `Client` port: a `real` adapter for it + the shared fake
  satisfying the same S. Build against **both real providers + the fake at once**
  (the nous#44 process finding) so quirks can't masquerade as the abstraction.
  Expect the port and/or S to need adjustment — that adjustment *is* the
  validation signal.
- **Expectation:** the S-articulation is largely a test+doc layer over the fake's
  existing state — likely **zero production change** to `lib/gh`'s consumer code.
  The 2nd provider is the part that adds real code (a new adapter + whatever port
  generalization it forces).

## Done when

- gh's consumer-POV state machine S is an explicit `target`, referenced by both
  adapters and the contract; the fake's faults are named as S's R-autonomous
  transitions.
- The gh dual-backend contract is a transition-coverage table over S.
- A 2nd git-hosting provider (gitlab/bitbucket) has a `real` adapter behind the
  same port, satisfying the same S; the fake certifies to both. Any port/S change
  the 2nd provider forced is recorded as the cross-domain-validation finding.
- The finding feeds back to ariadne#71 (whether one S-formalism fits both the
  credential-lifecycle and control-plane-CRUD domains).

## Plan

- [ ] (design) Extract gh's implicit S from `fakeState` + the contract invariants; write the provider-neutral `target` (states, transitions, R-autonomous fault edges, per-transition grounding).
- [ ] Recast `shim'(gh)` fault knobs as the named R-autonomous transitions of S.
- [ ] Recast `contract_test.go` into an S transition-coverage table (fake always; real GitHub build-tagged).
- [ ] Pick gitlab vs bitbucket; add its `real` adapter behind the `Client` port; generalize the port/`Conf` only as far as the 2nd backend forces.
- [ ] Ground both real backends + the fake against the same S; record port/S adjustments as the validation finding; report back to ariadne#71.

## Log

### 2026-06-08

Filed as the deferred gh follow-up from the nous#44 design discussion. The shim
state-machine framing (R real / M fake-as-model / S consumer-POV) is captured in
`ariadne/workshop/pensive/2026-06-08-01-pensive-shim-state-machines.md`. Gated on
nous#44 so explicit-S proves out on the oauth (credential-lifecycle) instance
before retrofitting gh as the control-plane-CRUD cross-domain validation. This
issue strengthens ariadne#71's "pattern proven + generalized" evidence before the
§5/ARCH promotion; consider adding it to #71's `deps:` if the operator wants the
gate to machine-enforce the cross-domain validation too.
