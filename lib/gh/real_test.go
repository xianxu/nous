package gh

import (
	"slices"
	"testing"
)

// These tests pin the exact `gh api` endpoint a real-adapter method
// targets — the BELOW-THE-SEAM bug class a library-level fake structurally
// cannot see (nous#26 bug 1: the 404 fell on the /users/<login> validation
// lookup, which is a DIFFERENT endpoint from the /user bearer-token lookup).
// They swap the runImpl exec seam so nothing actually execs `gh`.

func TestRealClient_UserExists_HitsUsersEndpoint(t *testing.T) {
	var gotArgs []string
	old := runImpl
	runImpl = func(_ Conf, args ...string) ([]byte, error) { gotArgs = args; return nil, nil }
	t.Cleanup(func() { runImpl = old })

	_ = New(Conf{}).UserExists("octocat")
	// bug 1: must probe /users/<login> (public lookup), NOT /user.
	want := []string{"api", "users/octocat", "--silent"}
	if !slices.Equal(gotArgs, want) {
		t.Fatalf("UserExists args = %v, want %v", gotArgs, want)
	}
}

func TestRealClient_AuthLogin_HitsUserEndpoint(t *testing.T) {
	var gotArgs []string
	old := runImpl
	runImpl = func(_ Conf, args ...string) ([]byte, error) { gotArgs = args; return []byte("x\n"), nil }
	t.Cleanup(func() { runImpl = old })

	_, _ = New(Conf{}).AuthLogin()
	// /user, the bearer-token lookup (works while /users/<login> lags).
	want := []string{"api", "user", "--jq", ".login"}
	if !slices.Equal(gotArgs, want) {
		t.Fatalf("AuthLogin args = %v, want %v", gotArgs, want)
	}
}

// TestRealClient_Endpoints pins the exact `gh api` argument vector for every
// real-adapter method below the seam. The mechanical free-func→method move in
// M1 could have mangled any endpoint string; a library-level fake can't see
// that (it replaces this layer), and the M3 conformance run is monthly — so
// these arg-vector assertions are the fast guard for the whole bug-1 class.
func TestRealClient_Endpoints(t *testing.T) {
	cases := []struct {
		name string
		call func(c Client)
		want []string
	}{
		{"CollaboratorPermission", func(c Client) { c.CollaboratorPermission("o", "r", "l") },
			[]string{"api", "repos/o/r/collaborators/l/permission", "--jq", ".permission"}},
		{"ListCollaborators", func(c Client) { c.ListCollaborators("o", "r") },
			[]string{"api", "--paginate", "repos/o/r/collaborators", "--jq", ".[].login"}},
		{"AddCollaborator", func(c Client) { c.AddCollaborator("o", "r", "l", "push") },
			[]string{"api", "-X", "PUT", "repos/o/r/collaborators/l", "-f", "permission=push", "--silent"}},
		{"RemoveCollaborator", func(c Client) { c.RemoveCollaborator("o", "r", "l") },
			[]string{"api", "-X", "DELETE", "repos/o/r/collaborators/l", "--silent"}},
		{"RepoPendingInvitations", func(c Client) { c.RepoPendingInvitations("o", "r") },
			[]string{"api", "--paginate", "repos/o/r/invitations"}},
		{"DeleteRepoInvitation", func(c Client) { c.DeleteRepoInvitation("o", "r", 7) },
			[]string{"api", "-X", "DELETE", "repos/o/r/invitations/7", "--silent"}},
		{"PendingInvitations", func(c Client) { c.PendingInvitations() },
			[]string{"api", "user/repository_invitations"}},
		{"AcceptInvitation", func(c Client) { c.AcceptInvitation(7) },
			[]string{"api", "-X", "PATCH", "user/repository_invitations/7", "--silent"}},
		{"DeclineInvitation", func(c Client) { c.DeclineInvitation(7) },
			[]string{"api", "-X", "DELETE", "user/repository_invitations/7", "--silent"}},
		{"UserRepos", func(c Client) { c.UserRepos() },
			[]string{"api", "user/repos", "--paginate", "-X", "GET", "-f", "per_page=100"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var gotArgs []string
			old := runImpl
			// return "[]" so JSON-array parsers don't error; args are captured first regardless.
			runImpl = func(_ Conf, args ...string) ([]byte, error) { gotArgs = args; return []byte("[]"), nil }
			t.Cleanup(func() { runImpl = old })

			tc.call(New(Conf{}))
			if !slices.Equal(gotArgs, tc.want) {
				t.Fatalf("%s args = %v, want %v", tc.name, gotArgs, tc.want)
			}
		})
	}
}

// TestRealClient_InviteCollaborator_ClearsStaleThenAdds pins the composite
// re-invite sequence (nous#41 #11): list repo invitations, delete a matching
// stale one, then PUT the collaborator. Verifies endpoints AND ordering.
func TestRealClient_InviteCollaborator_ClearsStaleThenAdds(t *testing.T) {
	var calls [][]string
	old := runImpl
	runImpl = func(_ Conf, args ...string) ([]byte, error) {
		calls = append(calls, args)
		if args[0] == "api" && len(args) >= 2 && args[1] == "--paginate" {
			// RepoPendingInvitations → one stale invite for "l", id 9.
			return []byte(`[{"id":9,"invitee":{"login":"l"}}]`), nil
		}
		return []byte("[]"), nil
	}
	t.Cleanup(func() { runImpl = old })

	res, err := New(Conf{}).InviteCollaborator("o", "r", "l", "push")
	if err != nil {
		t.Fatalf("InviteCollaborator: %v", err)
	}
	if !res.ReplacedStale {
		t.Fatalf("expected ReplacedStale=true when a stale invite existed")
	}
	want := [][]string{
		{"api", "--paginate", "repos/o/r/invitations"},
		{"api", "-X", "DELETE", "repos/o/r/invitations/9", "--silent"},
		{"api", "-X", "PUT", "repos/o/r/collaborators/l", "-f", "permission=push", "--silent"},
	}
	if len(calls) != len(want) {
		t.Fatalf("call count = %d, want %d (%v)", len(calls), len(want), calls)
	}
	for i := range want {
		if !slices.Equal(calls[i], want[i]) {
			t.Fatalf("call %d = %v, want %v", i, calls[i], want[i])
		}
	}
}

// TestRealClient_Conf_TokenPassedToExec guards the conformance multi-token
// mechanism: a non-empty Conf.Token must reach the exec seam so two clients
// in one process can act as two users (M3 real backend).
func TestRealClient_Conf_TokenPassedToExec(t *testing.T) {
	var gotConf Conf
	old := runImpl
	runImpl = func(conf Conf, _ ...string) ([]byte, error) { gotConf = conf; return []byte("x\n"), nil }
	t.Cleanup(func() { runImpl = old })

	_, _ = New(Conf{Token: "ghp_test"}).AuthLogin()
	if gotConf.Token != "ghp_test" {
		t.Fatalf("Conf.Token = %q, want it threaded to the exec seam", gotConf.Token)
	}
}
