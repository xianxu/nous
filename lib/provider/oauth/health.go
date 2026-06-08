package oauth

import (
	"fmt"
	"strings"

	"github.com/xianxu/nous/lib/provider/vault"
)

// HealthState describes whether a stored OAuth credential can still
// successfully refresh against the issuer. The TUI surfaces this so
// the operator knows when an account needs reauthentication before
// they hit a cryptic invalid_grant error mid-action.
//
// See nous#15 for the design rationale: charon can't prevent refresh-
// token death (Google's policies determine token lifetime); we can
// only detect it early.
type HealthState int

const (
	// HealthUnknown is the state when validity hasn't been checked yet,
	// or when the check itself failed for non-auth reasons (network
	// error, transient 5xx). Distinguished from NeedsReauth so the TUI
	// doesn't penalize the user for transient infrastructure issues.
	HealthUnknown HealthState = iota

	// HealthHealthy means the refresh-token successfully exchanged
	// for a fresh access token at last check. Doesn't guarantee future
	// validity (Google can revoke any time), but signals "as of last
	// check, this account works."
	HealthHealthy

	// HealthNeedsReauth means the issuer rejected the refresh token
	// in a way that won't recover from retry. Operator must
	// reauthenticate (browser flow) to issue a fresh refresh token.
	// Surfaces in the TUI as a "(needs reauth)" badge with a direct
	// reauth keystroke.
	HealthNeedsReauth
)

func (h HealthState) String() string {
	switch h {
	case HealthHealthy:
		return "healthy"
	case HealthNeedsReauth:
		return "needs reauth"
	default:
		return "unknown"
	}
}

// CheckHealth probes the refresh-token by attempting a refresh and
// classifying the outcome. Uses the same primitive (g.Refresh) the
// runtime uses for normal token rotation, so a healthy result here
// guarantees the next real refresh will succeed too (modulo Google
// revoking between this check and that call).
//
// The returned HealthState is a hint, not a contract — Google can
// revoke any time, including between CheckHealth and the next API
// call. The intended use is to surface stale state at session
// boundaries (TUI startup, long-idle wakeup) so the operator can
// proactively reauth instead of discovering the problem mid-action.
//
// Token rotation note: Google MAY issue a new refresh token in
// successful refresh responses. CheckHealth does NOT persist the
// updated credential — it's a pure probe. Callers that want to
// keep the new token should call g.Refresh directly and write back
// to the vault. This is intentional: probes happen on a TUI event
// loop where unexpected vault writes would be confusing.
func (g *GoogleProvider) CheckHealth(cred *vault.Credential) HealthState {
	return checkHealth(g.Refresh, cred)
}

// checkHealth is the provider-neutral probe shared by every Provider adapter
// (the real adapter passes g.Refresh; the fake passes its own), so the
// healthy / needs-reauth / unknown classification has one source of truth and
// the fake's refresh faults drive the same outcomes as the real adapter's.
//
//   - nil cred or no refresh token → NeedsReauth (never authenticated, or the
//     token was wiped; either way the account is unusable until reauth).
//   - refresh ok → Healthy.
//   - refresh fails with an RFC 6749 §5.2 token-state error → NeedsReauth.
//   - any other failure (network, 5xx, malformed) → Unknown, so the TUI renders
//     a neutral "?" instead of penalizing the operator for transient issues.
func checkHealth(refresh func(*vault.Credential) (*vault.Credential, error), cred *vault.Credential) HealthState {
	if cred == nil || cred.RefreshToken == "" {
		return HealthNeedsReauth
	}
	if _, err := refresh(cred); err != nil {
		if isReauthRequired(err) {
			return HealthNeedsReauth
		}
		return HealthUnknown
	}
	return HealthHealthy
}

// isReauthRequired classifies an error from g.Refresh as a "the
// refresh token won't ever work again, must reauth" outcome vs a
// transient/unknown failure. Maps OAuth2 RFC 6749 §5.2 error codes
// that explicitly indicate token-state failures.
//
// Surfaces:
//   - invalid_grant: refresh token revoked, expired, or otherwise
//     no longer recognized by the auth server. The standard "must
//     reauth" signal.
//   - invalid_token: similar; the token is malformed or no longer
//     valid for any reason.
//   - unauthorized_client: the OAuth client itself is no longer
//     authorized for this grant type; effectively requires reauth
//     (or a charon-side fix if the client was deconfigured).
//
// Anything else (network errors, 5xx, unexpected response shape,
// etc.) returns false — caller maps to HealthUnknown.
func isReauthRequired(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	for _, marker := range []string{
		"invalid_grant",
		"invalid_token",
		"unauthorized_client",
	} {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

// FriendlyError translates a refresh error into user-facing language
// suitable for TUI surfaces. The raw error is preserved (returned as
// the second value) so a debug pane can still show it.
//
// Used by the TUI to render error banners that tell the operator
// what to do, rather than dumping the OAuth library's wire-format
// message at them.
func FriendlyError(err error) (userFacing string, raw string) {
	if err == nil {
		return "", ""
	}
	raw = err.Error()
	if isReauthRequired(err) {
		return "Authentication expired or revoked. Press R to reauthenticate (opens browser).", raw
	}
	if strings.Contains(raw, "no refresh token") {
		return "No stored refresh token. Press R to authenticate (opens browser).", raw
	}
	if strings.Contains(raw, "token refresh failed") {
		return fmt.Sprintf("Couldn't reach Google's auth server. Check your network and retry. (%s)", raw), raw
	}
	// Default: surface the raw error but soften the framing.
	return fmt.Sprintf("Auth error: %s", raw), raw
}
