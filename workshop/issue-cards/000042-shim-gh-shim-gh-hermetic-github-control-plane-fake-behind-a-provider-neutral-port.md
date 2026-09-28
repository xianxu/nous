---
id: '000042'
status: done
created: 2026-06-05
updated: 2026-06-05
estimate_hours: 14
actual_hours: 0.30
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
