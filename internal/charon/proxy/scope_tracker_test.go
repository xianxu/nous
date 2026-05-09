package proxy

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/xianxu/nous/internal/charon/oauth"
	"github.com/xianxu/nous/internal/charon/vault"
	"github.com/xianxu/nous/internal/charon/vault/memory"
)

func TestScopeTracker_Track(t *testing.T) {
	st := NewScopeTracker(100, 24*time.Hour)

	st.Track("google", "user@gmail.com", []string{"calendar.readonly"})
	st.Track("google", "user@gmail.com", []string{"calendar.readonly"}) // increment count

	denials := st.Denials("", "")
	if len(denials) != 1 {
		t.Fatalf("expected 1 denial, got %d", len(denials))
	}
	if denials[0].Count != 2 {
		t.Errorf("expected count 2, got %d", denials[0].Count)
	}
	if denials[0].Scope != "calendar.readonly" {
		t.Errorf("expected scope 'calendar.readonly', got %q", denials[0].Scope)
	}
}

func TestScopeTracker_MultipleScopes(t *testing.T) {
	st := NewScopeTracker(100, 24*time.Hour)

	st.Track("google", "user@gmail.com", []string{"calendar.readonly", "drive.readonly"})

	denials := st.Denials("", "")
	if len(denials) != 2 {
		t.Fatalf("expected 2 denials, got %d", len(denials))
	}
}

func TestScopeTracker_FilterByProvider(t *testing.T) {
	st := NewScopeTracker(100, 24*time.Hour)

	st.Track("google", "user@gmail.com", []string{"calendar.readonly"})
	st.Track("dropbox", "user@gmail.com", []string{"files.content.read"})

	google := st.Denials("google", "")
	if len(google) != 1 {
		t.Fatalf("expected 1 google denial, got %d", len(google))
	}
	if google[0].Provider != "google" {
		t.Errorf("expected provider 'google', got %q", google[0].Provider)
	}
}

func TestScopeTracker_FilterByAccount(t *testing.T) {
	st := NewScopeTracker(100, 24*time.Hour)

	st.Track("google", "alice@gmail.com", []string{"calendar.readonly"})
	st.Track("google", "bob@gmail.com", []string{"drive.readonly"})

	alice := st.Denials("", "alice@gmail.com")
	if len(alice) != 1 {
		t.Fatalf("expected 1 denial for alice, got %d", len(alice))
	}
}

func TestScopeTracker_Expiry(t *testing.T) {
	st := NewScopeTracker(100, 1*time.Hour)
	now := time.Now()
	st.now = func() time.Time { return now }

	st.Track("google", "user@gmail.com", []string{"calendar.readonly"})

	// Advance past expiry.
	st.now = func() time.Time { return now.Add(2 * time.Hour) }

	denials := st.Denials("", "")
	if len(denials) != 0 {
		t.Fatalf("expected 0 denials after expiry, got %d", len(denials))
	}
}

func TestScopeTracker_MaxSize(t *testing.T) {
	st := NewScopeTracker(2, 24*time.Hour)
	now := time.Now()
	st.now = func() time.Time { return now }

	st.Track("google", "user@gmail.com", []string{"scope1"})
	now = now.Add(1 * time.Second)
	st.now = func() time.Time { return now }
	st.Track("google", "user@gmail.com", []string{"scope2"})
	now = now.Add(1 * time.Second)
	st.now = func() time.Time { return now }
	st.Track("google", "user@gmail.com", []string{"scope3"}) // should evict scope1

	denials := st.Denials("", "")
	if len(denials) != 2 {
		t.Fatalf("expected 2 denials (maxSize), got %d", len(denials))
	}
	// scope1 should have been evicted (oldest).
	for _, d := range denials {
		if d.Scope == "scope1" {
			t.Error("scope1 should have been evicted")
		}
	}
}

func TestFindMissingScopes(t *testing.T) {
	tests := []struct {
		requested []string
		granted   []string
		want      []string
	}{
		{[]string{"a", "b"}, []string{"a", "b", "c"}, nil},
		{[]string{"a", "b", "d"}, []string{"a", "b", "c"}, []string{"d"}},
		{[]string{"x"}, []string{}, []string{"x"}},
		{[]string{}, []string{"a"}, nil},
	}
	for _, tt := range tests {
		got := findMissingScopes(tt.requested, tt.granted, nil)
		if len(got) != len(tt.want) {
			t.Errorf("findMissingScopes(%v, %v) = %v, want %v", tt.requested, tt.granted, got, tt.want)
		}
	}
}

