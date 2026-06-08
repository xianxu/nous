package oauth

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/xianxu/nous/lib/provider/vault"
)

// TestMicrosoft_AuthParams_OfflineAccessPrompt pins Microsoft's static
// authorization dialect: prompt=consent, and offline_access/openid/profile in
// the required scope set (offline_access is how MS issues a refresh token —
// there is no access_type param). PKCE's code_challenge is layered on per-call
// by the real Auth, not here (see the exchange test for the verifier).
func TestMicrosoft_AuthParams_OfflineAccessPrompt(t *testing.T) {
	if got := microsoftAuthParams(false).Get("prompt"); got != "consent" {
		t.Errorf("prompt = %q, want consent", got)
	}
	want := map[string]bool{"openid": true, "profile": true, "offline_access": true}
	got := map[string]bool{}
	for _, s := range requiredMicrosoftScopes {
		got[s] = true
	}
	for s := range want {
		if !got[s] {
			t.Errorf("requiredMicrosoftScopes missing %q (have %v)", s, requiredMicrosoftScopes)
		}
	}
	if microsoftDialect.usePKCE != true {
		t.Error("microsoftDialect.usePKCE should be true (public client)")
	}
}

// TestMicrosoftIdentity_PreferredUsernameThenUPN pins MS identity extraction:
// preferred_username preferred, upn fallback, no verified guard, error when
// neither claim is present.
func TestMicrosoftIdentity_PreferredUsernameThenUPN(t *testing.T) {
	if got, err := microsoftIdentity(mintMicrosoftIDToken("u@xldigit.com", false)); err != nil || got != "u@xldigit.com" {
		t.Errorf("preferred_username: got (%q,%v), want (u@xldigit.com,nil)", got, err)
	}
	// upn fallback when preferred_username is absent.
	upnOnly := makeTestJWT(`{"upn":"fallback@xldigit.com"}`)
	if got, err := microsoftIdentity(upnOnly); err != nil || got != "fallback@xldigit.com" {
		t.Errorf("upn fallback: got (%q,%v), want (fallback@xldigit.com,nil)", got, err)
	}
	// neither claim → error.
	if _, err := microsoftIdentity(makeTestJWT(`{"sub":"abc"}`)); err == nil {
		t.Error("expected error when neither preferred_username nor upn present")
	}
}

// TestMicrosoft_Exchange_SendsVerifierNoSecret is the hermetic proof that the
// public-client + PKCE token exchange wire is correct: the code exchange sends
// the PKCE code_verifier and NO client_secret, and the MS id_token routes
// through microsoftIdentity (Provider:"microsoft", Account: preferred_username).
func TestMicrosoft_Exchange_SendsVerifierNoSecret(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("ParseForm: %v", err)
		}
		if got := r.FormValue("grant_type"); got != "authorization_code" {
			t.Errorf("grant_type = %q, want authorization_code", got)
		}
		if got := r.FormValue("code_verifier"); got != "the-verifier" {
			t.Errorf("code_verifier = %q, want the-verifier", got)
		}
		if _, ok := r.Form["client_secret"]; ok {
			t.Errorf("public client must NOT send client_secret, form = %v", r.Form)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, fmt.Sprintf(
			`{"access_token":"at","refresh_token":"rt","id_token":%q,"expires_in":3600,"scope":"openid profile offline_access"}`,
			mintMicrosoftIDToken("u@xldigit.com", false)))
	}))
	defer srv.Close()

	// Public client: ClientID set, ClientSecret empty, MS dialect (usePKCE).
	p := newProvider(Conf{ClientID: "cid", TokenURL: srv.URL}, microsoftDialect)
	cred, err := p.exchangeCode("the-code", "http://localhost:1234", "the-verifier")
	if err != nil {
		t.Fatalf("exchangeCode: %v", err)
	}
	if cred.Provider != "microsoft" || cred.Account != "u@xldigit.com" {
		t.Fatalf("bad cred: provider=%q account=%q", cred.Provider, cred.Account)
	}
	if cred.AccessToken != "at" || cred.RefreshToken != "rt" {
		t.Fatalf("bad tokens: %+v", cred)
	}
}

// TestMicrosoft_Refresh_RotatesNoSecret grounds the always-rotate refresh on the
// real adapter (public client, no client_secret): the response's new refresh
// token is adopted via applyRefresh.
func TestMicrosoft_Refresh_RotatesNoSecret(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("ParseForm: %v", err)
		}
		if _, ok := r.Form["client_secret"]; ok {
			t.Errorf("public client must NOT send client_secret on refresh, form = %v", r.Form)
		}
		if got := r.FormValue("grant_type"); got != "refresh_token" {
			t.Errorf("grant_type = %q, want refresh_token", got)
		}
		w.Header().Set("Content-Type", "application/json")
		// MS always rotates: the response carries a NEW refresh token.
		_, _ = io.WriteString(w, `{"access_token":"at2","refresh_token":"rt2","expires_in":3600}`)
	}))
	defer srv.Close()

	p := newProvider(Conf{ClientID: "cid", TokenURL: srv.URL}, microsoftDialect)
	in := &vault.Credential{
		Type:         vault.TypeOAuth,
		Provider:     "microsoft",
		Account:      "u@xldigit.com",
		AccessToken:  "at1",
		RefreshToken: "rt1",
		Scopes:       []string{"openid", "profile", "offline_access"},
	}
	out, err := p.Refresh(in)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if out.AccessToken != "at2" {
		t.Errorf("AccessToken = %q, want at2", out.AccessToken)
	}
	if out.RefreshToken != "rt2" {
		t.Errorf("refresh token did not rotate: %q, want rt2", out.RefreshToken)
	}
}

// TestMicrosoftRevoke_Unsupported pins the honest n=2 finding: Microsoft has no
// per-token revoke endpoint, so Revoke returns ErrRevokeUnsupported.
func TestMicrosoftRevoke_Unsupported(t *testing.T) {
	p := NewMicrosoftProvider("cid", "tenant")
	if err := p.Revoke("some-rt"); err != ErrRevokeUnsupported {
		t.Fatalf("Revoke err = %v, want ErrRevokeUnsupported", err)
	}
}

// (helper-free: the Refresh test seeds its credential inline.)

// TestNewMicrosoftProvider_PublicClientEndpoints checks the constructor wires
// tenant-scoped v2.0 endpoints, a public client (no secret), and no revoke URL.
func TestNewMicrosoftProvider_PublicClientEndpoints(t *testing.T) {
	p := NewMicrosoftProvider("app-id", "my-tenant")
	if p.clientSecret != "" {
		t.Error("Microsoft is a public client — clientSecret must be empty")
	}
	if p.authURL != "https://login.microsoftonline.com/my-tenant/oauth2/v2.0/authorize" {
		t.Errorf("authURL = %q", p.authURL)
	}
	if p.tokenURL != "https://login.microsoftonline.com/my-tenant/oauth2/v2.0/token" {
		t.Errorf("tokenURL = %q", p.tokenURL)
	}
	if p.revokeURL != "" {
		t.Errorf("revokeURL should be empty (MS has no token-revoke endpoint), got %q", p.revokeURL)
	}
}
