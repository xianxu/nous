package gh

import (
	"os/exec"
	"testing"
)

// Named regression anchors for the historical control-plane bugs this shim
// exists to pin. They assert the full bug-specific chain through the fake;
// the broader behavior is also covered by the M2 fake unit tests and the M3
// contract suite, but these are the explicit "this bug cannot return" guards.
//
// Out of scope here (by design): bug 1 is below the seam (real_test.go +
// contract real-run); bugs 4 & 5 are data-plane/brain-logic, pinned by
// existing file:// tests in lib/brain & lib/brainsync (verified green in M4,
// not re-pinned here); bug 3 (accepted-but-unpublished recovery) is an
// end-to-end flow, pinned by the M5 onboarding simulation.

// TestRegression_Nous26Bug2_MinimalRepositoryClone pins nous#26 bug 2: the
// /user/repository_invitations response is a MinimalRepository with an EMPTY
// ssh_url, so a naive `git clone <ssh_url>` ran `git clone "" tmpdir` and
// failed. The fix is CloneURL fabricating from full_name. This drives the
// real invitation shape (not a hand-built struct) and clones the result.
func TestRegression_Nous26Bug2_MinimalRepositoryClone(t *testing.T) {
	c := NewFake(Conf{CloneURLBase: "file://" + t.TempDir() + "/"}).(*fakeClient)
	c.AddUser("op")
	c.AddUser("ying")
	c.SwitchUser("op")
	c.CreateRepo("op", "brain", true)
	if err := c.AddCollaborator("op", "brain", "ying", "push"); err != nil {
		t.Fatal(err)
	}

	ying := c.AsUser("ying")
	invs, err := ying.PendingInvitations()
	if err != nil || len(invs) != 1 {
		t.Fatalf("PendingInvitations = (%v, %v), want 1 invite", invs, err)
	}
	inv := invs[0]
	// The bug-2 precondition: the invitation carries no ssh_url.
	if inv.Repository.SSHURL != "" {
		t.Fatalf("precondition: invitation ssh_url should be empty (MinimalRepository), got %q", inv.Repository.SSHURL)
	}
	// The fix: CloneURL fabricates a usable URL from full_name.
	url := ying.CloneURL(inv.Repository.FullName, inv.Repository.SSHURL)
	if url == "" {
		t.Fatal("CloneURL returned empty — bug 2 would reproduce (git clone \"\")")
	}
	dest := t.TempDir() + "/clone"
	if out, err := exec.Command("git", "clone", url, dest).CombinedOutput(); err != nil {
		t.Fatalf("git clone %s failed: %v\n%s", url, err, out)
	}
}

// TestRegression_Nous41_11_ReinviteResends pins nous#41 #11: PUT
// /collaborators no-ops against an existing invitation, so a naive re-invite
// sends nothing. InviteCollaborator must delete-then-readd so it actually
// re-sends — AND a failure to list/clear stale invites must be a HARD error
// (not swallowed), or the PUT silently no-ops.
func TestRegression_Nous41_11_ReinviteResends(t *testing.T) {
	c := NewFake(Conf{}).(*fakeClient)
	c.AddUser("op")
	c.AddUser("ying")
	c.SwitchUser("op")
	c.CreateRepo("op", "brain", true)

	// A stale pending invite exists; a fresh re-invite must replace it.
	if err := c.AddCollaborator("op", "brain", "ying", "push"); err != nil {
		t.Fatal(err)
	}
	res, err := c.InviteCollaborator("op", "brain", "ying", "push")
	if err != nil {
		t.Fatalf("InviteCollaborator: %v", err)
	}
	if !res.ReplacedStale {
		t.Fatal("expected ReplacedStale=true — the re-invite must clear the stale invite, else PUT no-ops")
	}

	// The hard-error guarantee: if listing invitations fails, the whole
	// operation fails loudly rather than no-opping.
	c.FailListInvitations(true)
	if _, err := c.InviteCollaborator("op", "brain", "ying", "push"); err == nil {
		t.Fatal("expected a hard error when listing invitations fails (a swallowed error would silently no-op the PUT)")
	}
}
