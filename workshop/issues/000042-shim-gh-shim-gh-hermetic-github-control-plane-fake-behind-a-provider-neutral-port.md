---
id: 000042
status: working
deps: []
github_issue:
created: 2026-06-05
updated: 2026-06-05
estimate_hours: 14
---

# shim(gh)+shim'(gh): hermetic GitHub control-plane fake behind a provider-neutral port

## Problem

nous integrates with GitHub's **control plane** (collaborator invitations, the
`repository_invitations` listing, MinimalRepository vs full-Repository JSON shapes,
multi-user access tokens) only through the `gh` CLI talking to real `api.github.com`.
That layer has **no hermetic test seam**, and it bites:

- `nous#41 #11` (re-invite hard-error fix) shipped with **zero automated coverage** —
  `lib/gh` execs the CLI with no injectable seam, so it was only "dogfood-verified."
- `nous#26` (GitHub-mediated onboarding) passed "build + vet" per milestone, then a manual
  run against real GitHub caught **five** control-plane bugs in succession: (1) the 404 fell
  on the *validation lookup* (`/users/<login>`) not the add; (2) `/user/repository_invitations`
  returns a **MinimalRepository** that omits `ssh_url`/`clone_url`, so `git clone "" tmpdir`
  failed; (3) consumed-invitation-but-failed-push left a stuck collaborator-but-unpublished
  state; (4) the discovery filter excluded single-recipient brains provisioned for sharing;
  (5) `make new-brain` didn't publish the operator pubkey to the keys branch.

Our `file://` bare-repo integration tests model the **data plane** (gcrypt push/pull, branch
ops) but not the **control plane** where the GitHub-shaped bugs lived (bugs 1–3 are squarely
control-plane; bugs 4–5 are brain-logic/data-plane bugs that the *combined* e2e flow needs the
control-plane fake to even reach). Function-call mocks
("every `gh.AddCollaborator` returns nil") cover trivial cases and miss exactly these
interaction bugs — the ones that only emerge from real-shaped responses and multi-call state.

Origin: ariadne#71 (the generic shim(X)/shim'(X) vision) + the auto-mocking pensives in brain
(`docs/vision/2026-05-19-01-pensive-auto-mocking-external-services.md`,
`2026-05-12-01-pensive-book-4-deterministic-shell.md`). gh is the **guinea pig** for the
pattern; Google OAuth is the planned second instance. ariadne#71 is the *final* step that
promotes the proven pattern to an ariadne architecture choice — it is gated on this issue
(and the OAuth instance) via `deps:` and does not change ariadne files until then.

## Spec

### The pattern (proven here, generalized later)

For an external service **X**, construct a pair behind a single seam:

- **`shim(X)`** — a **provider-neutral domain port**: a Go interface owned by *nous's* side
  of the boundary, expressed in terms of what a consumer of "this type of service" expects,
  *not* a verbatim copy of the vendor API and *not* exactly-what-current-code-happens-to-need.
  Multiple **adapters** sit behind it: a `real` adapter (execs `gh`) and a `fake` adapter (the
  mock). A future `gitlab`/other-vendor adapter should be able to satisfy the same port.
- **`shim'(X)`** — the **stateful in-memory fake** adapter: a model of X's control-plane state
  to the fidelity nous actually exercises (real-shaped values, multi-call/multi-user state,
  per-test teardown), through which all real-flow code paths run **unchanged**.

This is Ports & Adapters (hexagonal / anti-corruption layer). The port is simultaneously the
abstraction *over* the vendor and the interface internal code *consumes* — its dual purpose.

**Constructor convention (the one cross-service convention):** uniform shape
`New(Conf) Port` (real) and `NewFake(Conf) Port` (fake), where `Conf` is opaque and
service-specific. Nothing is shared between a gh `Conf` and a future oauth `Conf`; only the
*shape* of "construct a shim from a config the shim alone interprets" recurs.

### Transport decision: library calls (in-process), not bridge/RPC/channel

The seam is an **in-process Go interface**; the fake is an in-memory struct. Rejected:
- **RPC / `GH_HOST` HTTP bridge** — higher wire-fidelity, but we have already assigned
  wire-fidelity to the *grounding step* (below), so RPC pays for fidelity we get elsewhere; it
  also needs `gh` + a server in CI. (Would only flip if we wanted to run the fake as a
  standalone process for manual dogfooding, or had a non-Go consumer — neither holds today.)
