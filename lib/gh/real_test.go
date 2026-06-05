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
