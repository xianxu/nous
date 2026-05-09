package proxy

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/xianxu/nous/lib/provider/vault"
	"github.com/xianxu/nous/lib/provider/vault/memory"
)

// mockRefresher simulates token refresh for testing.
type mockRefresher struct {
	callCount int
	newToken  string
	newExpiry time.Time
	err       error
}

func (m *mockRefresher) Refresh(cred *vault.Credential) (*vault.Credential, error) {
	m.callCount++
	if m.err != nil {
		return nil, m.err
	}
	return &vault.Credential{
		Provider:     cred.Provider,
		Account:      cred.Account,
		AccessToken:  m.newToken,
		RefreshToken: cred.RefreshToken,
		Expiry:       m.newExpiry,
		Scopes:       cred.Scopes,
	}, nil
}

func TestRefresh_ExpiredTokenGetsRefreshed(t *testing.T) {
	now, advance := mockClock(baseTime)

	store := memory.New()
	_ = store.Set(&vault.Credential{
		Provider:     "test",
		Account:      "user",
		AccessToken:  "old-tok",
		RefreshToken: "refresh-tok",
		Expiry:       baseTime.Add(5 * time.Minute),
	})

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, r.Header.Get("Authorization"))
	}))
	defer upstream.Close()

	upstreamURL, _ := url.Parse(upstream.URL)
	hostname := upstreamURL.Hostname()
	HostToProvider[hostname] = &Provider{Name: "test", Auth: AuthBearer}
	defer delete(HostToProvider, hostname)

	refresher := &mockRefresher{
		newToken:  "refreshed-tok",
		newExpiry: baseTime.Add(15 * time.Minute),
	}

	srv := &Server{
		Vault:      store,
		Audit:      NopAuditLog(),
		CA:         testCA(t),
		Now:        now,
		Refreshers: map[string]Refresher{"test": refresher},
	}
	proxyServer := httptest.NewServer(srv)
	defer proxyServer.Close()

	proxyURL, _ := url.Parse(proxyServer.URL)
	client := &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)},
	}

	// First request: token is valid, served from vault.
	resp, _ := client.Get("http://" + upstreamURL.Host + "/test")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "Bearer old-tok" {
		t.Fatalf("first request: got %q, want 'Bearer old-tok'", body)
	}
	if refresher.callCount != 0 {
		t.Fatalf("should not have refreshed yet, callCount=%d", refresher.callCount)
	}

	// Advance past expiry (within grace period).
	advance(4*time.Minute + 31*time.Second)

	// Second request: token expired, should trigger refresh.
	resp, _ = client.Get("http://" + upstreamURL.Host + "/test")
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "Bearer refreshed-tok" {
		t.Fatalf("after expiry: got %q, want 'Bearer refreshed-tok'", body)
	}
	if refresher.callCount != 1 {
		t.Fatalf("expected 1 refresh call, got %d", refresher.callCount)
	}

	// Third request: refreshed token cached, no more refresh calls.
	resp, _ = client.Get("http://" + upstreamURL.Host + "/test")
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "Bearer refreshed-tok" {
		t.Fatalf("cached refresh: got %q, want 'Bearer refreshed-tok'", body)
	}
	if refresher.callCount != 1 {
		t.Fatalf("should not refresh again, callCount=%d", refresher.callCount)
	}
}

func TestRefresh_FailureFallsBackToExpiredToken(t *testing.T) {
	now, advance := mockClock(baseTime)

	store := memory.New()
	_ = store.Set(&vault.Credential{
		Provider:     "test",
		Account:      "user",
		AccessToken:  "old-tok",
		RefreshToken: "refresh-tok",
		Expiry:       baseTime.Add(5 * time.Minute),
	})

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, r.Header.Get("Authorization"))
	}))
	defer upstream.Close()

	upstreamURL, _ := url.Parse(upstream.URL)
	hostname := upstreamURL.Hostname()
	HostToProvider[hostname] = &Provider{Name: "test", Auth: AuthBearer}
	defer delete(HostToProvider, hostname)

	refresher := &mockRefresher{err: fmt.Errorf("network error")}

	srv := &Server{
		Vault:      store,
		Audit:      NopAuditLog(),
		CA:         testCA(t),
		Now:        now,
		Refreshers: map[string]Refresher{"test": refresher},
	}
	proxyServer := httptest.NewServer(srv)
	defer proxyServer.Close()

	proxyURL, _ := url.Parse(proxyServer.URL)
	client := &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)},
	}

	// Advance past expiry.
	advance(10 * time.Minute)

	// Should fall back to expired token.
	resp, _ := client.Get("http://" + upstreamURL.Host + "/test")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "Bearer old-tok" {
		t.Fatalf("fallback: got %q, want 'Bearer old-tok'", body)
	}
}

func TestRefresh_NoRefresherFallsBack(t *testing.T) {
	now, advance := mockClock(baseTime)

	store := memory.New()
	_ = store.Set(&vault.Credential{
		Provider:     "test",
		Account:      "user",
		AccessToken:  "old-tok",
		RefreshToken: "refresh-tok",
		Expiry:       baseTime.Add(5 * time.Minute),
	})

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, r.Header.Get("Authorization"))
	}))
	defer upstream.Close()

	upstreamURL, _ := url.Parse(upstream.URL)
	hostname := upstreamURL.Hostname()
	HostToProvider[hostname] = &Provider{Name: "test", Auth: AuthBearer}
	defer delete(HostToProvider, hostname)

	// No refreshers configured.
	srv := &Server{
		Vault: store,
		Audit: NopAuditLog(),
		CA:    testCA(t),
		Now:   now,
	}
	proxyServer := httptest.NewServer(srv)
	defer proxyServer.Close()

	proxyURL, _ := url.Parse(proxyServer.URL)
	client := &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)},
	}

	advance(10 * time.Minute)

	// Should fall back to expired token.
	resp, _ := client.Get("http://" + upstreamURL.Host + "/test")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "Bearer old-tok" {
		t.Fatalf("no refresher: got %q, want 'Bearer old-tok'", body)
	}
}

func TestRefresh_PersistsNewTokenToVault(t *testing.T) {
	now, advance := mockClock(baseTime)

	store := memory.New()
	_ = store.Set(&vault.Credential{
		Provider:     "test",
		Account:      "user",
		AccessToken:  "old-tok",
		RefreshToken: "old-refresh",
		Expiry:       baseTime.Add(5 * time.Minute),
	})

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "ok")
	}))
	defer upstream.Close()

	upstreamURL, _ := url.Parse(upstream.URL)
	hostname := upstreamURL.Hostname()
	HostToProvider[hostname] = &Provider{Name: "test", Auth: AuthBearer}
	defer delete(HostToProvider, hostname)

	refresher := &mockRefresher{
		newToken:  "new-tok",
		newExpiry: baseTime.Add(15 * time.Minute),
	}

	srv := &Server{
		Vault:      store,
		Audit:      NopAuditLog(),
		CA:         testCA(t),
		Now:        now,
		Refreshers: map[string]Refresher{"test": refresher},
	}
	proxyServer := httptest.NewServer(srv)
	defer proxyServer.Close()

	proxyURL, _ := url.Parse(proxyServer.URL)
	client := &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)},
	}

	advance(10 * time.Minute)

	resp, _ := client.Get("http://" + upstreamURL.Host + "/test")
	resp.Body.Close()

	// Verify the vault was updated with the new token.
	cred, err := store.Get("test", "user")
	if err != nil {
		t.Fatal(err)
	}
	if cred.AccessToken != "new-tok" {
		t.Errorf("vault should have new token, got %q", cred.AccessToken)
	}
}
