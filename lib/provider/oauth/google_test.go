package oauth

import (
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/xianxu/nous/lib/provider/vault"
)

func TestParseIDTokenEmail(t *testing.T) {
	tests := []struct {
		name    string
		token   string
		want    string
		wantErr bool
	}{
		{
			name:  "valid token",
			token: makeTestJWT(`{"email":"test@gmail.com","sub":"123"}`),
			want:  "test@gmail.com",
		},
		{
			name:    "empty token",
			token:   "",
			wantErr: true,
		},
		{
			name:    "invalid format",
			token:   "not-a-jwt",
			wantErr: true,
		},
		{
			name:    "missing email claim",
			token:   makeTestJWT(`{"sub":"123"}`),
			wantErr: true,
		},
		{
			name:    "empty email claim",
			token:   makeTestJWT(`{"email":"","sub":"123"}`),
			wantErr: true,
		},
		{
			name:    "invalid base64 payload",
			token:   "header.!!!invalid!!!.signature",
			wantErr: true,
		},
		{
			name:    "invalid json payload",
			token:   "header." + base64.RawURLEncoding.EncodeToString([]byte("not json")) + ".sig",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, _, err := parseIDToken(tt.token)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseIDToken() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("parseIDToken() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBuildAuthURL_LoginHint(t *testing.T) {
	const authURL, clientID = "https://accounts.google.com/o/oauth2/auth", "test-client-id"

	t.Run("without login hint", func(t *testing.T) {
		u := buildAuthURL(authURL, clientID, "http://localhost:1234", []string{"openid"}, "", false)
		if containsParam(u, "login_hint") {
			t.Error("expected no login_hint parameter")
		}
	})

	t.Run("with login hint", func(t *testing.T) {
		u := buildAuthURL(authURL, clientID, "http://localhost:1234", []string{"openid"}, "user@gmail.com", false)
		if !containsParam(u, "login_hint") {
			t.Error("expected login_hint parameter")
		}
	})

	t.Run("forceFresh sets include_granted_scopes=false", func(t *testing.T) {
		u := buildAuthURL(authURL, clientID, "http://localhost:1234", []string{"openid"}, "", true)
		if !strings.Contains(u, "include_granted_scopes=false") {
			t.Errorf("expected include_granted_scopes=false in URL: %s", u)
		}
	})

	t.Run("forceFresh=false keeps incremental", func(t *testing.T) {
		u := buildAuthURL(authURL, clientID, "http://localhost:1234", []string{"openid"}, "", false)
		if !strings.Contains(u, "include_granted_scopes=true") {
			t.Errorf("expected include_granted_scopes=true in URL: %s", u)
		}
	})
}

func TestMergeScopes(t *testing.T) {
	merged := mergeScopes([]string{"a", "b"}, []string{"b", "c"})
	seen := make(map[string]bool)
	for _, s := range merged {
		seen[s] = true
	}
	for _, want := range []string{"a", "b", "c"} {
		if !seen[want] {
			t.Errorf("missing scope %q in merged result %v", want, merged)
		}
	}
	if len(merged) != 3 {
		t.Errorf("expected 3 scopes, got %d: %v", len(merged), merged)
	}
}

func TestRequiredScopesIncluded(t *testing.T) {
	// Verify that requiredGoogleScopes include openid and email.
	seen := make(map[string]bool)
	for _, s := range requiredGoogleScopes {
		seen[s] = true
	}
	if !seen["openid"] {
		t.Error("requiredGoogleScopes missing 'openid'")
	}
	if !seen["https://www.googleapis.com/auth/userinfo.email"] {
		t.Error("requiredGoogleScopes missing userinfo.email")
	}
}

// Refresh must carry every non-OAuth sidecar (GCP project metadata,
// AI Studio key, admin-key/catalog payloads, Type discriminator)
// from the input credential into the refreshed credential. Without
// this, every token rotation wipes the user's configured project
// and minted keys — discovered when an account showed `vertex` but
// not `ai-studio` in `charon manifest` after a long-running session.
//
// Drives the actual HTTP refresh against a stub token endpoint by pointing
// the adapter's TokenURL at an httptest server via Conf.
func TestRefresh_PreservesSidecars(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"new-tok","expires_in":3600,"token_type":"Bearer"}`)
	}))
	defer srv.Close()

	gp := New(Conf{TokenURL: srv.URL})

	in := &vault.Credential{
		Type:         vault.TypeOAuth,
		Provider:     "google",
		Account:      "alice@gmail.com",
		AccessToken:  "stale",
		RefreshToken: "rt",
		Scopes:       []string{"openid"},
		GCP:          &vault.GCPData{ProjectID: "p", VertexRegion: "us-central1"},
		AIStudio:     &vault.AIStudioData{UID: "uid", KeyMaterial: "AIzaSy_FAKE"},
		AdminKey:     &vault.AdminKeyData{OrgID: "org-x", KeyMaterial: "sk-xxx"},
		Catalog:      &vault.CatalogData{KeyMaterial: "paste-key"},
	}
	out, err := gp.Refresh(in)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if out.AccessToken != "new-tok" {
		t.Errorf("AccessToken = %q, want new-tok", out.AccessToken)
	}
	if out.Type != vault.TypeOAuth {
		t.Errorf("Type = %q, want %q", out.Type, vault.TypeOAuth)
	}
	if out.GCP == nil || out.GCP.ProjectID != "p" {
		t.Errorf("GCP sidecar dropped or modified: %+v", out.GCP)
	}
	if out.AIStudio == nil || out.AIStudio.KeyMaterial != "AIzaSy_FAKE" {
		t.Errorf("AIStudio sidecar dropped or modified: %+v", out.AIStudio)
	}
	// AdminKey/Catalog aren't typical on a Google OAuth credential
	// in practice — but Refresh should preserve any sidecar a caller
	// passed in, regardless of whether it makes semantic sense for
	// this provider. Future-proofs against a refactor that drops
	// fields it "doesn't think apply".
	if out.AdminKey == nil || out.AdminKey.OrgID != "org-x" {
		t.Errorf("AdminKey sidecar dropped or modified: %+v", out.AdminKey)
	}
	if out.Catalog == nil || out.Catalog.KeyMaterial != "paste-key" {
		t.Errorf("Catalog sidecar dropped or modified: %+v", out.Catalog)
	}
}

// makeTestJWT creates a minimal JWT with the given JSON payload.
func makeTestJWT(payload string) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256"}`))
	body := base64.RawURLEncoding.EncodeToString([]byte(payload))
	return header + "." + body + ".fake-signature"
}