- **Go channels** — model streaming/concurrency, not request/response service calls; would be
  hand-rolled req/resp correlation for no gain. (Channels may appear *inside* an adapter for a
  push/event service — e.g. an OAuth redirect callback — but not as the port's transport.)

A *stateful* in-process fake satisfies the **spirit** of AGENTS.md §5 ("process-level fake")
even though it is not literally a separate process — §5 was warning against per-call stubs
that carry no cross-call state, which this is not. The §5 wording amendment is deferred to
ariadne#71.

### The two-step / "make the assumptions explicit" model

1. **Ground the fake** against reality (GitHub API docs + manual testing) when it is built,
   refreshed periodically (~monthly). This pins "what we believe about GitHub." Expensive,
   rare, human-in-loop.
2. **Everything else tests against the grounded fake** — fast, hermetic, every run. The fake's
   fidelity is accepted as an axiom between groundings.

   Two co-equal payoffs:
   - **Regression-pinning** (backward-looking) — lock in the historical bugs so they can't
     silently return.
   - **Simulation-based testing** (forward-looking, the larger win) — the fake + tmpdir data
     plane turns a whole **multi-actor, multi-step GitHub flow into a hermetic, scriptable
     simulation**: operator creates a brain → invites → joiner accepts → publishes pubkey →
     auto-admit runs → membership drifts → someone leaves, all in one `go test`, state evolving
     across actors and time, no network, no VM. This is the deterministic shell extended
     *outward* to include GitHub. The payoff isn't "we caught 5 old bugs"; it's that **every
     future GitHub-touching feature can be developed and self-verified against a realistic
     simulation before a human ever touches a VM** — the whole point of the shim(X) pattern.

**Division of labor (load-bearing).** A library-level fake *replaces* `lib/gh` entirely, so:
- bugs in code that *consumes* the port (call sequencing, mishandling empty `ssh_url`, the
  stuck-invitation state — nous#26 bugs 2–5) are **catchable** by tests against the fake;
- bugs *inside* the seam's own translation to the API (wrong endpoint — nous#26 bug 1; a
  parse/flag error) live *below* the seam and are **invisible** to fake-backed tests. Those are
  caught **only** by the grounding step. Grounding is therefore not optional polish; it is the
  sole defense for that bug class.

### Grounding mechanism: dual-backend contract test (always in scope)

One **contract test** suite of port-behavior assertions ("invite → appears in pending →
accept → is a collaborator"; "invitation repos come back with empty `ssh_url`"; "PUT
collaborators no-ops against an existing/expired invitation") runs against **both** backends:
- the **fake** — always, in CI (fast);
- the **real `gh`** — build-tagged (e.g. `//go:build conformance`), run manually/monthly. A
  green real-run *certifies* the fake; a red one means GitHub (or our seam) drifted.

### The port surface (derived from actual consumers)

`lib/gh` is imported by **13 non-test files** (cmd/nous/brain_{misc,invite,join,publish,recipient},
lib/tui/brain/{list,accept_invite,invite_collab,root}, lib/brain/{operator,status},
lib/brainsync/{recipient,leave}). The port covers exactly the surface they use, re-expressed
in provider-neutral domain terms (concrete names settled in the plan), today realized by:
`AuthLogin`, `UserExists`(+`ErrUserNotVisible`), `CollaboratorPermission`, `ListCollaborators`,
`AddCollaborator`, `InviteCollaborator`, `DeleteRepoInvitation`, `RepoPendingInvitations`,
`PendingInvitations`, `AcceptInvitation`, `DeclineInvitation`, `RemoveCollaborator`,
`UserRepos`; data types `Invitation`, `UserRepo`, `RepoInvitation`.

**Documented peculiarity extension points** — the GitHub-isms we deliberately keep visible so
the fake can reproduce them and consumers can leverage them: MinimalRepository omits
`ssh_url`/`clone_url`; `PUT collaborators/<login>` no-ops (204, no email) against an existing
*or expired* invitation; `/users/<login>` lags for brand-new accounts (404 while the bearer
token works).

**Seam migration:** the 13 importers must receive the port instead of calling package-level
functions. The injection mechanism (constructor DI vs. a swappable package default) is a plan
decision; provider-neutral naming is a goal of this migration, since we chose the domain-port
altitude.

### The fake's state model (`shim'(gh)`)

In-memory: users (with login + bearer token); repos (owner, private/public, topics);
collaborators (login → permission); invitations both **user-side** (MinimalRepository shape)
and **repo-side**; a **current-token context** so a test switches between operator and joiner
identities; and **state injection** (visibility-lag / shadow-flag so `/users/<login>` 404s
while the token still works). The peculiarities above are encoded as first-class behavior.

**Data-plane coupling.** The fake models the *control plane* only. Repo content stays real git
on **tmpdir bare repos**; control plane = in-memory port calls, data plane = real git
clone/push/pull — the same split as real GitHub.

The wiring hazard to resolve in the plan: real MinimalRepository **omits** `clone_url`/`ssh_url`,
so today `Invitation.CloneSSHURL()`/`UserRepo.CloneSSHURL()` *fabricate* `git@github.com:<full_name>.git`
from `full_name` (gh.go:85–93, 306–314). If the fake keeps that peculiarity faithfully (omits the
URLs) the fabrication still points at **real GitHub**, defeating hermeticity. Resolution: make the
**clone-URL fabrication base injectable via `Conf`** (e.g. `Conf.CloneURLBase`, default
`git@github.com:`), so the real adapter fabricates `git@github.com:<full_name>.git` and the fake
fabricates `file://<tmpdir>/<full_name>.git`. This **preserves** the MinimalRepository peculiarity
(still omits `ssh_url`; only the fabrication target is redirected) and is the single seam where the
control-plane fake meets the tmpdir data plane.

## Done when

- `shim(gh)` exists: a provider-neutral port with a `real` adapter (the only thing that execs
  `gh`) and all 13 consumers migrated onto the port.
- `shim'(gh)` exists: a stateful in-memory fake adapter (multi-user, MinimalRepository shape,
  invitation no-op + visibility-lag peculiarities, tmpdir-bare-repo data-plane coupling).
- A dual-backend **contract test** grounds the fake: same assertions pass against the fake (CI)
  and, build-tagged, against real `gh` (run once to certify, documented as the periodic step).
- Regression coverage is pinned to the layer that can actually see each bug (each test fails if
  its fix is reverted):
  - **Bugs 2, 3 + nous#41 #11 (control-plane)** — pinned by **new** hermetic tests through the
    fake + tmpdir data plane (bug 2: MinimalRepository empty-`ssh_url` → `CloneURL` fallback;
    bug 3: accepted-but-unpublished recovery; #41 #11: re-invite re-sends + hard-errors on a
    list/delete failure).
  - **Bugs 4, 5 (data-plane / brain-logic)** — already pinned by existing `file://` tests that
    don't route through `gh` (`lib/brainsync/discovery_test.go` + `lib/brain/manifest_test.go`
    for bug 4; `lib/brain/integration_test.go` `TestPublishOwnPubkeyToRemote_*` for bug 5). M4
    **verifies these stay green**; it does not re-pin them through the fake (the fake doesn't
    see them — they bypass `gh`). Claiming otherwise was the over-reach the spec review flagged.
  - **Bug 1 (wrong endpoint, below the seam)** — pinned by a `real`-adapter unit test asserting
    the exact endpoint string for the user-existence probe (`/users/<login>` vs `/user`),
    runnable without network, **plus** the dual-backend contract test's real-`gh` run. A
    library-level fake structurally cannot see this; the spec's Division-of-labor section says so,
    and "Done when" must not claim otherwise.
- A **full-lifecycle simulation test** exists (the forward-looking payoff): a single hermetic
  `go test` drives the multi-actor onboarding flow end-to-end through the fake + tmpdir data
  plane (operator new-brain → invite → joiner accept → pubkey publish → auto-admit → leave),
  demonstrating the fake as a self-verification substrate for *future* GitHub-touching features,
  not just a regression harness.
- No ariadne files changed. ariadne#71 carries the deferred convention/§5/architecture.md work
  and is linked via `deps`.

## Plan

- [ ] Define the provider-neutral port interface + `Conf` (incl. injectable `CloneURLBase`);
      settle domain naming and the documented peculiarity extension points.
- [ ] `real` adapter: move today's exec-`gh` logic behind the port unchanged; nothing else
      execs `gh`. Add a `real`-adapter unit test asserting exact endpoint strings (the
      below-the-seam regression home for bug 1 — `/users/<login>` vs `/user`).
- [ ] Migrate the 13 consumers onto the port (decide injection mechanism: constructor DI vs.
      swappable default).
- [ ] `shim'(gh)` fake adapter: state model (users/repos/collaborators/invitations), multi-user
      token context, peculiarity behaviors, tmpdir-bare-repo data-plane coupling, per-test
      teardown.
- [ ] Dual-backend contract test (fake always; real `gh` build-tagged); run the real backend
      once to certify; document the periodic grounding step.
- [ ] Hermetic regression tests for nous#26 bugs 2–5 + nous#41 #11 through the fake + tmpdir
      data plane (bug 1 is covered by the real-adapter endpoint test + contract test above, not
      here).
- [ ] Update atlas for the new port/fake surface; record the grounding cadence.

## Log

### 2026-06-05
- 2026-06-05: closed M3 — contract suite built; TestContract_Fake green (4 port invariants) via two AsUser clients over one fakeState; conformance real backend compiles under -tags conformance and skip-gates cleanly without env; REAL certification pending operator test-account tokens (documented ~monthly manual step in contract_real_test.go header). --no-atlas: grounding cadence already documented in M2 e2e-atlas note + conformance file header; full atlas consolidation lands in M5; review verdict: unknown
- 2026-06-05: closed M2 — go build/vet clean; lib/gh fake: 11 unit tests pass incl. real git clone of tmpdir bare repo; -race clean; three peculiarities (MinimalRepository empty ssh_url, PUT-collaborators no-op vs existing invite, shadow-flag 404) asserted; TUI tests retrofitted to inject the fake; review verdict: FIX-THEN-SHIP
- 2026-06-05: closed M1 — go build/vet clean; full suite green outside sandbox (lib/brain gpg-agent failures are sandbox-IPC-only, confirmed passing sandbox-disabled @36.8s, count=1); lib/gh endpoint tests pin bug-1 below-seam; grep gate = zero free-function call sites; 13+ consumers migrated to injected gh.Client (constructor/struct DI); review verdict: FIX-THEN-SHIP

Filed from ariadne#71 brainstorm. Decisions locked with operator:
- **Interface altitude:** provider-neutral domain port (Ports & Adapters), with documented
  extension points for GitHub peculiarities we want to leverage. Survives a future
  gitlab/other-OAuth adapter.
- **Transport:** library calls / in-process stateful fake — not bridge/RPC (wire-fidelity is
  the grounding step's job), not channels.
- **Grounding:** dual-backend contract test, always in scope; sole defense for seam-translation
  (below-the-seam) bugs.
- **Scope/tracking:** built nous-only; ariadne#71 repurposed as the final "promote to
  architecture choice" step, gated on this issue via `deps:`. Stable cross-repo link is the
  `deps:` frontmatter, not prose.
- **Constructor convention:** `New(Conf)` / `NewFake(Conf)` uniform shape, opaque per-service
  `Conf`; nothing else shared across service shims (no `lib/testfakes` framework).

Spec review (fresh-eyes subagent) caught one load-bearing contradiction: "Done when" claimed the
library-level fake reproduces all 5 nous#26 bugs, but bug 1 (wrong endpoint) is *below the seam*
and only the grounding/contract test or a real-adapter endpoint unit test can see it (the pensive
could claim all-5 because its fake was an HTTP bridge, which we rejected). Reconciled: bugs 2–5 +
nous#41 #11 pinned by the fake+tmpdir flow; bug 1 pinned by a real-adapter endpoint test + the
contract test. Also specified the clone-URL→tmpdir mechanism (injectable `Conf.CloneURLBase`,
preserving the MinimalRepository peculiarity) and the bug-1 regression home.

Planning: claimed (#42 working, est 14h), `sdlc start-plan` delivered ARCH-PURE/ARCH-DRY.
Injection mechanism decided with operator: **constructor/struct DI** (a `gh.Client` threaded
through all consumers), chosen as the pattern exemplar over a package-default bridge. Durable plan
at `workshop/plans/000042-shim-gh-plan.md` (4 milestones M1–M4). Plan review (fresh-eyes) caught a
second scope over-reach: bugs 4 & 5 are data-plane/brain-logic bugs already pinned by existing
`file://` tests that bypass `gh` (`lib/brainsync/discovery_test.go` `TestFindSharedBrains_SingleRecipientWithGcryptRemote`;
`lib/brain/integration_test.go` `TestEndToEnd_OperatorPubkeyMissingThenRepublish` /
`TestPublishOwnPubkeyToRemote_OrphanCreate`) — so M4's *new* contribution is bugs 2/3/#41 #11
through the fake; 4/5 are only verified-green, not re-pinned. Spec "Done when" corrected to match.
