package oauth

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

const (
	obKey          = "charon-credential-proxy-obfuscation"
	obClientID     = "5a51594157591a504a55575643150c0f1a481a1b5c4a4e431d05121d4007111c0a59065250061f1c041a5a41014a041e041a4f0b421f15031d0c5e0a10051a1d17041a1d410d0c05"
	obClientSecret = "242722213f360028202410033d440c145f6821021755007837072210322757232d000d"

	// Google's production OAuth endpoints. Seeded into Conf by
	// defaultGoogleConf; the adapter reads them off its fields so tests
	// (and a future non-Google provider) can point elsewhere via Conf.
	googleAuthURL   = "https://accounts.google.com/o/oauth2/auth"
	googleTokenURL  = "https://oauth2.googleapis.com/token"
	googleRevokeURL = "https://oauth2.googleapis.com/revoke"
)

// DefaultGoogleScopes are requested if none specified.
//
// Empty by default — the TUI is the canonical UX for choosing scopes.
// Headless callers (legacy code paths) get only the required openid+email
// for ID-token email extraction; data scopes must be opted into explicitly.
var DefaultGoogleScopes = []string{}

// requiredGoogleScopes are always included to enable email extraction from ID token.
//
// Note: we use the full userinfo.email URL form rather than the OIDC short
// name "email" because Google rewrites the short form to this URL on the
// way back, and we want request and response to use the same string so
// that round-tripping (request → token endpoint → keychain → catalog
// lookup) matches.
var requiredGoogleScopes = []string{
	"openid",
	"https://www.googleapis.com/auth/userinfo.email",
}

// googleDialect is the Google variant of the Provider port. Google authenticates
// by the verified `email` claim, requests a refresh token via access_type=offline,
// and revokes via the RFC-7009 endpoint. usePKCE is false (confidential client
// with an embedded secret).
var googleDialect = dialect{
	providerID:     "google",
	requiredScopes: requiredGoogleScopes,
	usePKCE:        false,
	authParams:     googleAuthParams,
	extractID:      googleIdentity,
	mintID:         mintGoogleIDToken,
	revoke:         googleRevoke,
}

// googleAuthParams are Google's authorization-URL dialect: access_type=offline +
// prompt=consent request a refresh token; include_granted_scopes toggles
// incremental (additive) vs reductive consent.
func googleAuthParams(forceFresh bool) url.Values {
	p := url.Values{
		"access_type": {"offline"}, // request refresh token
		"prompt":      {"consent"}, // force consent to get refresh token
	}
	if forceFresh {
		// Token covers only the requested scope set, not the union of existing
		// grants. Required for the reductive flow.
		p.Set("include_granted_scopes", "false")
	} else {
		p.Set("include_granted_scopes", "true") // incremental authorization
	}
	return p
}

// googleIdentity extracts + verifies the Google identity from an ID token: the
// `email` claim, rejecting email_verified==false. Accepting an unverified email
// would let a caller bind a credential to an address they don't control. Real
// Google returns email_verified==true for the consent flow, so production is
// unaffected; the fake's unverified knob exercises this guard.
//
// The guard lives HERE (not in the shared credentialFromToken) because it is a
// Google-layer payload concern: Microsoft has no email_verified claim at all
// (nous#48). email_verified is accepted as a bool or the string "true"; an
// ABSENT claim is treated as false → rejected (the zero value), preserving the
// prior production behavior — invariant 4, do not relax to "absent = trusted".
func googleIdentity(idToken string) (string, error) {
	var claims struct {
		Email         string      `json:"email"`
		EmailVerified interface{} `json:"email_verified"`
	}
	if err := decodeIDClaims(idToken, &claims); err != nil {
		return "", err
	}
	if claims.Email == "" {
		return "", fmt.Errorf("no email claim in ID token")
	}
	var verified bool
	switch v := claims.EmailVerified.(type) {
	case bool:
		verified = v
	case string:
		verified = v == "true"
	}
	if !verified {
		return "", fmt.Errorf("id token email %q is not verified", claims.Email)
	}
	return claims.Email, nil
}

// mintGoogleIDToken builds a structurally-valid unsigned ID token (header.
// payload. with an empty signature segment) carrying email + email_verified.
// The fake uses it (via googleDialect.mintID) so its tokens flow through the
// *same* googleIdentity the real adapter uses — exercising real parsing.
func mintGoogleIDToken(email string, verified bool) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	payloadJSON, _ := json.Marshal(struct {
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
	}{Email: email, EmailVerified: verified})
	payload := base64.RawURLEncoding.EncodeToString(payloadJSON)
	return header + "." + payload + "."
}

// ErrAlreadyRevoked indicates Google considers the token already invalid
// (revoked, expired, or otherwise unusable). Callers can treat this as
// success for local cleanup purposes — the upstream side is already in
// the desired state. Distinguished from real network or protocol errors
// so genuine failures still surface.
var ErrAlreadyRevoked = errors.New("token already revoked or invalid on Google's side")

// googleRevoke calls Google's RFC-7009 revoke endpoint, invalidating the
// refresh token (and the underlying authorization grant). Returns
// ErrAlreadyRevoked when Google responds 400 invalid_token (already revoked /
// never valid); callers that just want the token gone may treat that as success.
func googleRevoke(g *OIDCProvider, refreshToken string) error {
	resp, err := http.PostForm(g.revokeURL, url.Values{
		"token": {refreshToken},
	})
	if err != nil {
		return fmt.Errorf("revoke request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		return nil
	}
	// HTTP != 200. Try to parse Google's standard OAuth error envelope
	// {"error": "...", "error_description": "..."}. Google's revoke endpoint
	// returns 400 with error=invalid_token for tokens already revoked or never
	// valid; treat that as success.
	body, _ := io.ReadAll(resp.Body)
	var oauthErr struct {
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	_ = json.Unmarshal(body, &oauthErr)
	if resp.StatusCode == http.StatusBadRequest && oauthErr.Error == "invalid_token" {
		return ErrAlreadyRevoked
	}
	if oauthErr.Error != "" {
		return fmt.Errorf("revoke returned HTTP %d (%s): %s", resp.StatusCode, oauthErr.Error, oauthErr.ErrorDescription)
	}
	return fmt.Errorf("revoke returned HTTP %d", resp.StatusCode)
}

// NewGoogleProvider builds the real adapter against Google's production
// endpoints with the embedded (obfuscated) client credentials.
func NewGoogleProvider() (*OIDCProvider, error) {
	conf, err := defaultGoogleConf()
	if err != nil {
		return nil, err
	}
	return New(conf), nil
}

// defaultGoogleConf decodes the obfuscated client credentials and sets Google's
// production endpoints + default scopes. NewGoogleProvider wraps it.
func defaultGoogleConf() (Conf, error) {
	cid, err := XORDecode(obClientID, obKey)
	if err != nil {
		return Conf{}, fmt.Errorf("failed to decode client_id: %w", err)
	}
	cs, err := XORDecode(obClientSecret, obKey)
	if err != nil {
		return Conf{}, fmt.Errorf("failed to decode client_secret: %w", err)
	}
	return Conf{
		ClientID:      cid,
		ClientSecret:  cs,
		AuthURL:       googleAuthURL,
		TokenURL:      googleTokenURL,
		RevokeURL:     googleRevokeURL,
		DefaultScopes: DefaultGoogleScopes,
	}, nil
}
