# Recipient onboarding — GitHub-mediated

How a new person becomes a recipient on a shared brain after
nous#26 landed (2026-05-19). Replaces the legacy "operator hand-
imports a `.pub` file, runs a fingerprint verify ceremony, then
publishes via `nous brain recipient add`" flow with a two-command
exchange that goes through GitHub's collaborator-invitation
mechanism end-to-end.

## Trust anchor

**The act of the operator inviting a GitHub identity to the repo IS
the act of admission.** Whatever GPG identity the invitee chooses to
publish under that GitHub identity's authority — i.e., pushed to a
branch only they can push to — inherits the trust. No fingerprint
negotiation between humans is required to start participating.

The model is exactly WhatsApp's: phone-number-add admits the
contact; safety-number verification is an opt-in tamper check
("verify on suspicion"), not a precondition for chatting. Here:
github-collaborator-add admits the recipient; fingerprint
verification (the M6 path) is an opt-in tamper check, not a
precondition for being encrypted-to.

The trust model assumes the operator's "I add this GitHub user as
a collaborator" claim is the gating decision. The substrate
guarantees:

- Only people the operator (or an admin/maintain-tier collaborator
  on an org repo) has invited can push to any branch — GitHub's
  authorization layer enforces this.
- Only people in `.brain/config.md` recipients can decrypt main —
  gcrypt's encryption layer enforces this.
- Auto-admit closes the loop between the two: GitHub-collaborator
  publishes a `<login>.asc` on the keys branch; brain-sync detects
  it, appends to recipients, push re-encrypts.

## The two commands

### Operator side: `nous brain invite GITHUB-LOGIN`

