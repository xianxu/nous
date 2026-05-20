---
id: 000027
status: done
deps: [000026]
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

## Done when

### 1. nous brain new publishes operator's <login>.asc

- `cmd/nous/brain_new.go` (post-script publish loop) also publishes
  operator's own pubkey via a new `brain.PublishOwnPubkey(ctx,
  brainRoot, login, fp)` helper, in addition to (or instead of) the
  legacy `<FP>.asc` publish.
- The `<login>.asc` is the new-flow standard. If we keep `<FP>.asc`
  alongside it for back-compat, joiners with old code still see the
  operator's pubkey under either name (ImportAllPubkeys imports any
  `.asc`).
- Integration test extension: a peer joining a brain provisioned
  via the full flow finds `<operator-login>.asc` on the keys
  branch immediately.

### 2. nous brain clone surfaces missing-operator-pubkey clearly

- After `BootstrapPubkeys` returns, `cmd/nous/brain_clone.go`
  inspects the imported set. If only one pubkey was imported AND it
  matches the joiner's own fingerprint, that's the "only my key on
  keys branch" pattern — likely the operator hasn't published yet.
- Catch the `git clone gcrypt::` exit with a "No public key" or
  "Can't check signature" pattern in stderr, prepend a clear
  diagnostic + recovery hint before propagating the gcrypt error.
- No new flag needed; just better error.

### 3. Operator marker in `nous brain list`

- For each brain with a gcrypt:: remote pointing at github.com:
  resolve owner/repo via `brain.GitHubOwnerRepo`, call
  `gh.CollaboratorPermission(owner, repo, login)` (new helper),
  cache per session. Permission `admin` or `maintain`, OR `auth_login
  == owner` → operator.
- Mark with `*` prefix in the list. Non-github / non-operator
  brains show without prefix.
- Same predicate exported so the brain TUI (when added) can use it
  to gate the "invite" action.

## Spec

### `brain.PublishOwnPubkey(ctx, brainRoot, login, fp string)`

Thin wrapper around the existing filestore Put — same as
PublishPubkey but with `login` as the filename stem instead of the
fingerprint:

```go
func PublishOwnPubkey(ctx context.Context, brainRoot, login, fp string) error {
    armor, err := identity.Export(fp)
    if err != nil { return err }
    store, err := filestore.Open(brainRoot, keysBranch)
    if err != nil { return err }
    defer store.Close()
    return store.Put(ctx, login+pubkeyFilenameSuffix, []byte(armor))
}
```

Called from `cmd/nous/brain_new.go` after the existing
PublishPubkey loop:

```go
login, err := gh.AuthLogin()
if err == nil {
    if err := brain.PublishOwnPubkey(ctx, abs, login, ownFp); err != nil {
        fmt.Fprintf(out, "  warning: publish %s.asc: %v\n", login, err)
    }
}
```

(Soft failure — if gh.AuthLogin fails, fall back to the legacy
`<FP>.asc`-only state. Operator can manually run `nous brain join
<repo>` later to publish.)

### Clone-side detection

```go
// After BootstrapPubkeys:
out, err := exec.Command("git", "clone", gcryptURL, target).CombinedOutput()
if err != nil {
    if strings.Contains(string(out), "No public key") || strings.Contains(string(out), "Can't check signature") {
        return fmt.Errorf(
          "clone failed: keys branch missing the operator's pubkey.\n"+
          "  Ask the brain's operator to run `nous brain join <repo>` from their host,\n"+
          "  which publishes their <login>.asc to the keys branch. Then retry this clone.\n\n"+
          "gcrypt output:\n%s", out)
    }
    return fmt.Errorf("git clone: %w\n%s", err, out)
}
```

### Operator predicate + list marker

```go
// lib/gh/gh.go
// CollaboratorPermission returns the auth'd user's permission
// level on owner/repo: one of "admin", "maintain", "push",
// "triage", "pull", or "" (not a collaborator).
func CollaboratorPermission(owner, repo, login string) (string, error) {
    out, err := run("api",
        fmt.Sprintf("repos/%s/%s/collaborators/%s/permission", owner, repo, login),
        "--jq", ".permission")
    ...
}
```

```go
// cmd/nous/brain_misc.go::runBrainList
isOperator := false
if origin := readBrainOriginURL(b.Path); origin != "" {
    if owner, repo, err := brain.GitHubOwnerRepo(origin); err == nil {
        myLogin, _ := gh.AuthLogin()
        if myLogin == owner { isOperator = true } else {
            perm, _ := gh.CollaboratorPermission(owner, repo, myLogin)
            isOperator = perm == "admin" || perm == "maintain"
        }
    }
}
prefix := " "
if isOperator { prefix = "*" }
fmt.Fprintf(w, "%s %-22s ...", prefix, ...)
```

## Plan

- [x] M1: `brain.PublishOwnPubkey` in lib/brain/peerkeys.go.
      cmd/nous/brain_new.go now calls it after the legacy
      publish loop. Soft-fails if `gh.AuthLogin` returns error
      (prints a hint about running `nous brain join` later).
- [x] M2: Clone-side error detection in cmd/nous/brain_clone.go.
      Tees stderr through a buffer (kept live-streaming to user
      via io.MultiWriter), pattern-matches "No public key" /
      "Can't check signature", surfaces a recovery hint before
      propagating.
- [x] M3: `gh.CollaboratorPermission` in lib/gh/gh.go (returns
      ""+nil on 404, surfacing real errors only on infrastructure
      failures). `isOperator` predicate + `*` marker rendered
      in cmd/nous/brain_misc.go::runBrainList. Footer legend
      shows the meaning + current github login.
- [x] M4: `provisionBrain` test helper updated to dual-publish
      (legacy <FP>.asc + new <login>.asc); assertion in
      TestEndToEnd_GitHubMediatedOnboarding that both files
      exist on the keys branch after provision.
- [x] M5: Smoke-tested `nous brain list` on operator's real
      brains — three local brains all marked `*` because xianxu
      owns them. Footer legend renders. (Also noticed
      brain-family is now "shared 2 recipients" — the
      discovery-filter fix from nous#26 (6bab8bc) successfully
      auto-admitted ying.)

## Out of scope

- Operator marker in the bubbletea TUI (`nous brain` interactive
  mode). Same predicate applies, but the TUI rendering is a
  separate change. List CLI gets it first; TUI mirrors later.
- Automatic invite-on-marker — the marker only gates ergonomics,
  not behavior. Even non-operator collaborators can attempt
  invite; the gh API will reject. Surfacing the marker upfront
  saves them the round-trip.

## Log
