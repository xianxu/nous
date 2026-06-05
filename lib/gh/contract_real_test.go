//go:build conformance

// This is the GROUNDING run (spec §grounding). It runs the SAME contract
// suite (runContract, contract_test.go) against the REAL `gh` CLI to certify
// the fake hasn't drifted from GitHub. It is build-tagged `conformance` so it
// never runs in normal CI — invoke it manually, ~monthly or on suspected
// drift:
//
//	GH_TOKEN_OP=…  GH_TOKEN_INVITEE=…  \
//	GH_TEST_OWNER=<op-login>  GH_TEST_REPO=<disposable-private-repo>  \
//	GH_TEST_INVITEE_LOGIN=<invitee-login>  \
//	go test -tags conformance ./lib/gh/ -run Contract_Real -v
//
// The repo must already exist and be owned by GH_TOKEN_OP's account (we don't
// create/delete repos here — that's outside the Client surface). The suite is
// non-destructive beyond invitations/collaborators, which newWorld resets
// before each subtest and t.Cleanup clears after. If a subtest FAILS, the fake
// has drifted: fix fake.go (not the test) and re-certify.
//
// Eventual consistency: GitHub's post-acceptance endpoints can lag (the spec
// notes /user/repos by tens of seconds). ListCollaborators is usually prompt,
// but if the invite→accept→ListCollaborators subtest goes red on the real
// backend, suspect propagation lag (add a short retry) before concluding the
// fake drifted.

package gh

import (
	"os"
	"testing"
)

func TestContract_Real(t *testing.T) {
	opTok := os.Getenv("GH_TOKEN_OP")
	inviteeTok := os.Getenv("GH_TOKEN_INVITEE")
	owner := os.Getenv("GH_TEST_OWNER")
	repo := os.Getenv("GH_TEST_REPO")
	inviteeLogin := os.Getenv("GH_TEST_INVITEE_LOGIN")
	if opTok == "" || inviteeTok == "" || owner == "" || repo == "" || inviteeLogin == "" {
		t.Skip("conformance run needs GH_TOKEN_OP, GH_TOKEN_INVITEE, GH_TEST_OWNER, GH_TEST_REPO, GH_TEST_INVITEE_LOGIN")
	}

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
