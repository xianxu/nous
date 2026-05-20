---
id: 000026
status: done
deps: [000023, 000024, 000025]
created: 2026-05-19
updated: 2026-05-20
estimate_hours: 6
actual_hours: 7
---

# brain: GitHub-mediated recipient onboarding (invite/join/auto-admit/verify)

## Problem

Adding a new recipient to a shared brain today requires a manual
sneakernet ceremony even with #23 in place:

1. New user exports their pubkey (`gpg --armor --export`)
2. SCP / copy/paste to operator's host
3. Operator inspects fingerprint, verifies out-of-band
4. Operator runs `nous brain recipient add` with the `.pub` file
5. Operator separately runs `gh api PUT collaborators/<login>` (or
   web UI) to grant GitHub access
6. New user accepts the GitHub invite via web UI

Six steps across two humans, three trust handoffs (pubkey transfer,
fingerprint compare, GitHub invitation accept), and zero of them are
required by the actual threat model. The trust anchor is "operator
chose to invite this GitHub identity" — same model as WhatsApp ("you
chose to add this phone number"). Everything else is mechanism that
can be automated.

## Insight

GitHub-collaborator-add IS the trust admission. Whatever GPG identity
the invited user chooses to publish under that GitHub identity's
authority (pushing to a branch only they can push to) inherits the
trust. Fingerprint verification becomes an *opt-in tamper check*
("did GitHub or my pull get MITM'd?"), not a gate — exactly WhatsApp's
verify-safety-number tab.

Three pieces of new mechanism collapse the six-step ceremony into
two commands, one per human:

- **`nous brain invite <gh-login>`** (operator): TUI picks the brain;
  validates the gh-login exists; sends the GitHub collaborator
  invitation. Done. Operator goes back to whatever they were doing.

- **`nous brain join`** (new user): TUI lists pending GitHub invites
  filtered to brain projects (via repo description / topic marker);
  user picks; tool accepts the invite via `gh api`, plain-git pushes
  their pubkey to the `keys` branch. Done. They never open github.com.

- **Auto-admit in `brainsync.PullBrain`** (operator's side): scans
  `keys` branch for `<login>.asc` whose fingerprint isn't in the
  manifest yet, appends, commits, pushes. No prompt. The #24 push
  wrapper handles `gcrypt-participants` sync.

Opt-in verify (`nous brain recipient verify`) protects against
future MITM. Sidecar `.brain/verified.yaml` records who's been
verified by whom and when; brain-sync raises an alarm if a verified
recipient's fingerprint ever changes.

## Done when

### Operator side

- `nous brain invite <gh-login>` opens a TUI listing all local
  brains in the operator's workspace (private and shared alike —
  pre-first-recipient brains turn into shared via this very flow).
  Annotation: `[private]` vs `[shared, N recipients]`.
- The command validates `gh api users/<gh-login>` returns 200
  (with a `--force` flag to override for fresh-account-lag cases
  like nous#25). Errors clearly if not found.
- On confirm, runs `gh api -X PUT repos/<owner>/<name>/collaborators/<gh-login>`
  with `permission=push`. Reports the invitation URL the user
  must accept.
- `nous brain new` (and `scripts/new-brain.sh`) set both the
  description marker (`nous-brain: <name>`) and the topic
  (`nous-brain`) on the GitHub repo so `nous brain join` can
  filter invitations on the joiner's side.

### New-user side

- `nous brain join` calls `gh api user/repository_invitations`,
  filters to entries whose `repository.description` starts with
  `nous-brain:` OR `repository.topics` contains `nous-brain`.
- TUI shows the filtered list (multi-select); user picks one or
  more.
- For each picked: accepts the invitation via
  `gh api -X PATCH user/repository_invitations/<id>`; plain-git
  clones the `keys` branch only (no decryption needed);
  writes `<gh-login>.asc` containing armored pubkey; pushes.
- GPG identity selection:
    - 0 keys → error "run `nous identity init` first."
    - 1 key → use it.
    - >1 keys → prompt to pick (same UX as `make new-brain`).
- Reports: "Published. Operator will admit you on next sync.
  Run `nous brain clone <url>` once you're admitted (or wait
  for brain-sync to do it automatically)."

### Auto-admit

