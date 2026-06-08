package oauth

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/xianxu/nous/lib/provider/vault"
)

// Fake is the in-memory OAuth issuer — the shim'(google-oauth) deliverable. It
// implements the Provider port by minting tokenResponses and running them
// through the *same* pure core (credentialFromToken / applyRefresh) the real
// adapter uses, so it exercises real ID-token parsing and credential shaping
// rather than bypassing them.
//
// It is an executable model of the issuer's consumer-observable state machine
// (target: oauth-credential-lifecycle), not a mock: the happy-path operations
// are the consumer-driven edges, and the fault knobs are the issuer's
// provider-autonomous edges — the transitions we don't drive and can only
// observe late. Construct with NewFake(Conf); seed/fault via the methods below.
//
// All exported methods are safe for concurrent use.
type Fake struct {
	conf Conf

	mu        sync.Mutex
	live      map[string]string // live refresh token → account email
	authEmail string            // identity the next Auth resolves to
	verified  bool              // email_verified for the next Auth
	seq       int               // monotonic unique-token source
	now       func() time.Time

	// Fault knobs = the S machine's provider-autonomous edges.
	denyConsent  bool                // Auth consent leg → access_denied (NoGrant→NoGrant)
	wrongAccount string              // Auth resolves to this instead of authEmail
	transient    bool                // Refresh fails transiently → Unknown (state belief unchanged)
	revokeFails  bool                // Revoke request itself errors
	rotateRT     bool                // issue a new refresh token on every refresh (Microsoft-like)
	dead         map[string]bool     // account → grant killed by the issuer (Refresh→Dead)
	downgrade    map[string][]string // account → reduced scope set returned on next Refresh

	lastAuthURL string // the authorization request the last Auth built
}

var _ Provider = (*Fake)(nil)

// NewFake builds an in-memory issuer. Conf endpoints/scopes are used only to
// build the recorded authorization URL and the default scope set; no network.
func NewFake(conf Conf) *Fake {
	return &Fake{
		conf:      conf,
		live:      map[string]string{},
		dead:      map[string]bool{},
		downgrade: map[string][]string{},
		verified:  true,
		now:       time.Now,
	}
}

// mintToken returns a unique opaque token with the given prefix. Caller holds mu.
func (f *Fake) mintToken(prefix string) string {
	f.seq++
	return fmt.Sprintf("%s-%d", prefix, f.seq)
}

// --- seeding API ---------------------------------------------------------

// SetAuthEmail sets the identity (and email_verified flag) the next Auth
// resolves its consent to. verified=false exercises the credentialFromToken
// unverified-email guard.
func (f *Fake) SetAuthEmail(email string, verified bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.authEmail = email
	f.verified = verified
}

// SetClock overrides the clock used for token expiry (default time.Now). For
// deterministic hermetic harnesses; assert expiry via Credential.IsExpiredAt.
func (f *Fake) SetClock(now func() time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = now
}

// SeedAccount creates a live, refreshable grant directly (without the consent
// leg) and returns a credential already in the Expired state — ready to drive
// Refresh / CheckHealth / Revoke tests. Mirrors the conformance backend, which
// seeds from a Keychain-stored refresh token.
func (f *Fake) SeedAccount(account string, scopes []string) *vault.Credential {
	f.mu.Lock()
	defer f.mu.Unlock()
	rt := f.mintToken("rt")
	f.live[rt] = account
	return &vault.Credential{
		Type:         vault.TypeOAuth,
		Provider:     "google",
		Account:      account,
		AccessToken:  f.mintToken("at"),
		RefreshToken: rt,
		Scopes:       scopes,
		Expiry:       f.now().Add(-time.Minute), // already expired → ready to refresh
	}
}

// --- fault API (named provider-autonomous transitions) -------------------

// SetRotateRefreshTokens makes Refresh issue a new refresh token each time
// (Microsoft-like always-rotate). Default false (Google usually keeps the same
// refresh token across a refresh).
func (f *Fake) SetRotateRefreshTokens(v bool) { f.set(func() { f.rotateRT = v }) }

// DenyConsent makes the next Auth's consent leg return access_denied.
func (f *Fake) DenyConsent(v bool) { f.set(func() { f.denyConsent = v }) }

// WrongAccount makes Auth resolve consent to a different email than requested
// (the "requested X but authenticated as Y" path). Empty clears it.
func (f *Fake) WrongAccount(email string) { f.set(func() { f.wrongAccount = email }) }

// Transient makes Refresh fail with a transient error → CheckHealth Unknown
// (state belief unchanged; not a Dead transition).
func (f *Fake) Transient(v bool) { f.set(func() { f.transient = v }) }

// RevokeFails makes the Revoke request itself error (network-shaped).
func (f *Fake) RevokeFails(v bool) { f.set(func() { f.revokeFails = v }) }

