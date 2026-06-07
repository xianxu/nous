---
id: 000043
status: working
deps: []
github_issue:
created: 2026-06-06
updated: 2026-06-06
estimate_hours: 0.5
---

# conformance: move gh fixture repo to the test account (ephemeral, two throwaway accounts)

## Problem

nous#42's gh conformance run used the operator's own `gh auth` account (xianxu) as
operator and a throwaway (yingtest42) as invitee, with a **standing** fixture repo
on xianxu (`xianxu/shim-conformance`). Two issues: (1) it puts a test repo on the
real account, and (2) the operator token lacked `delete_repo`, so the fixture
couldn't be ephemeral. Operator wants the **real account fully off the test path**.

## Spec

Use **two throwaway accounts**, neither being the developer's real account:
- **operator / repo-owner = yingtest42** — token in Keychain `nous-conformance-operator`;
  has `repo` + `delete_repo` (granted).
- **invitee / collaborator = emmatest42** — token in Keychain `nous-conformance-invitee`;
  classic PAT, `repo` scope (to accept the invitation).
- `gh auth` (xianxu) is **not used** by the conformance run anymore.

Fixture repo is **ephemeral**: `<operator>/shim-conformance` is created at the
start of the run and **deleted** in cleanup (operator's `delete_repo`). Zero
standing test artifacts anywhere; nothing touches the real account.

Resolution order unchanged in spirit (env override → baked default), only the
sources flip:
- `GH_TOKEN_OP`   → env, else Keychain `nous-conformance-operator`
- `GH_TOKEN_INVITEE` → env, else Keychain `nous-conformance-invitee`
- owner / invitee logins derived from the tokens (AuthLogin); repo defaults to
  `shim-conformance`. All env-overridable (CI path).

## Done when

- Conformance run resolves both tokens from Keychain (operator + invitee), uses
  neither `gh auth` nor xianxu, creates the ephemeral fixture on yingtest42, runs
  all 10 invariants, and deletes the fixture on cleanup.
- Verified end to end (`go test -tags conformance ./lib/gh/ -run Contract_Real`)
  once emmatest42's token is in Keychain. Header docs updated.

## Plan

- [ ] Rework `resolveConformanceConfig`: operator ← Keychain `nous-conformance-operator`,
      invitee ← Keychain `nous-conformance-invitee` (drop `gh auth` default).
- [ ] Ephemeral repo: keep ensure-create; add delete-on-cleanup (operator `delete_repo`).
- [ ] Update header runbook (two-account model, ephemeral, Keychain service names).
- [ ] Keychain: migrate yingtest42 → `nous-conformance-operator`; emmatest42 → `nous-conformance-invitee` (operator-supplied).
- [ ] Verify: zero-config conformance green; fixture created+deleted on yingtest42.

## Log

### 2026-06-06

Follow-up to nous#42 (merged). Operator: "use emmatest42 as the other test
account, yingtest42 as operator, emmatest42 as collaborator — my main account is
fully off this test path." yingtest42 was granted `delete_repo` to enable the
ephemeral fixture.
