package gh

import (
	"errors"
	"os/exec"
	"slices"
	"testing"
)

// newSeededFake returns an operator-bound fake with a file:// base under
// t.TempDir() (so the data plane works and Go cleans it up), plus two
// users and one repo owned by "op".
func newSeededFake(t *testing.T) *fakeClient {
	t.Helper()
	c := NewFake(Conf{CloneURLBase: "file://" + t.TempDir() + "/"}).(*fakeClient)
	c.AddUser("op")
	c.AddUser("ying")
	c.SwitchUser("op")
	c.CreateRepo("op", "brain", true)
	return c
}

func TestFake_InviteAcceptFlow(t *testing.T) {
	c := newSeededFake(t)

	if err := c.AddCollaborator("op", "brain", "ying", "push"); err != nil {
		t.Fatalf("AddCollaborator: %v", err)
	}

	ying := c.AsUser("ying")
	invs, err := ying.PendingInvitations()
	if err != nil {
		t.Fatalf("PendingInvitations: %v", err)
	}
	if len(invs) != 1 {
		t.Fatalf("want 1 pending invite, got %d", len(invs))
	}
	// PECULIARITY: /user/repository_invitations is a MinimalRepository —
	// ssh_url is empty (nous#26 bug 2).
	if invs[0].Repository.SSHURL != "" {
		t.Fatalf("fake must return empty ssh_url (MinimalRepository), got %q", invs[0].Repository.SSHURL)
	}
	if got := invs[0].Repository.FullName; got != "op/brain" {
		t.Fatalf("invitation FullName = %q, want op/brain", got)
	}

	if err := ying.AcceptInvitation(invs[0].ID); err != nil {
		t.Fatalf("AcceptInvitation: %v", err)
	}

	cols, err := c.ListCollaborators("op", "brain")
	if err != nil {
		t.Fatalf("ListCollaborators: %v", err)
	}
	if !slices.Contains(cols, "ying") {
		t.Fatalf("ying should be a collaborator after accept; got %v", cols)
	}
	// And the invitation is no longer pending.
	if pend, _ := ying.PendingInvitations(); len(pend) != 0 {
		t.Fatalf("invitation should be consumed after accept; got %d pending", len(pend))
	}
}

func TestFake_AddCollaborator_NoopsAgainstExistingInvitation(t *testing.T) {
	c := newSeededFake(t)
	if err := c.AddCollaborator("op", "brain", "ying", "push"); err != nil {
		t.Fatal(err)
	}
	if err := c.AddCollaborator("op", "brain", "ying", "push"); err != nil {
		t.Fatal(err) // second add must no-op, not error
	}
	pend, _ := c.RepoPendingInvitations("op", "brain")
	n := 0
	for _, p := range pend {
		if p.Invitee.Login == "ying" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("want exactly 1 pending invite after double-add (no-op), got %d", n)
	}
}

func TestFake_InviteCollaborator_ReplacesStaleAndResends(t *testing.T) {
	c := newSeededFake(t)
	// seed a stale pending invite
	if err := c.AddCollaborator("op", "brain", "ying", "push"); err != nil {
		t.Fatal(err)
	}
	res, err := c.InviteCollaborator("op", "brain", "ying", "push")
	if err != nil {
		t.Fatalf("InviteCollaborator: %v", err)
	}
	if !res.ReplacedStale {
		t.Fatalf("expected ReplacedStale=true (a pending invite existed)")
	}
	// still exactly one pending invite (the fresh one)
	pend, _ := c.RepoPendingInvitations("op", "brain")
	if len(pend) != 1 {
		t.Fatalf("want 1 pending invite after re-invite, got %d", len(pend))
	}
}

func TestFake_InviteCollaborator_HardErrorsOnListFailure(t *testing.T) {
	c := newSeededFake(t)
	c.FailListInvitations(true) // nous#41 #11: a list failure must NOT be swallowed
	_, err := c.InviteCollaborator("op", "brain", "ying", "push")
	if err == nil {
		t.Fatalf("expected a hard error when listing invitations fails (PUT would silently no-op)")
	}
}

func TestFake_UserExists_ShadowFlag(t *testing.T) {
	c := newSeededFake(t)
	if err := c.UserExists("ying"); err != nil {
		t.Fatalf("ying should be visible before shadowing: %v", err)
	}
	c.ShadowUser("ying", true)
	if err := c.UserExists("ying"); !errors.Is(err, ErrUserNotVisible) {
		t.Fatalf("shadowed user should be ErrUserNotVisible, got %v", err)
	}
	// ...but the bearer token still authenticates (the brand-new-account asymmetry)
	ying := c.AsUser("ying")
	if login, err := ying.AuthLogin(); err != nil || login != "ying" {
		t.Fatalf("shadowed user's token must still resolve: login=%q err=%v", login, err)
	}
}

