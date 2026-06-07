package gh

import (
	"errors"
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

	t.Run("auth_login_resolves_each_identity", func(t *testing.T) {
		w := newWorld(t)
		// operator's token (the /user bearer lookup) resolves to the owner login.
		if got, err := w.operator.AuthLogin(); err != nil || !strings.EqualFold(got, w.owner) {
			t.Fatalf("operator AuthLogin = (%q, %v), want %q", got, err, w.owner)
		}
		if got, err := w.invitee.AuthLogin(); err != nil || !strings.EqualFold(got, w.inviteeLogin) {
			t.Fatalf("invitee AuthLogin = (%q, %v), want %q", got, err, w.inviteeLogin)
		}
	})

	t.Run("user_exists_distinguishes_visible_and_missing", func(t *testing.T) {
		w := newWorld(t)
		// Use the OWNER as the known-visible login (a repo owner is necessarily
		// public). Do NOT use the invitee: a secondary/new account can be
		// invitable yet NOT visible via /users/<login> (the nous#25 lag) — the
		// 2026-06-05 grounding confirmed yingtest42 is exactly that case, which
		// is the very asymmetry the fake's shadow-flag models.
		if err := w.operator.UserExists(w.owner); err != nil {
			t.Fatalf("UserExists(%q) = %v, want nil (owner is visible)", w.owner, err)
		}
		// a clearly-nonexistent login 404s → ErrUserNotVisible
		if err := w.operator.UserExists(missingLogin); !errors.Is(err, ErrUserNotVisible) {
			t.Fatalf("UserExists(%q) = %v, want ErrUserNotVisible", missingLogin, err)
		}
	})

	t.Run("collaborator_permission_owner_admin_noncollaborator_none", func(t *testing.T) {
		w := newWorld(t)
		// the owner's own permission is implicitly "admin" (GitHub returns it)
		if perm, err := w.operator.CollaboratorPermission(w.owner, w.repo, w.owner); err != nil || perm != "admin" {
			t.Fatalf("owner CollaboratorPermission = (%q, %v), want (admin, nil)", perm, err)
		}
		// a non-collaborator on a visible repo → "none" (real GitHub returns 200
		// {"permission":"none"}, grounded #42 — NOT "" / 404)
		if perm, err := w.operator.CollaboratorPermission(w.owner, w.repo, w.inviteeLogin); err != nil || perm != "none" {
			t.Fatalf("non-collaborator CollaboratorPermission = (%q, %v), want (none, nil)", perm, err)
		}
	})

	t.Run("user_repos_lists_the_repo", func(t *testing.T) {
		w := newWorld(t)
		repos, err := w.operator.UserRepos()
		if err != nil {
			t.Fatalf("UserRepos: %v", err)
		}
		want := w.owner + "/" + w.repo
		found := false
		for _, r := range repos {
			if strings.EqualFold(r.FullName, want) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("UserRepos did not include %q (got %d repos)", want, len(repos))
		}
	})

	t.Run("decline_removes_pending_invitation", func(t *testing.T) {
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
			t.Fatalf("no pending invitation to decline")
		}
		if err := w.invitee.DeclineInvitation(inv.ID); err != nil {
			t.Fatalf("DeclineInvitation: %v", err)
		}
		after, _ := w.invitee.PendingInvitations()
		if _, still := findInvite(after, w.owner, w.repo); still {
			t.Fatalf("invitation still pending after decline")
		}
	})

	t.Run("remove_collaborator_revokes_access", func(t *testing.T) {
		w := newWorld(t)
		if err := w.operator.AddCollaborator(w.owner, w.repo, w.inviteeLogin, "push"); err != nil {
			t.Fatalf("AddCollaborator: %v", err)
		}
		invs, _ := w.invitee.PendingInvitations()
		inv, ok := findInvite(invs, w.owner, w.repo)
		if !ok {
			t.Fatalf("no pending invitation to accept")
		}
		if err := w.invitee.AcceptInvitation(inv.ID); err != nil {
			t.Fatalf("AcceptInvitation: %v", err)
		}
		cols, _ := w.operator.ListCollaborators(w.owner, w.repo)
		if !containsFold(cols, w.inviteeLogin) {
			t.Fatalf("precondition: invitee should be a collaborator after accept")
		}
		if err := w.operator.RemoveCollaborator(w.owner, w.repo, w.inviteeLogin); err != nil {
			t.Fatalf("RemoveCollaborator: %v", err)
		}
		cols, _ = w.operator.ListCollaborators(w.owner, w.repo)
		if containsFold(cols, w.inviteeLogin) {
			t.Fatalf("invitee still a collaborator after RemoveCollaborator; got %v", cols)
		}
	})
}

// missingLogin is a deliberately-nonexistent GitHub login for the UserExists
// 404 path. Static (not random) so the real backend's lookup is reproducible.
const missingLogin = "nous42-conformance-no-such-user-zzq"

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