func TestFindMissingScopes_Normalize(t *testing.T) {
	// Agent declares short name; credential has the full URL Google issues.
	// Without a normalizer, findMissingScopes treats them as different
	// strings and reports the scope as missing — which is the bug agents
	// kept hitting in #000005 manual testing.
	requested := []string{"gmail.readonly"}
	granted := []string{"https://www.googleapis.com/auth/gmail.readonly"}

	identity := func(s string) string { return s }
	if got := findMissingScopes(requested, granted, identity); len(got) == 0 {
		t.Errorf("identity normalize: expected mismatch, got none")
	}

	canon := func(s string) string {
		if s == "gmail.readonly" {
			return "https://www.googleapis.com/auth/gmail.readonly"
		}
		return s
	}
	if got := findMissingScopes(requested, granted, canon); len(got) != 0 {
		t.Errorf("canon normalize: got %v missing, want none", got)
	}
}

// TestFindMissingScopes_SetSemantics covers cases where set-membership
// matters more than naive equality: short-vs-full forms on either side,
// duplicates in the request, and the OIDC userinfo.email rewrite that
// Google performs.
func TestFindMissingScopes_SetSemantics(t *testing.T) {
	// Use the real google resolver so the test exercises the actual
	// canonicalization path the proxy uses in production.
	norm := oauth.ResolveGoogleScope

	tests := []struct {
		name      string
		requested []string
		granted   []string
		want      []string // expected missing (in original requested form)
	}{
		{
			name:      "short request matches full granted",
			requested: []string{"gmail.readonly"},
			granted:   []string{"https://www.googleapis.com/auth/gmail.readonly"},
			want:      nil,
		},
		{
			name:      "full request matches short granted",
			requested: []string{"https://www.googleapis.com/auth/gmail.readonly"},
			granted:   []string{"gmail.readonly"},
			want:      nil,
		},
		{
			name:      "OIDC email rewrite — short on agent side, full on credential side",
			requested: []string{"email"},
			granted:   []string{"openid", "https://www.googleapis.com/auth/userinfo.email"},
			want:      nil,
		},
		{
			name:      "openid stays openid (Google does not rewrite)",
			requested: []string{"openid"},
			granted:   []string{"openid"},
			want:      nil,
		},
		{
			name:      "mixed forms in same request — both should resolve",
			requested: []string{"gmail.readonly", "https://www.googleapis.com/auth/calendar.readonly"},
			granted:   []string{"https://www.googleapis.com/auth/gmail.readonly", "calendar.readonly"},
			want:      nil,
		},
		{
			name:      "missing reports original form, not canonical",
			requested: []string{"gmail.send"},
			granted:   []string{"https://www.googleapis.com/auth/gmail.readonly"},
			want:      []string{"gmail.send"}, // 407 should show what the agent asked for
		},
		{
			name:      "duplicate request entries — both reported as missing",
			requested: []string{"gmail.send", "gmail.send"},
			granted:   []string{},
			want:      []string{"gmail.send", "gmail.send"},
		},
		{
			name:      "unknown scope (not in catalog) passes through unchanged",
			requested: []string{"https://example.com/random"},
			granted:   []string{"https://example.com/random"},
			want:      nil,
		},
		{
			name:      "unknown scope missing from granted",
			requested: []string{"https://example.com/random"},
			granted:   []string{"openid"},
			want:      []string{"https://example.com/random"},
		},
		{
			name:      "partial overlap — only mismatched scope reported",
			requested: []string{"gmail.readonly", "drive.readonly"},
			granted:   []string{"https://www.googleapis.com/auth/gmail.readonly"},
			want:      []string{"drive.readonly"},
		},
		{
			name:      "empty granted set — every requested scope is missing",
			requested: []string{"gmail.readonly", "calendar.readonly"},
			granted:   nil,
			want:      []string{"gmail.readonly", "calendar.readonly"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := findMissingScopes(tt.requested, tt.granted, norm)
			if !equalScopeSlices(got, tt.want) {
				t.Errorf("findMissingScopes(%v, %v) = %v, want %v",
					tt.requested, tt.granted, got, tt.want)
			}
		})
	}
}

// TestFindMissingScopes_NilNormalizeIsIdentity verifies that passing a
// nil normalize function is equivalent to direct string equality — the
// pre-#000005 behavior, kept available for non-Google providers until
// #000006 lands proper provider abstraction.
func TestFindMissingScopes_NilNormalizeIsIdentity(t *testing.T) {
	got := findMissingScopes(
		[]string{"gmail.readonly"},
		[]string{"https://www.googleapis.com/auth/gmail.readonly"},
		nil,
	)
	if len(got) != 1 || got[0] != "gmail.readonly" {
		t.Errorf("nil normalize: got %v, want missing because no canonicalization", got)
	}
}

func equalScopeSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestHTTPProxy_ScopeEnforcement(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, r.Header.Get("Authorization"))
	}))
	defer upstream.Close()

	upstreamURL, _ := url.Parse(upstream.URL)
	hostname := upstreamURL.Hostname()
	HostToProvider[hostname] = &Provider{Name: "test-scope", Auth: AuthBearer, HasScopes: true}
	defer delete(HostToProvider, hostname)

	store := memory.New()
	_ = store.Set(&vault.Credential{
		Provider:    "test-scope",
		Account:     "user@gmail.com",
		AccessToken: "tok",
		Scopes:      []string{"gmail.readonly", "calendar.readonly"},
	})

	tracker := NewScopeTracker(100, 24*time.Hour)
	srv := &Server{
		Vault:        store,
		Audit:        NopAuditLog(),
		CA:           testCA(t),
		ScopeTracker: tracker,
	}
	proxyServer := httptest.NewServer(srv)
	defer proxyServer.Close()

	proxyURL, _ := url.Parse(proxyServer.URL)
	client := &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)},
	}

	t.Run("scope granted", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "http://"+upstreamURL.Host+"/test", nil)
		req.Header.Set("X-Charon-Scope", "gmail.readonly")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Errorf("expected 200, got %d", resp.StatusCode)
		}
	})

	t.Run("scope missing", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "http://"+upstreamURL.Host+"/test", nil)
		req.Header.Set("X-Charon-Scope", "drive.readonly")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 407 {
			t.Errorf("expected 407, got %d", resp.StatusCode)
		}

		var body struct {
			Error   string   `json:"error"`
			Missing []string `json:"missing"`
			Fix     string   `json:"fix"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}
		if body.Error != "scope_missing" {
			t.Errorf("expected error 'scope_missing', got %q", body.Error)
		}
		if len(body.Missing) != 1 || body.Missing[0] != "drive.readonly" {
			t.Errorf("expected missing ['drive.readonly'], got %v", body.Missing)
		}
	})

	t.Run("no scope header passes through", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "http://"+upstreamURL.Host+"/test", nil)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if string(body) != "Bearer tok" {
			t.Errorf("expected 'Bearer tok', got %q", body)
		}
	})

	t.Run("denial tracked", func(t *testing.T) {
		denials := tracker.Denials("test-scope", "")
		if len(denials) != 1 {
			t.Fatalf("expected 1 tracked denial, got %d", len(denials))
		}
		if denials[0].Scope != "drive.readonly" {
			t.Errorf("expected tracked scope 'drive.readonly', got %q", denials[0].Scope)
		}
	})
}

// TestHTTPProxy_GoogleScopeNormalization is the end-to-end regression test
// for the bug agents kept hitting in #000005 manual testing: agent declares
// X-Charon-Scope with short names (gmail.readonly) while the credential
// holds the full URL form Google issues. The proxy must canonicalize on
// both sides via the google scope catalog before comparing.
func TestHTTPProxy_GoogleScopeNormalization(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "upstream-ok")
	}))
	defer upstream.Close()

	upstreamURL, _ := url.Parse(upstream.URL)
	hostname := upstreamURL.Hostname()
	// Provider.Name = "google" so scopeNormalizer wires in
	// oauth.ResolveGoogleScope.
	HostToProvider[hostname] = &Provider{Name: "google", Auth: AuthBearer, HasScopes: true}
	defer delete(HostToProvider, hostname)

	store := memory.New()
	_ = store.Set(&vault.Credential{
		Provider:    "google",
		Account:     "user@gmail.com",
		AccessToken: "tok",
		// Stored as full URLs — that's what Google returns in the OAuth
		// token response's `scope` field.
		Scopes: []string{
			"openid",
			"https://www.googleapis.com/auth/userinfo.email",
			"https://www.googleapis.com/auth/gmail.readonly",
		},
	})

	srv := &Server{
		Vault:        store,
		Audit:        NopAuditLog(),
		CA:           testCA(t),
		ScopeTracker: NewScopeTracker(100, 24*time.Hour),
	}
	proxyServer := httptest.NewServer(srv)
	defer proxyServer.Close()
	proxyURL, _ := url.Parse(proxyServer.URL)
	client := &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)},
	}

	cases := []struct {
		name           string
		scopeHeader    string
		wantStatus     int
		wantUpstream   bool // true = expect to reach upstream (200)
		wantMissingFix string // empty if not 407
	}{
		{
			name:         "short name resolves to granted full URL",
			scopeHeader:  "gmail.readonly",
			wantStatus:   200,
			wantUpstream: true,
		},
		{
			name:         "full URL matches granted full URL",
			scopeHeader:  "https://www.googleapis.com/auth/gmail.readonly",
			wantStatus:   200,
			wantUpstream: true,
		},
		{
			name:         "OIDC short name 'email' matches userinfo.email URL",
			scopeHeader:  "email",
			wantStatus:   200,
			wantUpstream: true,
		},
		{
			name:         "openid matches openid (no rewrite)",
			scopeHeader:  "openid",
			wantStatus:   200,
			wantUpstream: true,
		},
		{
			name:         "multiple scopes, all granted via short names",
			scopeHeader:  "gmail.readonly,email,openid",
			wantStatus:   200,
			wantUpstream: true,
		},
		{
			name:           "scope not granted reports original short name in 407",
			scopeHeader:    "gmail.send",
			wantStatus:     407,
			wantUpstream:   false,
			wantMissingFix: "gmail.send",
		},
		{
			name:           "partial overlap — only missing scope reported",
			scopeHeader:    "gmail.readonly,drive.readonly",
			wantStatus:     407,
			wantUpstream:   false,
			wantMissingFix: "drive.readonly",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req, _ := http.NewRequest("GET", "http://"+upstreamURL.Host+"/test", nil)
			req.Header.Set("X-Charon-Account", "user@gmail.com")
			req.Header.Set("X-Charon-Scope", tc.scopeHeader)

			resp, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != tc.wantStatus {
				body, _ := io.ReadAll(resp.Body)
				t.Errorf("status = %d, want %d. body=%s", resp.StatusCode, tc.wantStatus, body)
				return
			}

			if tc.wantUpstream {
				body, _ := io.ReadAll(resp.Body)
				if string(body) != "upstream-ok" {
					t.Errorf("expected upstream body, got %q", body)
				}
			}

			if tc.wantMissingFix != "" {
				var got struct {
					Error   string   `json:"error"`
					Missing []string `json:"missing"`
					Fix     string   `json:"fix"`
				}
				if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
					t.Fatalf("decode 407 body: %v", err)
				}
				if got.Error != "scope_missing" {
					t.Errorf("error = %q, want scope_missing", got.Error)
				}
				found := false
				for _, m := range got.Missing {
					if m == tc.wantMissingFix {
						found = true
					}
				}
				if !found {
					t.Errorf("missing %v does not contain %q", got.Missing, tc.wantMissingFix)
				}
			}
		})
	}
}

func TestHTTPProxy_MultipleRequestedScopes(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "ok")
	}))
	defer upstream.Close()

	upstreamURL, _ := url.Parse(upstream.URL)
	hostname := upstreamURL.Hostname()
	HostToProvider[hostname] = &Provider{Name: "test-multi", Auth: AuthBearer, HasScopes: true}
	defer delete(HostToProvider, hostname)

	store := memory.New()
	_ = store.Set(&vault.Credential{
		Provider:    "test-multi",
		Account:     "user",
		AccessToken: "tok",
		Scopes:      []string{"a"},
	})

	srv := &Server{
		Vault:        store,
		Audit:        NopAuditLog(),
		CA:           testCA(t),
		ScopeTracker: NewScopeTracker(100, 24*time.Hour),
	}
	proxyServer := httptest.NewServer(srv)
	defer proxyServer.Close()

	proxyURL, _ := url.Parse(proxyServer.URL)
	client := &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)},
	}

	// Request multiple scopes, some missing.
	req, _ := http.NewRequest("GET", "http://"+upstreamURL.Host+"/test", nil)
	req.Header.Set("X-Charon-Scope", "a, b, c")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 407 {
		t.Fatalf("expected 407, got %d", resp.StatusCode)
	}
	var body struct {
		Missing []string `json:"missing"`
	}
	json.NewDecoder(resp.Body).Decode(&body)
	if len(body.Missing) != 2 {
		t.Errorf("expected 2 missing scopes, got %v", body.Missing)
	}
}

func TestScopeDeniedEndpoint(t *testing.T) {
	tracker := NewScopeTracker(100, 24*time.Hour)
	tracker.Track("google", "user@gmail.com", []string{"calendar.readonly"})

	srv := &Server{
		Vault:        memory.New(),
		Audit:        NopAuditLog(),
		CA:           testCA(t),
		ScopeTracker: tracker,
	}
	proxyServer := httptest.NewServer(srv)
	defer proxyServer.Close()

	resp, err := http.Get(proxyServer.URL + "/scopes/denied?provider=google")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var denials []ScopeDenial
	json.NewDecoder(resp.Body).Decode(&denials)
	if len(denials) != 1 {
		t.Fatalf("expected 1 denial, got %d", len(denials))
	}
	if denials[0].Scope != "calendar.readonly" {
		t.Errorf("expected 'calendar.readonly', got %q", denials[0].Scope)
	}
}

func TestScopeDeniedEndpoint_Empty(t *testing.T) {
	srv := &Server{
		Vault: memory.New(),
		Audit: NopAuditLog(),
		CA:    testCA(t),
		// No ScopeTracker.
	}
	proxyServer := httptest.NewServer(srv)
	defer proxyServer.Close()

	resp, err := http.Get(proxyServer.URL + "/scopes/denied")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if string(body) != "[]" {
		t.Errorf("expected empty array '[]', got %q", body)
	}
}
