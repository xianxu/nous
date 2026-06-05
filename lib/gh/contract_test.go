package gh

import (
	"slices"
	"strings"
	"testing"
)

// The contract suite is the GROUNDING mechanism (spec §grounding). One set
// of port-behavior invariants runs against BOTH backends:
//   - the fake — always, here (TestContract_Fake), fast, in CI;
//   - the real `gh` — build-tagged `conformance` (contract_real_test.go),
//     run manually/~monthly to CERTIFY the fake hasn't drifted from GitHub.
//
// A library-level fake structurally cannot see below-the-seam endpoint bugs
// (bug 1); this contract's real-backend run is, with real_test.go's endpoint
// assertions, the defense for that class. Assertions here are expressed
// PURELY in Client terms — no adapter-specific seeding leaks in (seeding is
// the factory's job), so the same body certifies both backends.

// contractWorld is what each backend provisions: an operator client and an
// invitee client over the SAME underlying world, plus the repo coordinates
// and the invitee's login. The fake builds this in-memory (shared fakeState
// via AsUser); the real backend builds it from two GH_TOKENs + a repo.
type contractWorld struct {
	operator     Client
	invitee      Client
	owner        string
	repo         string
	inviteeLogin string
}

// runContract asserts the invariants that must hold for ANY Client. newWorld
// is called fresh per subtest for isolation.
func runContract(t *testing.T, newWorld func(t *testing.T) contractWorld) {
	t.Run("invite_then_pending_then_accept_then_collaborator", func(t *testing.T) {
		w := newWorld(t)
		if err := w.operator.AddCollaborator(w.owner, w.repo, w.inviteeLogin, "push"); err != nil {
			t.Fatalf("AddCollaborator: %v", err)
		}
		invs, err := w.invitee.PendingInvitations()
		if err != nil {
			t.Fatalf("PendingInvitations: %v", err)
		}
		inv, ok := findInvite(invs, w.owner, w.repo)
		if !ok {
			t.Fatalf("invitee has no pending invitation for %s/%s; got %+v", w.owner, w.repo, invs)
		}
		if err := w.invitee.AcceptInvitation(inv.ID); err != nil {
			t.Fatalf("AcceptInvitation: %v", err)
		}
		cols, err := w.operator.ListCollaborators(w.owner, w.repo)
		if err != nil {
			t.Fatalf("ListCollaborators: %v", err)
		}
		if !containsFold(cols, w.inviteeLogin) {
			t.Fatalf("invitee %q not a collaborator after accept; got %v", w.inviteeLogin, cols)
		}
	})

	t.Run("invitation_repo_omits_ssh_url", func(t *testing.T) {
		w := newWorld(t)
		if err := w.operator.AddCollaborator(w.owner, w.repo, w.inviteeLogin, "push"); err != nil {
			t.Fatalf("AddCollaborator: %v", err)
		}
		invs, err := w.invitee.PendingInvitations()
		if err != nil {
			t.Fatalf("PendingInvitations: %v", err)
		}
		inv, ok := findInvite(invs, w.owner, w.repo)
		if !ok {
			t.Fatalf("no pending invitation to inspect")
		}
		// MinimalRepository invariant: /user/repository_invitations omits
		// ssh_url. nous#26 bug 2 — consumers must fall back to CloneURL.
		if inv.Repository.SSHURL != "" {
			t.Fatalf("invitation must carry empty ssh_url (MinimalRepository); got %q", inv.Repository.SSHURL)
		}
		if got := w.operator.CloneURL(inv.Repository.FullName, inv.Repository.SSHURL); got == "" {
			t.Fatalf("CloneURL must fabricate a non-empty URL from full_name when ssh_url is empty")
		}
	})

	// NOTE: this grounds only the *pending* invitation no-op. The spec also
	// names the *expired* invitation case (the actual nous#41 #11 surface),
	// but neither the fake (no expiry field) nor real `gh` (can't mint an
	// expired invite in-test) can exercise it — expiry is the ungrounded leg.
	t.Run("add_collaborator_noops_against_existing_invitation", func(t *testing.T) {
		w := newWorld(t)
		if err := w.operator.AddCollaborator(w.owner, w.repo, w.inviteeLogin, "push"); err != nil {
			t.Fatalf("first AddCollaborator: %v", err)
		}
		if err := w.operator.AddCollaborator(w.owner, w.repo, w.inviteeLogin, "push"); err != nil {
			t.Fatalf("second AddCollaborator should no-op, not error: %v", err)
		}
		pend, err := w.operator.RepoPendingInvitations(w.owner, w.repo)
		if err != nil {
			t.Fatalf("RepoPendingInvitations: %v", err)
		}
		n := 0
		for _, p := range pend {
			if strings.EqualFold(p.Invitee.Login, w.inviteeLogin) {
				n++
			}
		}
		if n != 1 {
			t.Fatalf("want exactly 1 pending invite after double-add (PUT no-op), got %d", n)
		}
	})

	t.Run("reinvite_replaces_stale_and_resends", func(t *testing.T) {
		w := newWorld(t)
		if err := w.operator.AddCollaborator(w.owner, w.repo, w.inviteeLogin, "push"); err != nil {
			t.Fatalf("seed invite: %v", err)
		}
		res, err := w.operator.InviteCollaborator(w.owner, w.repo, w.inviteeLogin, "push")
		if err != nil {
			t.Fatalf("InviteCollaborator: %v", err)
		}
		if !res.ReplacedStale {
			t.Fatalf("expected ReplacedStale=true when a pending invite existed")
		}
	})
}

func findInvite(invs []Invitation, owner, repo string) (Invitation, bool) {
	want := owner + "/" + repo
	for _, iv := range invs {
		if strings.EqualFold(iv.Repository.FullName, want) {
			return iv, true
		}
	}
	return Invitation{}, false
}

func containsFold(s []string, v string) bool {
	return slices.ContainsFunc(s, func(x string) bool { return strings.EqualFold(x, v) })
}

// TestContract_Fake runs the contract against the in-memory fake — always on.
// Operator and invitee are two clients over one fakeState (via AsUser), the
// in-memory analog of the real backend's two GH_TOKENs.
func TestContract_Fake(t *testing.T) {
	runContract(t, func(t *testing.T) contractWorld {
		op := NewFake(Conf{CloneURLBase: "file://" + t.TempDir() + "/"}).(*Fake)
		op.AddUser("op")
		op.AddUser("ying")
		op.SwitchUser("op")
		op.CreateRepo("op", "brain", true)
		return contractWorld{
			operator:     op,
			invitee:      op.AsUser("ying"),
			owner:        "op",
			repo:         "brain",
			inviteeLogin: "ying",
		}
	})
}
