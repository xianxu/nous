//go:build conformance

// This is the GROUNDING run (spec §grounding). It runs the SAME contract
// suite (runContract, contract_test.go) against the REAL `gh` CLI to certify
// the fake hasn't drifted from GitHub. It is build-tagged `conformance` so it
// never runs in normal CI — invoke it manually, ~monthly or on suspected drift.
//
// TWO THROWAWAY ACCOUNTS — the developer's real account is NOT on this path:
//   - operator / repo-owner = a disposable account with `repo` + `delete_repo`
//   - invitee / collaborator = a second disposable account with `repo`
// Both tokens come from Keychain; `gh auth` is not used.
//
// ZERO-CONFIG INVOCATION:
//
//	go test -tags conformance ./lib/gh/ -run Contract_Real -v
//
// With no env set, it resolves:
//   - operator token ← Keychain `nous-conformance-operator`
//   - invitee token  ← Keychain `nous-conformance-invitee`
//   - owner / invitee logins ← derived from the tokens (AuthLogin)
//   - repo ← "shim-conformance" (created under the operator)
//
// ONE-TIME SETUP (tokens stored in Keychain — never committed):
//
//	security add-generic-password -s nous-conformance-operator \
//	  -a <operator-login> -w <operator-PAT: classic, repo + delete_repo>
//	security add-generic-password -s nous-conformance-invitee \
//	  -a <invitee-login>  -w <invitee-PAT: classic, repo>
//
// Every value is OVERRIDABLE by env (GH_TOKEN_OP, GH_TOKEN_INVITEE,
// GH_TEST_OWNER, GH_TEST_REPO, GH_TEST_INVITEE_LOGIN) — the CI path (encrypted
// Actions secrets). Tokens NEVER live in the repo: only Keychain or CI secrets.
// If a required value can't be resolved, the test SKIPS (never fails for creds).
//
// EPHEMERAL FIXTURE: `<operator>/shim-conformance` is created (private) at the
// start of the run and DELETED on cleanup (the operator token's `delete_repo`).
// Zero standing test artifacts; nothing touches the real account. The suite is
// otherwise non-destructive beyond invitations/collaborators, which newWorld
// resets before each subtest and t.Cleanup clears after. If a subtest FAILS, the
// fake has drifted: fix fake.go (not the test) and re-certify.
//
// Eventual consistency: GitHub's post-acceptance endpoints can lag (the spec
// notes /user/repos by tens of seconds). ListCollaborators is usually prompt,
// but if the invite→accept→ListCollaborators subtest goes red on the real
// backend, suspect propagation lag (add a short retry) before concluding the
// fake drifted.

package gh

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
)