- `brainsync.PullBrain` (post-#23) already calls
  `ImportAllPubkeys` to bring keys-branch pubkeys into the
  keyring. Extends to: for each `<login>.asc` whose fingerprint
  is NOT in the manifest's recipients, append, commit (msg:
  `auto-admit <login>`), call `AddCommitPush` (which the #24
  wrapper handles end-to-end including `gcrypt-participants`
  sync).
- Audit log line in brain-sync's structured log: who got
  auto-admitted, by which sync cycle, at what time.

### Verify (opt-in, anytime)

- `nous brain recipient verify` opens a TUI listing all
  recipients with their verification status:
  `unverified` (default after auto-admit) or `verified by <login>
  on <date>`.
- Picking a recipient shows side-by-side: github-login, GPG UID
  (name + email from the pubkey), full fingerprint, last 8 hex.
- "Compare with the human out of band. Verified? [y/N]"
- On y: writes/updates `.brain/verified.yaml` entry keyed by
  github-login: `{fingerprint, verified_by, verified_at}`.
- Drift detection in `brainsync.PullBrain`: if a recipient's
  fingerprint in the keys branch differs from their entry in
  `verified.yaml`, log a loud warning and pause auto-admit for
  that recipient (operator must explicitly re-verify).

### Cross-cutting

- Identity display convention: github-login as primary key
  everywhere. GPG UID (name + email) shown alongside. Fingerprint
  abbreviated to last 8 hex in lists; full when verifying or
  drift-detecting.
- All TUIs follow the existing nous brain TUI patterns
  (bubbletea, same key bindings, same color palette).

## Spec

### `nous brain invite <gh-login>`

```go
// cmd/nous/brain_invite.go
func runInvite(ghLogin string, force bool) error {
    // 1. Validate target user exists on GitHub.
    if !force {
        if _, err := gh.UserExists(ghLogin); err != nil {
            return fmt.Errorf("github user %q not visible: %w (use --force to bypass)", ghLogin, err)
        }
    }
    // 2. List all local brains; TUI to pick.
    brains, err := brain.DiscoverAll(workspace.Root())
    if err != nil { return err }
    picked, err := tui.PickBrain(brains)  // returns brain.Manifest + path
    if err != nil { return err }
    // 3. Derive GH repo from remote URL.
    owner, repo, err := brain.GitHubOwnerRepo(picked.Path)
    if err != nil { return err }
    // 4. PUT collaborator invitation.
    if err := gh.AddCollaborator(owner, repo, ghLogin, "push"); err != nil {
        return err
    }
    fmt.Printf("Invitation sent. %s can accept via `nous brain join` or at:\n", ghLogin)
    fmt.Printf("  https://github.com/%s/%s/invitations\n", owner, repo)
    return nil
}
```

### `nous brain join`

```go
// cmd/nous/brain_join.go
func runJoin() error {
    // 1. List pending invites.
    invites, err := gh.PendingInvitations()
    if err != nil { return err }
    // 2. Filter to brain projects.
    brainInvites := filterBrains(invites)  // description prefix OR topic
    if len(brainInvites) == 0 {
        fmt.Println("No pending brain invitations.")
        return nil
    }
    // 3. TUI multi-select.
    picked, err := tui.PickInvitations(brainInvites)
    if err != nil { return err }
    // 4. Pick GPG identity (0/1/many handling).
    fp, err := identity.SelectForJoin()
    if err != nil { return err }
    // 5. For each picked invite: accept + push pubkey.
    for _, inv := range picked {
        if err := gh.AcceptInvitation(inv.ID); err != nil { return err }
        if err := brain.PublishOwnPubkey(inv.RepoCloneURL, gh.AuthLogin(), fp); err != nil {
            return err
        }
        fmt.Printf("Joined %s. Wait for operator to admit you.\n", inv.RepoFullName)
    }
    return nil
}
```

