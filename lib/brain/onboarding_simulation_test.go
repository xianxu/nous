package brain_test

import (
	"context"
	"runtime"
	"strings"
	"testing"

	"github.com/xianxu/nous/lib/brain"
	"github.com/xianxu/nous/lib/brainsync"
	"github.com/xianxu/nous/lib/gh"
)

// TestSimulation_OnboardingLifecycle is the forward-looking payoff of the
// shim (nous#42 M5): a single hermetic `go test` drives the full multi-actor
// brain-onboarding lifecycle with the GitHub CONTROL plane modeled by the
// in-memory gh.Fake and the DATA plane on a tmpdir gcrypt bare repo. The two
// planes meet at gh.Fake.CloneURL — the fake CREATES the bare repo, and its
// CloneURL for that repo IS the very gcrypt remote the brain is provisioned
// into (asserted in step 3).
//
// This is the deterministic shell extended outward to include GitHub: no
// network, no VM, operator + joiner over one in-memory GitHub, state evolving
// across actors and time. It demonstrates the fake as a self-verification
// substrate for ANY future GitHub-touching feature — not just a regression
// harness. (Contrast TestEndToEnd_GitHubMediatedOnboarding, which modeled the
// join by calling PublishOwnPubkeyToRemote directly because there was no
// control-plane fake to invite/accept through.)
func TestSimulation_OnboardingLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test (-short)")
	}
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skipf("integration test requires POSIX gpg; runtime is %s", runtime.GOOS)
	}
	mustHave(t, "gpg")
	mustHave(t, "git")
	mustHave(t, "git-remote-gcrypt")

	ctx := context.Background()
	const joinerLogin = "joiner"

	// --- the in-memory GitHub (control plane) ---
	base := "file://" + t.TempDir() + "/"
	ghOp := gh.NewFake(gh.Conf{CloneURLBase: base}).(*gh.Fake)
	ghOp.AddUser("operator")
	ghOp.AddUser(joinerLogin)
	ghOp.SwitchUser("operator")
	ghOp.CreateRepo("operator", "brain", true) // also git-inits the bare repo (data plane)
	ghJoiner := ghOp.AsUser(joinerLogin)

	// THE SEAM: the fake's CloneURL for the repo IS the gcrypt remote the
	// brain is provisioned into. Control plane and data plane meet here.
	remoteURL := ghOp.CloneURL("operator/brain", "")
	if remoteURL == "" {
		t.Fatal("fake CloneURL returned empty")
	}

	operator := setupPeer(t, "operator", "operator@test.local")
	joiner := setupPeer(t, joinerLogin, "joiner@test.local")

	// === Step 1 (DATA PLANE): operator provisions the brain into the repo ===
	withPeer(t, operator, func() {
		operator.brainPath = provisionBrain(t, operator, remoteURL, []string{operator.fp})
	})

	// === Step 2 (CONTROL PLANE): operator invites joiner via the fake ===
	if _, err := ghOp.InviteCollaborator("operator", "brain", joinerLogin, "push"); err != nil {
		t.Fatalf("InviteCollaborator: %v", err)
	}
	if pend, err := ghOp.RepoPendingInvitations("operator", "brain"); err != nil || len(pend) != 1 || pend[0].Invitee.Login != joinerLogin {
		t.Fatalf("expected 1 pending invite for joiner, got %+v (err %v)", pend, err)
	}

	// === Step 3 (CONTROL PLANE): joiner sees + accepts the invite ===
	invs, err := ghJoiner.PendingInvitations()
	if err != nil || len(invs) != 1 {
		t.Fatalf("joiner PendingInvitations = (%v, %v), want 1", invs, err)
	}
	inv := invs[0]
	if inv.Repository.SSHURL != "" {
		t.Fatalf("invitation must be a MinimalRepository (empty ssh_url), got %q", inv.Repository.SSHURL)
	}
	// the seam, asserted: the clone URL fabricated from full_name equals the
	// gcrypt remote the brain actually lives in.
	if got := ghJoiner.CloneURL(inv.Repository.FullName, inv.Repository.SSHURL); got != remoteURL {
		t.Fatalf("invite CloneURL %q != provisioned remote %q (control/data-plane seam broken)", got, remoteURL)
	}
	if err := ghJoiner.AcceptInvitation(inv.ID); err != nil {
		t.Fatalf("AcceptInvitation: %v", err)
	}
	if cols, err := ghOp.ListCollaborators("operator", "brain"); err != nil || !simHasLogin(cols, joinerLogin) {
		t.Fatalf("joiner should be a collaborator after accept; got %v (err %v)", cols, err)
	}

	// === Step 4 (DATA PLANE): joiner publishes pubkey via plain git ===
	// Before nous#26 this post-accept step could wedge a "collaborator-but-
	// unpublished" stuck state (bug 3). Here it's a normal resumable step.
	withPeer(t, joiner, func() {
		if err := brain.PublishOwnPubkeyToRemote(ctx, remoteURL, joinerLogin, joiner.armorPub); err != nil {
			t.Fatalf("joiner PublishOwnPubkeyToRemote: %v", err)
		}
	})

	// === Step 5 (DATA PLANE): operator auto-admits the joiner ===
	withPeer(t, operator, func() {
		if _, _, err := brain.ImportAllPubkeys(ctx, operator.brainPath); err != nil {
			t.Fatalf("ImportAllPubkeys: %v", err)
		}
		added, drift, err := brain.AutoAdmitFromKeysBranch(ctx, operator.brainPath)
		if err != nil {
			t.Fatalf("AutoAdmitFromKeysBranch: %v", err)
		}
		if len(drift) != 0 {
			t.Errorf("unexpected drift: %+v", drift)
		}
		if len(added) != 1 || added[0].Login != joinerLogin {
			t.Fatalf("expected joiner admitted, got %+v", added)
		}
		if err := brainsync.AddCommitPush(operator.brainPath, "auto-admit "+joinerLogin); err != nil {
			t.Fatalf("operator push post-admit: %v", err)
		}
	})

	// === Step 6 (DATA PLANE): joiner clones via gcrypt — decrypt AND verify ===
	// The bug-5 property exercised in-flow: the joiner must both decrypt main
	// (they're a gcrypt recipient now) AND signature-verify it (operator's
	// pubkey is on the keys branch). Both fps present in the manifest proves it.
	withPeer(t, joiner, func() {
		joiner.brainPath = cloneBrainViaPeerkeys(t, joiner, remoteURL)
		manifest := readBrainFile(t, joiner.brainPath, ".brain/config.md")
		if !strings.Contains(strings.ToUpper(manifest), strings.ToUpper(joiner.fp)) {
			t.Errorf("joiner manifest missing joiner fp:\n%s", manifest)
		}
		if !strings.Contains(strings.ToUpper(manifest), strings.ToUpper(operator.fp)) {
			t.Errorf("joiner manifest missing operator fp (the signature-verify pubkey):\n%s", manifest)
		}
	})

	// === Step 7 (recovery property, bug 3): re-running join steps is
	// idempotent — there is no stuck state to wedge in; the flow is resumable. ===
	withPeer(t, joiner, func() {
		if err := brain.PublishOwnPubkeyToRemote(ctx, remoteURL, joinerLogin, joiner.armorPub); err != nil {
			t.Fatalf("re-publish should be idempotent: %v", err)
		}
	})
	withPeer(t, operator, func() {
		added, _, err := brain.AutoAdmitFromKeysBranch(ctx, operator.brainPath)
		if err != nil {
			t.Fatalf("re-run auto-admit: %v", err)
		}
		if len(added) != 0 {
			t.Errorf("re-run should admit nobody new (idempotent recovery), got %+v", added)
		}
	})

	// === Step 8 (CONTROL PLANE): joiner leaves — GitHub access revoked ===
	if err := ghJoiner.RemoveCollaborator("operator", "brain", joinerLogin); err != nil {
		t.Fatalf("RemoveCollaborator (leave): %v", err)
	}
	if cols, _ := ghOp.ListCollaborators("operator", "brain"); simHasLogin(cols, joinerLogin) {
		t.Fatalf("joiner should no longer be a collaborator after leave; got %v", cols)
	}
}

func simHasLogin(s []string, v string) bool {
	for _, x := range s {
		if strings.EqualFold(x, v) {
			return true
		}
	}
	return false
}