func TestFake_UserExists_Unknown(t *testing.T) {
	c := newSeededFake(t)
	if err := c.UserExists("nobody"); !errors.Is(err, ErrUserNotVisible) {
		t.Fatalf("unknown user should be ErrUserNotVisible, got %v", err)
	}
}

func TestFake_CloneURL_ResolvesToTmpdirBareRepo(t *testing.T) {
	c := newSeededFake(t)
	url := c.CloneURL("op/brain", "") // empty ssh_url → fabricated from base
	if url == "" {
		t.Fatal("CloneURL returned empty")
	}
	// a real git clone of the seeded bare repo must succeed
	dest := t.TempDir() + "/clone"
	out, err := exec.Command("git", "clone", url, dest).CombinedOutput()
	if err != nil {
		t.Fatalf("git clone %s failed: %v\n%s", url, err, out)
	}
}

func TestFake_CollaboratorPermission(t *testing.T) {
	c := newSeededFake(t)
	// owner is implicitly admin
	if perm, _ := c.CollaboratorPermission("op", "brain", "op"); perm != "admin" {
		t.Fatalf("owner perm = %q, want admin", perm)
	}
	// non-collaborator → ""
	if perm, _ := c.CollaboratorPermission("op", "brain", "ying"); perm != "" {
		t.Fatalf("non-collaborator perm = %q, want empty", perm)
	}
	// unknown repo → "" (no error, matches real 404 handling)
	if perm, err := c.CollaboratorPermission("op", "ghost", "op"); perm != "" || err != nil {
		t.Fatalf("unknown repo = (%q,%v), want (\"\",nil)", perm, err)
	}
}

func TestFake_DeleteRepoInvitation_And_Decline(t *testing.T) {
	c := newSeededFake(t)
	_ = c.AddCollaborator("op", "brain", "ying", "push")
	pend, _ := c.RepoPendingInvitations("op", "brain")
	if len(pend) != 1 {
		t.Fatalf("setup: want 1 pending, got %d", len(pend))
	}
	if err := c.DeleteRepoInvitation("op", "brain", pend[0].ID); err != nil {
		t.Fatal(err)
	}
	if p, _ := c.RepoPendingInvitations("op", "brain"); len(p) != 0 {
		t.Fatalf("invite should be deleted, got %d", len(p))
	}

	// Decline path (invitee-side)
	_ = c.AddCollaborator("op", "brain", "ying", "push")
	ying := c.AsUser("ying")
	invs, _ := ying.PendingInvitations()
	if err := ying.DeclineInvitation(invs[0].ID); err != nil {
		t.Fatal(err)
	}
	if p, _ := ying.PendingInvitations(); len(p) != 0 {
		t.Fatalf("invite should be declined, got %d", len(p))
	}
}

func TestFake_RemoveCollaborator(t *testing.T) {
	c := newSeededFake(t)
	_ = c.AddCollaborator("op", "brain", "ying", "push")
	ying := c.AsUser("ying")
	_ = ying.AcceptInvitation(mustFirstInviteID(t, ying))
	if cols, _ := c.ListCollaborators("op", "brain"); !slices.Contains(cols, "ying") {
		t.Fatal("setup: ying should be a collaborator")
	}
	if err := c.RemoveCollaborator("op", "brain", "ying"); err != nil {
		t.Fatal(err)
	}
	if cols, _ := c.ListCollaborators("op", "brain"); slices.Contains(cols, "ying") {
		t.Fatalf("ying should be removed, got %v", cols)
	}
}

func TestFake_UserRepos(t *testing.T) {
	c := newSeededFake(t)
	// op owns brain → appears
	op := c.AsUser("op")
	repos, _ := op.UserRepos()
	if len(repos) != 1 || repos[0].FullName != "op/brain" {
		t.Fatalf("op UserRepos = %v, want [op/brain]", repos)
	}
	// ying has no access yet → empty
	ying := c.AsUser("ying")
	if r, _ := ying.UserRepos(); len(r) != 0 {
		t.Fatalf("ying UserRepos = %v, want empty", r)
	}
}

func mustFirstInviteID(t *testing.T, c *fakeClient) int {
	t.Helper()
	invs, err := c.PendingInvitations()
	if err != nil || len(invs) == 0 {
		t.Fatalf("expected a pending invitation: %v", err)
	}
	return invs[0].ID
}