// keychainSecret reads a generic password from the macOS Keychain by service
// name, or "" (other OSes, or not present).
func keychainSecret(service string) string {
	if runtime.GOOS != "darwin" {
		return ""
	}
	out, err := exec.Command("security", "find-generic-password", "-s", service, "-w").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// resolveConformanceConfig fills the conformance inputs from env (override) →
// Keychain defaults. Both identities are disposable accounts (operator +
// invitee); the real `gh auth` account is intentionally not used. Returns
// ok=false when a required value can't be resolved, so the caller SKIPS rather
// than fails. Tokens are never read from the repo.
func resolveConformanceConfig() (opTok, inviteeTok, owner, repo, inviteeLogin string, ok bool) {
	opTok = os.Getenv("GH_TOKEN_OP")
	if opTok == "" {
		opTok = keychainSecret("nous-conformance-operator")
	}
	inviteeTok = os.Getenv("GH_TOKEN_INVITEE")
	if inviteeTok == "" {
		inviteeTok = keychainSecret("nous-conformance-invitee")
	}
	if opTok == "" || inviteeTok == "" {
		return "", "", "", "", "", false
	}

	repo = os.Getenv("GH_TEST_REPO")
	if repo == "" {
		repo = "shim-conformance"
	}
	// Derive the logins from the tokens (no usernames hardcoded). AuthLogin hits
	// /user, which also implicitly grounds it.
	owner = os.Getenv("GH_TEST_OWNER")
	if owner == "" {
		owner, _ = New(Conf{Token: opTok}).AuthLogin()
	}
	inviteeLogin = os.Getenv("GH_TEST_INVITEE_LOGIN")
	if inviteeLogin == "" {
		inviteeLogin, _ = New(Conf{Token: inviteeTok}).AuthLogin()
	}
	ok = owner != "" && inviteeLogin != "" && repo != ""
	return opTok, inviteeTok, owner, repo, inviteeLogin, ok
}

// ensureConformanceRepo creates the private fixture repo (operator-owned).
// Execs `gh repo create` directly with the operator token rather than via the
// Client — repo lifecycle isn't part of nous's used surface, so it stays off the
// port. Idempotent: an "already exists" error (e.g. a prior run's delete failed)
// is fine.
func ensureConformanceRepo(t *testing.T, opTok, owner, repo string) {
	// Bare name (not owner/repo): `gh repo create` then makes it under the authed
	// user — which IS the operator. Passing owner/repo makes gh resolve the owner
	// via /users/<owner>, which 404s for a brand-new throwaway account still in
	// GitHub's visibility lag (the operator here is exactly such an account).
	cmd := exec.Command("gh", "repo", "create", repo, "--private")
	cmd.Env = append(os.Environ(), "GH_TOKEN="+opTok)
	out, err := cmd.CombinedOutput()
	if err != nil && !strings.Contains(strings.ToLower(string(out)), "already exists") {
		t.Fatalf("ensure fixture repo %s/%s: %v\n%s", owner, repo, err, out)
	}
}

// deleteConformanceRepo removes the ephemeral fixture (operator `delete_repo`).
// Best-effort: a failed delete must not fail an otherwise-green cert — it just
// leaves the repo for the next run's ensure-create to reuse — so we warn, not
// fatal.
func deleteConformanceRepo(t *testing.T, opTok, owner, repo string) {
	cmd := exec.Command("gh", "repo", "delete", owner+"/"+repo, "--yes")
	cmd.Env = append(os.Environ(), "GH_TOKEN="+opTok)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Logf("warning: could not delete ephemeral fixture %s/%s (leaving for next run): %v\n%s", owner, repo, err, out)
	}
}

func TestContract_Real(t *testing.T) {
	opTok, inviteeTok, owner, repo, inviteeLogin, ok := resolveConformanceConfig()
	if !ok {
		t.Skip("conformance creds unresolved — store both throwaway-account tokens in Keychain:\n" +
			"  security add-generic-password -s nous-conformance-operator -a <login> -w <PAT repo+delete_repo>\n" +
			"  security add-generic-password -s nous-conformance-invitee  -a <login> -w <PAT repo>\n" +
			"or pass GH_TOKEN_OP/GH_TOKEN_INVITEE/GH_TEST_* env")
	}

	ensureConformanceRepo(t, opTok, owner, repo)
	t.Cleanup(func() { deleteConformanceRepo(t, opTok, owner, repo) }) // ephemeral

	operator := New(Conf{Token: opTok})
	invitee := New(Conf{Token: inviteeTok})

	reset := func(t *testing.T) {
		// Clear any pending invitation for the invitee, then remove them
		// as a collaborator, so each subtest starts from a clean repo.
		if invs, err := operator.RepoPendingInvitations(owner, repo); err == nil {
			for _, iv := range invs {
				if iv.Invitee.Login == inviteeLogin {
					_ = operator.DeleteRepoInvitation(owner, repo, iv.ID)
				}
			}
		}
		// invitee may also have an unaccepted user-side invitation
		if invs, err := invitee.PendingInvitations(); err == nil {
			for _, iv := range invs {
				if iv.Repository.FullName == owner+"/"+repo {
					_ = invitee.DeclineInvitation(iv.ID)
				}
			}
		}
		_ = operator.RemoveCollaborator(owner, repo, inviteeLogin)
	}

	runContract(t, func(t *testing.T) contractWorld {
		reset(t)
		t.Cleanup(func() { reset(t) })
		return contractWorld{
			operator:     operator,
			invitee:      invitee,
			owner:        owner,
			repo:         repo,
			inviteeLogin: inviteeLogin,
		}
	})
}