// containsParam checks if a URL string contains a query parameter.
func containsParam(urlStr, param string) bool {
	return len(urlStr) > 0 && len(param) > 0 &&
		(contains(urlStr, param+"=") || contains(urlStr, param+"%3D"))
}

func contains(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// TestWaitForCallback_Code drives the real adapter's async redirect callback
// hermetically — no browser. The channel-inside-the-adapter (waitForCallback)
// is the below-seam leg the fake can't model; this pins the code-extraction.
func TestWaitForCallback_Code(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		resp, err := http.Get(fmt.Sprintf("http://%s/?code=abc123", ln.Addr().String()))
		if err == nil {
			resp.Body.Close()
		}
	}()
	code, err := waitForCallback(ln)
	if err != nil || code != "abc123" {
		t.Fatalf("got (%q,%v), want (abc123,nil)", code, err)
	}
}

// TestWaitForCallback_Error pins the callback error leg (?error=access_denied).
func TestWaitForCallback_Error(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		resp, err := http.Get(fmt.Sprintf("http://%s/?error=access_denied", ln.Addr().String()))
		if err == nil {
			resp.Body.Close()
		}
	}()
	if _, err := waitForCallback(ln); err == nil {
		t.Fatal("expected error for access_denied callback")
	}
}

// TestExchangeCode_HTTP grounds the real adapter's token-exchange leg
// hermetically: it asserts the grant-request form params and that the response
// routes through the shared credentialFromToken. This is the below-seam
// translation the fake is structurally blind to.
func TestExchangeCode_HTTP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("ParseForm: %v", err)
		}
		if got := r.FormValue("grant_type"); got != "authorization_code" {
			t.Errorf("grant_type = %q, want authorization_code", got)
		}
		if got := r.FormValue("code"); got != "the-code" {
			t.Errorf("code = %q, want the-code", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, fmt.Sprintf(
			`{"access_token":"at","refresh_token":"rt","id_token":%q,"expires_in":3600,"scope":"openid email"}`,
			mintIDToken("u@x.com", true)))
	}))
	defer srv.Close()

	gp := New(Conf{ClientID: "cid", ClientSecret: "sec", TokenURL: srv.URL})
	cred, err := gp.exchangeCode("the-code", "http://localhost:1234")
	if err != nil {
		t.Fatalf("exchangeCode: %v", err)
	}
	if cred.Account != "u@x.com" || cred.AccessToken != "at" || cred.RefreshToken != "rt" {
		t.Fatalf("bad cred: %+v", cred)
	}
}

// TestExchangeCode_RejectsUnverified grounds the verified-email guard on the
// real HTTP exchange path (not just the pure-function unit test).
func TestExchangeCode_RejectsUnverified(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, fmt.Sprintf(
			`{"access_token":"at","refresh_token":"rt","id_token":%q,"expires_in":3600}`,
			mintIDToken("u@x.com", false)))
	}))
	defer srv.Close()

	gp := New(Conf{TokenURL: srv.URL})
	if _, err := gp.exchangeCode("c", "http://localhost:1234"); err == nil {
		t.Fatal("expected unverified-email rejection on the real exchange path")
	}
}
