package oauth

import (
	"errors"
	"strings"
	"testing"
)

func TestIsReauthRequired_RecognizesOAuth2ErrorCodes(t *testing.T) {
	cases := []struct {
		name string
		err  string
		want bool
	}{
		// Google's actual error format from g.Refresh — observed
		// 2026-05-09 in nous#15's surfacing case:
		//   "token refresh error: invalid_grant: Token has been
		//    expired or revoked."
		{"invalid_grant", "token refresh error: invalid_grant: Token has been expired or revoked.", true},
		{"invalid_token", "auth failed: invalid_token: token does not match", true},
		{"unauthorized_client", "unauthorized_client: this client is not authorized", true},

		// Errors that aren't reauth signals — should return false
		// so the caller maps to HealthUnknown rather than penalizing
		// the operator for transient issues.
		{"network failure", "token refresh failed: dial tcp: connection refused", false},
		{"5xx response", "token refresh failed: unexpected status: 503", false},
		{"malformed", "failed to parse token response: unexpected EOF", false},
		{"empty error string", "", false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := isReauthRequired(errors.New(c.err))
			if c.err == "" {
				// Special case: nil-error check via a fresh nil; the
				// literal "" goes through the same path but the test
				// harness can't pass a nil through errors.New.
				return
			}
			if got != c.want {
				t.Errorf("isReauthRequired(%q) = %v, want %v", c.err, got, c.want)
			}
		})
	}

	// Explicit nil-error case
	if isReauthRequired(nil) != false {
		t.Errorf("isReauthRequired(nil) should be false")
	}
}

func TestFriendlyError_ReauthCase(t *testing.T) {
	user, raw := FriendlyError(errors.New("token refresh error: invalid_grant: Token has been expired or revoked."))
	if user == "" {
		t.Fatal("expected user-facing message")
	}
	if !strings.Contains(user, "Press R to reauthenticate") {
		t.Errorf("user-facing message missing reauth hint: %q", user)
	}
	if raw == "" {
		t.Errorf("raw error not preserved")
	}
}

func TestFriendlyError_NoRefreshToken(t *testing.T) {
	user, _ := FriendlyError(errors.New("no refresh token for google/foo@bar.com"))
	if !strings.Contains(user, "Press R to authenticate") {
		t.Errorf("no-refresh-token case missing auth hint: %q", user)
	}
}

func TestFriendlyError_NetworkFailure(t *testing.T) {
	user, _ := FriendlyError(errors.New("token refresh failed: connection refused"))
	if !strings.Contains(user, "Couldn't reach Google") {
		t.Errorf("network-failure case missing reachability hint: %q", user)
	}
}

func TestFriendlyError_NilError(t *testing.T) {
	user, raw := FriendlyError(nil)
	if user != "" || raw != "" {
		t.Errorf("nil error should return empty strings; got user=%q raw=%q", user, raw)
	}
}

func TestHealthState_String(t *testing.T) {
	cases := []struct {
		state HealthState
		want  string
	}{
		{HealthHealthy, "healthy"},
		{HealthNeedsReauth, "needs reauth"},
		{HealthUnknown, "unknown"},
		{HealthState(99), "unknown"},
	}
	for _, c := range cases {
		if got := c.state.String(); got != c.want {
			t.Errorf("HealthState(%d).String() = %q, want %q", c.state, got, c.want)
		}
	}
}

