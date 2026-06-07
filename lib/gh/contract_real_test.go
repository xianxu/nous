//go:build conformance

// This is the GROUNDING run (spec §grounding). It runs the SAME contract
// suite (runContract, contract_test.go) against the REAL `gh` CLI to certify
// the fake hasn't drifted from GitHub. It is build-tagged `conformance` so it
// never runs in normal CI — invoke it manually, ~monthly or on suspected drift.
//
// ZERO-CONFIG INVOCATION (defaults baked in — see resolveConformanceConfig):
//
//	go test -tags conformance ./lib/gh/ -run Contract_Real -v
//
// With no env set, it resolves:
//   - operator token  ← `gh auth token` (your existing gh login)
//   - invitee token   ← macOS Keychain: `security find-generic-password
//                        -s nous-conformance-invitee -w`
//   - owner login     ← derived from the operator token (AuthLogin)
//   - invitee login   ← derived from the invitee token (AuthLogin)
//   - repo            ← "shim-conformance" (under the owner)
//
// ONE-TIME SETUP (stores the invitee token in Keychain — never committed):
//
//	security add-generic-password -s nous-conformance-invitee \
//	  -a <invitee-login> -w <invitee-PAT-classic-repo-scope>
//
// That's the only setup — the fixture repo is auto-provisioned (see below).
//
// Every value is OVERRIDABLE by env (GH_TOKEN_OP, GH_TOKEN_INVITEE,
// GH_TEST_OWNER, GH_TEST_REPO, GH_TEST_INVITEE_LOGIN) — that's the path CI uses
// (encrypted Actions secrets). Tokens NEVER live in the repo: only `gh auth`,
// Keychain, or CI secrets. If a required value can't be resolved, the test
// SKIPS (it never fails for missing creds).
//
// The fixture repo is ENSURE-CREATED (private) if missing — `gh repo create`,
// idempotent. We create but never delete (the operator token has `repo`, not
// `delete_repo` scope), so it persists as an empty private fixture reused next
// run; it holds no real content. The suite is otherwise non-destructive beyond
// invitations/collaborators, which newWorld resets before each subtest and
// t.Cleanup clears after. If a subtest FAILS, the fake has drifted: fix fake.go
// (not the test) and re-certify.
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

// ghAuthToken returns the active `gh` login's token (the operator's own auth),
// or "" if gh isn't installed/authed.
func ghAuthToken() string {
	out, err := exec.Command("gh", "auth", "token").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

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
// baked defaults (gh auth + Keychain + token-derived logins). Returns ok=false
// when a required value can't be resolved, so the caller SKIPS rather than
// fails. Tokens are never read from the repo.
func resolveConformanceConfig() (opTok, inviteeTok, owner, repo, inviteeLogin string, ok bool) {
	opTok = os.Getenv("GH_TOKEN_OP")
	if opTok == "" {
		opTok = ghAuthToken()
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

// ensureConformanceRepo provisions the private fixture repo if it doesn't
// exist (zero-config). Execs `gh repo create` directly with the operator token
// rather than via the Client — repo creation isn't part of nous's used surface,
// so it stays off the port. Idempotent: an "already exists" error is fine.
func ensureConformanceRepo(t *testing.T, opTok, owner, repo string) {
	cmd := exec.Command("gh", "repo", "create", owner+"/"+repo, "--private")
	cmd.Env = append(os.Environ(), "GH_TOKEN="+opTok)
	out, err := cmd.CombinedOutput()
	if err != nil && !strings.Contains(strings.ToLower(string(out)), "already exists") {
		t.Fatalf("ensure fixture repo %s/%s: %v\n%s", owner, repo, err, out)
	}
}

func TestContract_Real(t *testing.T) {
	opTok, inviteeTok, owner, repo, inviteeLogin, ok := resolveConformanceConfig()
	if !ok {
		t.Skip("conformance creds unresolved — set the invitee token in Keychain " +
			"(security add-generic-password -s nous-conformance-invitee -a <login> -w <PAT>) " +
			"and `gh auth login`, or pass GH_TOKEN_OP/GH_TOKEN_INVITEE/GH_TEST_* env")
	}

	ensureConformanceRepo(t, opTok, owner, repo)

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
