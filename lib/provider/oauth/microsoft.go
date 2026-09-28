package oauth

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
)

// Microsoft identity platform (Entra ID) v2.0 endpoints are tenant-scoped: the
// tenant segment selects single-tenant ({tenant-id}), this-org-only
// (organizations), personal (consumers), or any (common). The tenant folds into
// the URL — it is not a separate Conf dimension.
func microsoftAuthURL(tenant string) string {
	return "https://login.microsoftonline.com/" + tenant + "/oauth2/v2.0/authorize"
}

func microsoftTokenURL(tenant string) string {
	return "https://login.microsoftonline.com/" + tenant + "/oauth2/v2.0/token"
}

// requiredMicrosoftScopes are always merged into a request:
//   - openid: OIDC sign-in (the sub/oid claims).
//   - profile: required for the preferred_username claim in the ID token.
//   - offline_access: Microsoft only issues a refresh token when this scope is
//     requested (unlike Google's access_type=offline auth-URL param).
var requiredMicrosoftScopes = []string{"openid", "profile", "offline_access"}

// microsoftDialect is the Microsoft variant of the Provider port. Identity is
// the human-readable preferred_username (no email_verified claim exists, so no
// verified guard); a refresh token is requested via the offline_access scope;
// the client is public (no secret) so the token exchange uses PKCE; and there is
// no per-token revoke endpoint (see microsoftRevoke).
var microsoftDialect = dialect{
	providerID:     "microsoft",
	requiredScopes: requiredMicrosoftScopes,
	usePKCE:        true,
	authParams:     microsoftAuthParams,
	extractID:      microsoftIdentity,
	mintID:         mintMicrosoftIDToken,
	revoke:         microsoftRevoke,
}

// microsoftAuthParams are Microsoft's authorization-URL dialect: prompt=consent
// to (re)issue a refresh token. offline_access rides in the scope set (not an
// auth param), and Microsoft does incremental consent natively — so there is no
// include_granted_scopes / access_type. The PKCE code_challenge is layered on by
// the real Auth per-call, not here.
func microsoftAuthParams(forceFresh bool) url.Values {
	return url.Values{"prompt": {"consent"}}
}

// microsoftIdentity extracts the Microsoft identity from an ID token: the
// human-readable preferred_username, falling back to upn. Microsoft has no
// reliable top-level email and no email_verified claim (nous#48) — so unlike
// googleIdentity there is no verified guard; the verified-email guard was a
// Google-layer concern all along.
func microsoftIdentity(idToken string) (string, error) {
	var claims struct {
		PreferredUsername string `json:"preferred_username"`
		UPN               string `json:"upn"`
	}
	if err := decodeIDClaims(idToken, &claims); err != nil {
		return "", err
	}
	id := claims.PreferredUsername
	if id == "" {
		id = claims.UPN
	}
	if id == "" {
		return "", fmt.Errorf("no preferred_username/upn claim in ID token")
	}
	return id, nil
}

// mintMicrosoftIDToken builds a structurally-valid unsigned ID token carrying a
// preferred_username claim. The fake uses it (via microsoftDialect.mintID) so
// its tokens flow through the *same* microsoftIdentity the real adapter uses.
// The `verified` arg is ignored — Microsoft has no email_verified concept — but
// kept to satisfy the dialect.mintID signature shared with Google.
func mintMicrosoftIDToken(account string, _ bool) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	payloadJSON, _ := json.Marshal(struct {
		PreferredUsername string `json:"preferred_username"`
	}{PreferredUsername: account})
	payload := base64.RawURLEncoding.EncodeToString(payloadJSON)
	return header + "." + payload + "."
}

// ErrRevokeUnsupported reports that Microsoft has no RFC-7009 per-token revoke
// endpoint. Revocation is only available via Microsoft Graph
// revokeSignInSessions (which revokes ALL of a user's refresh tokens across
// every app, needs an elevated Graph token, and is intentionally not instant) —
// not a per-credential operation a third-party app can scope to its own grant.
// The Provider.Revoke method exists for port-uniformity; the grounding boundary
// differs (this is one of the recorded nous#48 n=2 findings).
var ErrRevokeUnsupported = errors.New("microsoft has no per-token revoke endpoint (RFC 7009); revoke via Graph revokeSignInSessions or the account portal")

func microsoftRevoke(_ *OIDCProvider, _ string) error {
	return ErrRevokeUnsupported
}

// NewMicrosoftProvider builds the real adapter against a tenant's Microsoft
// identity platform v2.0 endpoints as a PUBLIC client (no secret) — the
// recommended pattern for a native/CLI app, matching charon's random loopback
// port. clientID is the Entra app's Application (client) ID; tenant is the
// Directory (tenant) ID (or organizations/common/consumers). Microsoft has no
// token-revoke endpoint, so RevokeURL is left empty.
func NewMicrosoftProvider(clientID, tenant string) *OIDCProvider {
	return newProvider(Conf{
		ClientID: clientID,
		AuthURL:  microsoftAuthURL(tenant),
		TokenURL: microsoftTokenURL(tenant),
	}, microsoftDialect)
}