(`cmd/nous/brain_invite.go`, nous#26 M2)

1. Validates `GITHUB-LOGIN` exists on GitHub (with `--force` to
   skip — nous#25's fresh-account cache-lag escape hatch).
2. Picks the target brain. Multi-brain workspaces prompt; single-
   brain is automatic.
3. Resolves `remote.origin.url` → `owner/repo` via
   `brain.GitHubOwnerRepo`.
4. Calls `gh.AddCollaborator(owner, repo, login, "push")`.

Done. Operator's role ends here. The invitee will be auto-admitted
once they accept and publish their pubkey.

### Invitee side: `nous brain` (TUI) — `nous brain join` is the CLI plumbing

The canonical joiner surface is the `nous brain` TUI. It lists
pending GitHub repository invitations (filtered to brain projects
via the `nous brain new` markers — description prefix
`nous-brain:` or topic `nous-brain`) inline with local brains and
accessible-but-not-cloned repos. The invitee navigates to a
pending row, presses `enter`, and the inline `accept_invite.go`
flow handles GPG identity selection, `gh.AcceptInvitation`, and
the plain-git push of `<login>.asc` to the keys branch — all
without a subprocess or terminal handoff. After the accept, the
brain appears as accessible-but-not-cloned; `enter` again
launches the clone subprocess.

(`lib/tui/brain/accept_invite.go` + `lib/tui/brain/list.go`,
nous#26 M5 + nous#27.)

`cmd/nous/brain_join.go` is the underlying CLI plumbing for the
same actions. Most operators won't reach it directly; it stays
exposed because:

- It's the **republish** path: `nous brain join OWNER/REPO`
  re-pushes the invitee's pubkey to a specific brain's keys
  branch. Used when an earlier accept succeeded but the publish
  failed, or after a GPG key rotation.
- It's the **non-TTY fallback**: scripted environments without
  an interactive TUI can still bulk-accept via the CLI.

Both paths converge on the same underlying steps:

1. `gh.PendingInvitations()` filtered to brain projects.
2. `gh.AcceptInvitation(id)` to flip the invitation.
3. `brain.PublishOwnPubkeyToRemote(remoteURL, login, armor)` to
   plain-git clone the keys branch, write `<login>.asc`, push back.
   Orphan-creates the keys branch if it doesn't exist.

GitHub is reduced to "identity provider" in this flow. The
invitee never opens github.com.

## Auto-admit

(`lib/brain/autoadmit.go::AutoAdmitFromKeysBranch`, nous#26 M3)

On every brain-sync tick (`lib/brainsync/watch.go`), after
`syncBrainPubkeys` imports any new pubkeys into the local keyring,
auto-admit scans the keys branch for `<login>.asc` whose
fingerprint isn't yet in the manifest's recipients. New ones get
appended; `AddCommitPush` rewrites `.brain/config.md`, the
nous#24 push wrapper syncs `gcrypt-participants` from the manifest,
and the gcrypt push re-encrypts to the new set.

Filename discriminator: stems matching the 40-hex-uppercase
fingerprint shape are skipped — those are legacy nous#23 entries
(operator-published peer pubkeys), already in the manifest by
construction. The new convention is `<login>.asc` where stem ≠
fingerprint.

**No prompt, no gate.** The trust anchor (the operator's earlier
GitHub-collaborator-add) already happened; auto-admit is the
mechanism following the decision through.

## Drift detection (the safety floor)

(`lib/brain/verified.go`, nous#26 M6)

The single concession to "what if the trust anchor was wrong, or
the substrate gets MITM'd later." When the operator runs
`nous brain recipient verify` and the OOB last-8 ceremony
succeeds, the verification is persisted to `.brain/verified.yaml`:

```yaml
yingtest42:
  fingerprint: 653D6B6D5A2268F7E4D06773858BD736ADCF7FD3
  verified_by: xianxu
  verified_at: 2026-05-20T14:32:00Z
```

Keyed by GitHub login. Lives in the brain root, encrypted with
everything else.

Auto-admit honors these entries: if `<login>.asc` on the keys
branch has a fingerprint that differs from the verified entry,
auto-admit refuses to admit the new fingerprint and returns a
`DriftEvent` for the caller (brainsync) to log loudly. The
operator must re-verify (writing the new fingerprint to
`verified.yaml`) before the substituted key becomes acceptable.

This is the only safety property the otherwise-fully-automatic
flow has against a substituted-key MITM. It's also opt-in: if the
operator never verifies anyone, drift detection never fires (and
auto-admit accepts whatever shows up). That's the WhatsApp tradeoff
made consciously — admission is convenient by default; explicit
verify is the suspicion-mode escape.

## Trust circle, in pictures

```
GitHub collaborator list                gcrypt recipients in manifest
─────────────────────────               ──────────────────────────────
xianxu (owner)         ←─────── invite ──────→  xianxu's fp
yingtest42 (push)                ─auto-admit─→  yingtest42's fp
                                  (via <login>.asc on keys branch)

verified.yaml (operator's pinned claims)
────────────────────────────────────────
yingtest42 → DC73…B6E9 (verified by xianxu on 2026-05-20)
            ↑
            └── drift detection fires if keys-branch fingerprint
                for yingtest42 ever differs
```

## What's NOT in this flow

- **Offline / sneakernet admission via `.pub` file**: the legacy
  `nous brain recipient add` path. Still in the code (used at brain
  creation time to publish operator's own pubkey as `<FP>.asc`),
  but the operator-facing CLI doesn't surface it for new
  admissions. Deprecated; remove when no brain in the wild has
  legacy `<FP>.asc` entries that need maintenance.
- **GPG keyserver lookups**: never used. The keys branch IS the
  distribution mechanism for this trust circle. Cross-brain
  identity (one GPG key, many brains) works fine — the same
  pubkey just appears under each brain's keys branch
  independently.
- **Web-of-trust signatures on the pubkeys themselves**: not
  consulted. Trust comes from GitHub-collaborator membership +
  optional OOB verify, not from third-party GPG signatures.

## Key file map

| file | purpose |
|---|---|
| `cmd/nous/brain_invite.go`        | operator invite CLI |
| `cmd/nous/brain_join.go`          | invitee join CLI plumbing (+ republish mode); most invitees use the TUI instead |
| `lib/tui/brain/accept_invite.go`  | TUI inline accept-invite flow (the primary invitee surface) |
| `lib/tui/brain/list.go`           | renders pending invitations + accessible-but-not-cloned rows |
| `cmd/nous/brain_recipient.go::Verify` | OOB-ceremony CLI + verified.yaml persistence |
| `lib/gh/gh.go`                    | thin wrappers over `gh api` |
| `lib/brain/autoadmit.go`          | auto-admit + drift detection logic |
| `lib/brain/verified.go`           | verified.yaml read/write + LoginForFingerprint |
| `lib/brain/peerkeys.go::PublishOwnPubkeyToRemote` | invitee-side keys-branch push (with orphan-create) |
| `lib/brainsync/discovery.go`      | `FindSharedBrains` (watch-list predicate) |
| `lib/brainsync/watch.go::autoAdmitBrain` | per-tick caller of auto-admit |
| `lib/brain/integration_test.go`   | TestEndToEnd_GitHubMediatedOnboarding, _DriftDetection, _OperatorPubkeyMissingThenRepublish |

## When this doc gets stale

- The trust anchor changes (e.g., we add a separate ACL layer
  between GH-collaborator and gcrypt-recipient — would invalidate
  the "GH-add IS admission" claim).
- A new substrate replaces GitHub as the transport (e.g., a custom
  git-daemon for self-hosted brains) — the "GitHub as identity
  provider" framing wouldn't apply.
- Drift detection's behavior changes (e.g., we add automatic
  revocation on drift, or quarantine + manual-resolve modes).
- The verify ceremony adds a TUI for batch verification — would
  need a brief section on the TUI's surface and when to use it
  vs the single-FP CLI.

The integration tests are the authoritative behavior. This atlas
is the why and the cross-mechanism map.
