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
// The provider-specific authorization params (Google: access_type=offline +
// prompt=consent + include_granted_scopes; Microsoft: prompt=consent, with
// offline_access riding in the scope set) come in via `extra` from the
// dialect's authParams. PKCE params (code_challenge/code_challenge_method) are
// layered on by the real Auth per-call (they depend on a per-call verifier),
// not here — the fake records the URL without them.
func buildAuthURL(authURL, clientID, redirectURI string, scopes []string, loginHint string, extra url.Values) string {
	params := url.Values{
		"client_id":     {clientID},
		"redirect_uri":  {redirectURI},
		"response_type": {"code"},
		"scope":         {strings.Join(scopes, " ")},
	}
	for k, vs := range extra {
		params[k] = vs
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
// (the pure body of the code-exchange path: extract the authenticated identity
// from the ID token, split scopes, compute expiry from ExpiresIn against the
// injected clock). The real adapter feeds it an HTTP-decoded response; the
// fake feeds it a minted one — one source of truth for "token → credential".
//
// The two per-provider concerns are injected: `providerID` (the credential's
// Provider tag) and `extractID` (the identity-claim extractor — the seam
// Microsoft forced into existence, nous#48). Google's extractor reads `email`
// and rejects email_verified==false; Microsoft's reads preferred_username/upn
// with no verified claim. The verified-email guard therefore lives *inside*
// googleIdentity now (a Google-layer payload concern, not shared) — everything
// here is OIDC-standard.
func credentialFromToken(tok tokenResponse, providerID string, extractID func(idToken string) (string, error), now time.Time) (*vault.Credential, error) {
	account, err := extractID(tok.IDToken)
	if err != nil {
		return nil, fmt.Errorf("failed to identify account: %w", err)
	}
	var scopes []string
	if tok.Scope != "" {
		scopes = strings.Split(tok.Scope, " ")
	}
	return &vault.Credential{
		Provider:     providerID,
		Account:      account,
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

// decodeIDClaims decodes an OIDC ID token's (JWT) payload into the
// caller-supplied struct. Provider-neutral: split header.payload.sig,
// base64url-decode the payload, json.Unmarshal. No signature verification —
// the token comes directly from the issuer's token endpoint over HTTPS (the
// same trust assumption the prior parseIDToken made). Each provider's identity
// extractor (googleIdentity / microsoftIdentity) decodes its own claim subset
// through this one path.
func decodeIDClaims(idToken string, into any) error {
	if idToken == "" {
		return fmt.Errorf("no ID token in response (openid scope may not be granted)")
	}
	parts := strings.Split(idToken, ".")
	if len(parts) != 3 {
		return fmt.Errorf("invalid ID token format")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return fmt.Errorf("failed to decode ID token payload: %w", err)
	}
	if err := json.Unmarshal(payload, into); err != nil {
		return fmt.Errorf("failed to parse ID token claims: %w", err)
	}
	return nil
}