// RevokeGrant kills the issuer-side grant for account: its next Refresh returns
// invalid_grant (Active/Expired → Dead) — the provider-autonomous revocation we
// can't drive and only see on the next call.
func (f *Fake) RevokeGrant(account string) { f.set(func() { f.dead[account] = true }) }

// DowngradeScope makes account's next Refresh return a reduced scope set
// (one-shot), modeling an admin trimming a granted scope.
func (f *Fake) DowngradeScope(account string, scopes []string) {
	f.set(func() { f.downgrade[account] = scopes })
}

func (f *Fake) set(mut func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	mut()
}

// LastAuthURL returns the authorization request the most recent Auth built.
// Lets a test assert the consent leg is modeled (response_type=code, scope,
// redirect_uri) even though the fake skips the browser.
func (f *Fake) LastAuthURL() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastAuthURL
}

// --- Provider implementation --------------------------------------------

// Auth models NoGrant→Active: it builds (and records) the real authorization
// request, resolves consent synchronously (the async callback short-circuited),
// then mints a token response and shapes it through the shared pure core.
func (f *Fake) Auth(account string, scopes, existingScopes []string, forceFresh bool) (*vault.Credential, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if len(scopes) == 0 {
		scopes = f.conf.DefaultScopes
	}
	var allScopes []string
	if forceFresh {
		allScopes = mergeScopes(scopes, requiredGoogleScopes)
	} else {
		allScopes = mergeScopes(mergeScopes(scopes, existingScopes), requiredGoogleScopes)
	}

	// Build + record the authorization request — the consent leg is modeled,
	// not faked away. No real browser, so the redirect URI is synthetic.
	redirectURI := "http://127.0.0.1:0/fake-oauth-callback"
	f.lastAuthURL = buildAuthURL(f.conf.AuthURL, f.conf.ClientID, redirectURI, allScopes, account, forceFresh)

	// Consent short-circuit: the PendingConsent sub-state resolves immediately.
	if f.denyConsent {
		return nil, fmt.Errorf("OAuth callback failed: OAuth error: access_denied")
	}

	email := f.authEmail
	if f.wrongAccount != "" {
		email = f.wrongAccount
	}

	rt := f.mintToken("rt")
	tok := tokenResponse{
		AccessToken:  f.mintToken("at"),
		RefreshToken: rt,
		IDToken:      mintIDToken(email, f.verified),
		ExpiresIn:    3600,
		Scope:        strings.Join(allScopes, " "),
	}
	cred, err := credentialFromToken(tok, f.now())
	if err != nil {
		return nil, err
	}
	f.live[rt] = cred.Account
	return cred, nil
}

// Refresh models Expired→Active (or →Dead / →Unknown under fault). It looks up
// the grant by refresh token, applies the issuer's autonomous faults, then
// shapes the rotated response through the shared applyRefresh.
func (f *Fake) Refresh(cred *vault.Credential) (*vault.Credential, error) {
	if cred.RefreshToken == "" {
		return nil, fmt.Errorf("no refresh token for %s/%s", cred.Provider, cred.Account)
	}
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.transient {
		// Transient (network / 5xx): we can't tell if the token is bad →
		// CheckHealth maps this to Unknown, not NeedsReauth.
		return nil, fmt.Errorf("token refresh failed: simulated transient error (HTTP 503)")
	}

	account, ok := f.live[cred.RefreshToken]
	if !ok || f.dead[account] {
		// Issuer rejects the grant — the standard "must reauth" signal.
		return nil, fmt.Errorf("token refresh error: invalid_grant: refresh token revoked or expired")
	}

	tok := tokenResponse{
		AccessToken: f.mintToken("at"),
		ExpiresIn:   3600,
	}
	if f.rotateRT {
		newRT := f.mintToken("rt")
		tok.RefreshToken = newRT
		delete(f.live, cred.RefreshToken)
		f.live[newRT] = account
	}
	if reduced, ok := f.downgrade[account]; ok {
		tok.Scope = strings.Join(reduced, " ")
		delete(f.downgrade, account) // one-shot
	}
	return applyRefresh(cred, tok, f.now()), nil
}

// Revoke models any→NoGrant: it deletes the live grant. An unknown token is
// treated as already-revoked (mirrors Google's 400 invalid_token → success).
func (f *Fake) Revoke(refreshToken string) error {
	if refreshToken == "" {
		return fmt.Errorf("no refresh token to revoke")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.revokeFails {
		return fmt.Errorf("revoke request failed: simulated network error")
	}
	if _, ok := f.live[refreshToken]; !ok {
		return ErrAlreadyRevoked
	}
	delete(f.live, refreshToken)
	return nil
}

// CheckHealth is the shared composite over Refresh — identical classification
// to the real adapter, driven by the fake's own faults.
func (f *Fake) CheckHealth(cred *vault.Credential) HealthState {
	return checkHealth(f.Refresh, cred)
}