`brain.PublishOwnPubkey` is a thin wrapper around the existing
`filestore` abstraction (#23): plain-git clone of `keys` branch,
`Put("<login>.asc", armoredPubkey)`, push.

### Auto-admit

```go
// lib/brainsync/pull.go — extends existing PullBrain after ImportAllPubkeys.
func autoAdmit(brainDir string) error {
    m, err := brain.Read(filepath.Join(brainDir, ".brain", "config.md"))
    if err != nil { return err }
    branchFiles, err := filestore.List(brainDir, "keys")
    if err != nil { return err }
    existing := stringset.FromStrings(m.Recipients)  // case-insensitive
    var added []string
    for _, f := range branchFiles {
        if !strings.HasSuffix(f.Name, ".asc") { continue }
        login := strings.TrimSuffix(f.Name, ".asc")
        fp, err := gpg.FingerprintFromArmored(f.Content)
        if err != nil {
            log.Warnf("auto-admit: skipping %s: %v", f.Name, err)
            continue
        }
        if existing.Contains(fp) { continue }
        m.Recipients = append(m.Recipients, fp)
        added = append(added, fmt.Sprintf("%s (%s)", login, fp[len(fp)-8:]))
    }
    if len(added) == 0 { return nil }
    if err := brain.RewriteFrontmatter(filepath.Join(brainDir, ".brain", "config.md"), m); err != nil {
        return err
    }
    msg := "auto-admit " + strings.Join(added, ", ")
    return brainsync.AddCommitPush(brainDir, msg)
}
```

### Verify drift detection

`verified.yaml` (committed to brain repo, encrypted along with
everything else):

```yaml
yingtest42:
  fingerprint: 653D6B6D5A2268F7E4D06773858BD736ADCF7FD3
  verified_by: xianxu
  verified_at: 2026-05-20T14:32:00Z
alice:
  fingerprint: 0ECF6AC06E9BB6C5B928F10B5D6885D83872C2F0
  verified_by: xianxu
  verified_at: 2026-05-15T09:11:00Z
```

In `autoAdmit`, before appending a new fingerprint for an existing
login (i.e., login is already verified but the FP differs):

```go
if v, ok := verified[login]; ok && v.Fingerprint != fp {
    log.Errorf("DRIFT: %s's pubkey changed from %s to %s — possible MITM. " +
               "Pausing auto-admit for this login. Run `nous brain recipient verify %s` to re-verify.",
               login, v.Fingerprint, fp, login)
    return nil  // skip this login; continue with others
}
```

## Plan

- [x] M1: `gh` helper wrappers in `lib/gh/`: `UserExists`,
      `AddCollaborator`, `PendingInvitations`, `AcceptInvitation`,
      `AuthLogin` (+ `DeclineInvitation` for symmetry). All thin
      wrappers around `gh api` subprocess calls; no new dependencies.
      Smoke-tested against live gh: AuthLogin returns the auth'd
      login, UserExists returns `ErrUserNotVisible`-wrapped for the
      404 case, PendingInvitations parses (empty slice). Mutating
      ops (AddCollaborator, AcceptInvitation) will be exercised in
      M2.
- [x] M2: `cmd/nous/brain_invite.go`. Reused
      `brain.DiscoverAll()`. Picker is a numbered prompt (not full
      bubbletea TUI) — same UX as `scripts/new-brain.sh`. Multi-
      brain path shows kind ([private]/[shared, N]). `--brain PATH`,
      `--force`, `--yes` flags. Added `lib/brain/url.go` with
      `GitHubOwnerRepo(remoteURL)` for parsing gcrypt/ssh/https/SCP
      forms (+ table-driven test).
- [x] M3: Auto-admit implemented in `lib/brain/autoadmit.go`
      (`AutoAdmitFromKeysBranch`); wired into `lib/brainsync/watch.go`
      via new `autoAdmitBrain` helper, called after each tick's
      `syncBrainPubkeys`. Push uses `AddCommitPush` so #24's
      gcrypt-participants sync handles re-encryption atomically.
      Unit tested `looksLikeFingerprint` (the legacy-vs-new
      filename discriminator). Full end-to-end exercise lands
      in M7's integration-test extension (4-peer scenario).
- [x] M4: `cmd/nous/brain_join.go`. Filter accepts three markers:
      description prefix `nous-brain:` (new), description contains
      `gcrypt-encrypted brain` (legacy from scripts/new-brain.sh),
      or topic `nous-brain`. Comma-separated index multi-select
      (or 'all'). New `brain.PublishOwnPubkeyToRemote(ctx, cloneURL,
      login, armor)` does plain-git clone of keys branch (with
      orphan-create fallback for new brains), writes `<login>.asc`,
      pushes. Smoke-tested the "no pending invitations" path against
      live gh.
- [x] M5: Updated `scripts/new-brain.sh` (the source for both
      `make new-brain` and `nous brain new` paths since the latter
      delegates to the former): description now `nous-brain: <name>
      (gcrypt-encrypted)` and topic `nous-brain` is set via a
      separate `gh api PUT repos/.../topics` call. Backward compat
      preserved — the legacy `gcrypt-encrypted brain` description
      still matches `nous brain join`'s filter for any brain
      created before this commit.
- [x] M6: Verify ceremony now persists to `.brain/verified.yaml`;
      `AutoAdmitFromKeysBranch` pauses for any login whose
      keys-branch fingerprint differs from the verified entry.
      Specifically:
      - `lib/brain/verified.go`: Verified type (login → VerifiedEntry),
        Read/Write with sorted-key stable output, fingerprint case
        normalization. 4 unit tests cover round-trip, missing-file,
        case normalization, and deterministic output.
      - `lib/brain/autoadmit.go`: AutoAdmitFromKeysBranch returns
        `([]AdmittedRecipient, []DriftEvent, error)`. Drift detected
        when `verified.yaml` pins a fingerprint that differs from
        the keys-branch fingerprint for the same login.
      - `lib/brainsync/watch.go`: autoAdmitBrain logs drift loudly
        every tick (regardless of verbose) — MITM safety floor.
      - `cmd/nous/brain_recipient.go`: existing verify CLI now
        persists on successful match — looks up github-login via
        `brain.LoginForFingerprint(keys-branch scan)`, writes the
        VerifiedEntry, commits + pushes. Legacy `<FP>.asc`
        admissions get a soft notice (no login → can't persist).
      - `lib/brain/integration_test.go::TestEndToEnd_DriftDetection`:
        operator persists verify → MITM substitutes peerC.asc with
        peerD's pubkey → auto-admit refuses, returns DriftEvent →
        manifest unchanged → operator re-verifies → next auto-admit
        accepts the new key. Full safety-floor exercise.
      Out of scope (deferred): a `nous brain recipient verify`
      TUI listing all unverified keys for batch verification. The
      current single-FP CLI is the data-plane primitive; the TUI
      wrap-around is a separate ergonomics issue.
- [x] M7: Integration test extension landed in two new tests in
      `lib/brain/integration_test.go`:
      - `TestEndToEnd_GitHubMediatedOnboarding`: full flow with a
        real bare-repo remote — operator provisions, peerC joins
        via `PublishOwnPubkeyToRemote`, operator's `ImportAllPubkeys`
        + `AutoAdmitFromKeysBranch` + `AddCommitPush` admits peerC,
        peerC clones via gcrypt and reads the manifest with both
        fingerprints. Also exercises idempotence (re-running
        auto-admit yields no new admissions) and the
        `looksLikeFingerprint` discriminator (operator's legacy
        `<FP>.asc` not re-admitted).
      - `TestPublishOwnPubkeyToRemote_OrphanCreate`: the brand-new-
        brain case where the joiner runs against an empty bare
        repo — orphan-checkout path creates the keys branch.
      Discovery filter fix (single-recipient brain with gcrypt
      remote is watched) covered in the unit test
      `TestFindSharedBrains_SingleRecipientWithGcryptRemote`. Push
      wrapper now tolerates non-brain repos (regression fix for
      three pre-existing brainsync tests that broke under #24).
      Drift-detection (M6 scope) tested separately when M6 lands.
- [x] M8: `atlas/nous/recipient-onboarding.md` — documents the
      trust model (GH-collaborator = recipient), the WhatsApp
      analogy (admission convenient by default; verify opt-in),
      both command flows (invite + join), auto-admit mechanism,
      drift detection as the safety floor, what's explicitly NOT
      in this flow (offline sneakernet, GPG keyservers, WoT
      signatures), and the file map. Sized to match sibling
      atlas docs (e2e-integration-testing.md as the template).

## Out of scope

- Revoking recipients via `nous brain recipient revoke` — already
  exists; not changed here.
- Cross-brain identity (one GPG key, multiple brains) — covered
  implicitly; the same `<login>.asc` lives under each brain's keys
  branch independently.
- Audit log of who-admitted-whom — `auto-admit` commit messages
  + the verified.yaml history (git blame) cover it.
- Group permissions (admin vs member). All recipients are equal for
  decryption; GitHub's existing collaborator permissions handle
  who-can-invite-others.

## Log
