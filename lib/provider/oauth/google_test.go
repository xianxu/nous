package oauth

import (
	"encoding/base64"
	"io"
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
			got, err := parseIDTokenEmail(tt.token)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseIDTokenEmail() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("parseIDTokenEmail() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBuildAuthURL_LoginHint(t *testing.T) {
	gp := &GoogleProvider{clientID: "test-client-id", clientSecret: "test-secret"}

	t.Run("without login hint", func(t *testing.T) {
		u := gp.buildAuthURL("http://localhost:1234", []string{"openid"}, "", false)
		if containsParam(u, "login_hint") {
			t.Error("expected no login_hint parameter")
		}
	})

	t.Run("with login hint", func(t *testing.T) {
		u := gp.buildAuthURL("http://localhost:1234", []string{"openid"}, "user@gmail.com", false)
		if !containsParam(u, "login_hint") {
			t.Error("expected login_hint parameter")
		}
	})

	t.Run("forceFresh sets include_granted_scopes=false", func(t *testing.T) {
		u := gp.buildAuthURL("http://localhost:1234", []string{"openid"}, "", true)
		if !strings.Contains(u, "include_granted_scopes=false") {
			t.Errorf("expected include_granted_scopes=false in URL: %s", u)
		}
	})

	t.Run("forceFresh=false keeps incremental", func(t *testing.T) {
		u := gp.buildAuthURL("http://localhost:1234", []string{"openid"}, "", false)
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
// Drives the actual HTTP refresh against a stub token endpoint via
// reflection-free indirection: we override googleTokenURL just for
// the duration of this test.
func TestRefresh_PreservesSidecars(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"new-tok","expires_in":3600,"token_type":"Bearer"}`)
	}))
	defer srv.Close()

	orig := googleTokenURL
	googleTokenURL = srv.URL
	defer func() { googleTokenURL = orig }()

	gp, err := NewGoogleProvider()
	if err != nil {
		t.Fatalf("NewGoogleProvider: %v", err)
	}

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
