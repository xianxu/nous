package oauth

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/xianxu/nous/lib/provider/vault"
)

// buildAuthURL constructs the OAuth2 authorization-code request URL. Free
// function (not a method) so both the real adapter — which opens it in a
// browser — and the fake — which records it for assertions without a browser —
// build the identical request through one code path.
//
// access_type=offline + prompt=consent + include_granted_scopes are Google's
// dialect for "issue a refresh token" + incremental vs reductive consent; a
// non-Google adapter would vary these (e.g. the offline_access scope).
func buildAuthURL(authURL, clientID, redirectURI string, scopes []string, loginHint string, forceFresh bool) string {
	params := url.Values{
		"client_id":     {clientID},
		"redirect_uri":  {redirectURI},
		"response_type": {"code"},
		"scope":         {strings.Join(scopes, " ")},
		"access_type":   {"offline"}, // request refresh token
		"prompt":        {"consent"}, // force consent to get refresh token
	}
	if forceFresh {
		// Token covers only the requested scope set, not the union of existing
		// grants. Required for the reductive flow.
		params.Set("include_granted_scopes", "false")
	} else {
		params.Set("include_granted_scopes", "true") // incremental authorization
	}
	if loginHint != "" {
		params.Set("login_hint", loginHint)
	}
	return authURL + "?" + params.Encode()
}

// mergeScopes returns the set-union of two scope lists (order-independent,
// empties dropped). Pure; shared by the auth flow in both adapters.
func mergeScopes(requested, existing []string) []string {
	seen := make(map[string]bool)
	for _, s := range existing {
		seen[s] = true
	}
	for _, s := range requested {
		seen[s] = true
	}
	var merged []string
	for s := range seen {
		if s != "" {
			merged = append(merged, s)
		}
	}
	return merged
}

// tokenResponse is the JSON response from an OAuth2 token endpoint
// (RFC 6749 §5.1/§5.2). Shared by the real adapter (which decodes it from
// HTTP) and the fake (which mints it in-memory), so both flow through the
// same pure credential-shaping helpers below.
type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	ExpiresIn    int    `json:"expires_in"`
	TokenType    string `json:"token_type"`
	Scope        string `json:"scope"`
	Error        string `json:"error"`
	ErrorDesc    string `json:"error_description"`
}

// credentialFromToken maps a token-endpoint response to a fresh credential
// (the pure body of the code-exchange path: extract the authenticated email
// from the ID token, split scopes, compute expiry from ExpiresIn against the
// injected clock). The real adapter feeds it an HTTP-decoded response; the
// fake feeds it a minted one — one source of truth for "token → credential".
//
// It rejects an unverified email: accepting email_verified==false would let a
// caller bind a credential to an address they don't control. Real Google
// returns email_verified==true for the consent flow, so production is
// unaffected; the fake's unverified knob exercises this guard.
//
// The "google" provider id is the one per-provider concern here (the seam a
// future non-Google OIDC adapter varies); everything else is OIDC-standard.
func credentialFromToken(tok tokenResponse, now time.Time) (*vault.Credential, error) {
	email, verified, err := parseIDToken(tok.IDToken)
	if err != nil {
		return nil, fmt.Errorf("failed to identify account: %w", err)
	}
	if !verified {
		return nil, fmt.Errorf("id token email %q is not verified", email)
	}
	var scopes []string
	if tok.Scope != "" {
		scopes = strings.Split(tok.Scope, " ")
	}
	return &vault.Credential{
		Provider:     "google",
		Account:      email,
		AccessToken:  tok.AccessToken,
		RefreshToken: tok.RefreshToken,
		Expiry:       now.Add(time.Duration(tok.ExpiresIn) * time.Second),
		Scopes:       scopes,
	}, nil
}

// applyRefresh maps a refresh-grant response onto an existing credential:
// new access token + expiry, refresh-token rotation (keep the old token
// unless the response carries a new one — handles Google's sometimes-rotate
// and Microsoft's always-rotate from one branch), scope update if present
// (else default to the old scopes), and preservation of the identity fields
// (Type/Provider/Account) plus every sidecar (GCP/AIStudio/AdminKey/Catalog).
//
// The sidecar/identity preservation is load-bearing: earlier versions wiped
// the user's configured project + minted key on every rotation. Keeping it in
// one pure function is what stops the real and fake adapters from drifting on
// this contract.
func applyRefresh(old *vault.Credential, tok tokenResponse, now time.Time) *vault.Credential {
	updated := &vault.Credential{
		Type:         old.Type,
		Provider:     old.Provider,
		Account:      old.Account,
		AccessToken:  tok.AccessToken,
		RefreshToken: old.RefreshToken,
		Expiry:       now.Add(time.Duration(tok.ExpiresIn) * time.Second),
		Scopes:       old.Scopes,
		GCP:          old.GCP,
		AIStudio:     old.AIStudio,
		AdminKey:     old.AdminKey,
		Catalog:      old.Catalog,
	}
	if tok.RefreshToken != "" {
		updated.RefreshToken = tok.RefreshToken
	}
	if tok.Scope != "" {
		updated.Scopes = strings.Split(tok.Scope, " ")
	}
	return updated
}

// parseIDToken extracts the email and email_verified claims from an OIDC ID
// token (JWT). No signature verification — the token comes directly from the
// issuer's token endpoint over HTTPS. Returns (email, verified, err).
//
// email_verified is accepted as either a bool or the string "true"; Google's
// v2 endpoint returns a bool, but the string form has been observed and is
// cheap to tolerate.
func parseIDToken(idToken string) (email string, verified bool, err error) {
	if idToken == "" {
		return "", false, fmt.Errorf("no ID token in response (openid scope may not be granted)")
	}
	parts := strings.Split(idToken, ".")
	if len(parts) != 3 {
		return "", false, fmt.Errorf("invalid ID token format")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", false, fmt.Errorf("failed to decode ID token payload: %w", err)
	}
	var claims struct {
		Email         string      `json:"email"`
		EmailVerified interface{} `json:"email_verified"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return "", false, fmt.Errorf("failed to parse ID token claims: %w", err)
	}
	if claims.Email == "" {
		return "", false, fmt.Errorf("no email claim in ID token")
	}
	switch v := claims.EmailVerified.(type) {
	case bool:
		verified = v
	case string:
		verified = v == "true"
	}
	return claims.Email, verified, nil
}

// mintIDToken builds a structurally-valid unsigned ID token (header.payload.
// with an empty signature segment) carrying the given email + email_verified
// claims. The fake uses it so its tokens flow through the *same* parseIDToken
// the real adapter uses — exercising real parsing, not bypassing it.
func mintIDToken(email string, verified bool) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	payloadJSON, _ := json.Marshal(struct {
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
	}{Email: email, EmailVerified: verified})
	payload := base64.RawURLEncoding.EncodeToString(payloadJSON)
	return header + "." + payload + "."
}
