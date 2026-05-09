package proxy

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xianxu/nous/internal/charon/vault"
	"github.com/xianxu/nous/internal/charon/vault/memory"
)

var baseTime = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

// mockClock returns a Now function and a way to advance time.
func mockClock(start time.Time) (now func() time.Time, advance func(d time.Duration)) {
	var offset atomic.Int64
	now = func() time.Time {
		return start.Add(time.Duration(offset.Load()))
	}
	advance = func(d time.Duration) {
		offset.Add(int64(d))
	}
	return
}

// setupProxyTest creates a proxy with a fake upstream, store, and mock clock.
// The upstream echoes the Authorization header.
func setupProxyTest(t *testing.T, store vault.Store) (client *http.Client, srv *Server, upstream *httptest.Server, now func() time.Time, advance func(d time.Duration)) {
	t.Helper()

	upstream = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, r.Header.Get("Authorization"))
	}))
	t.Cleanup(upstream.Close)

	upstreamURL, _ := url.Parse(upstream.URL)
	hostname := upstreamURL.Hostname()
	HostToProvider[hostname] = &Provider{Name: "test", Auth: AuthBearer}
	t.Cleanup(func() { delete(HostToProvider, hostname) })

	now, advance = mockClock(baseTime)
	srv = &Server{
		Vault: store,
		Audit: NopAuditLog(),
		CA:    testCA(t),
		Now:   now,
	}
	proxyServer := httptest.NewServer(srv)
	t.Cleanup(proxyServer.Close)

	proxyURL, _ := url.Parse(proxyServer.URL)
	client = &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)},
	}
	return
}

func doGet(t *testing.T, client *http.Client, url string) string {
	t.Helper()
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return string(body)
}

func TestCache_NoExpiry_CachedForever(t *testing.T) {
	store := memory.New()
	_ = store.Set(&vault.Credential{
		Provider: "test", Account: "user", AccessToken: "tok-1",
		// No Expiry set → zero value → never expires.
	})

	client, _, upstream, _, advance := setupProxyTest(t, store)

	got := doGet(t, client, upstream.URL+"/test")
	if got != "Bearer tok-1" {
		t.Fatalf("first request: got %q, want 'Bearer tok-1'", got)
	}

	// Update token in vault.
	_ = store.Set(&vault.Credential{
		Provider: "test", Account: "user", AccessToken: "tok-2",
	})

	// Even after 24h, cache still returns old token (no expiry).
	advance(24 * time.Hour)
	got = doGet(t, client, upstream.URL+"/test")
	if got != "Bearer tok-1" {
		t.Fatalf("after 24h without expiry: got %q, want 'Bearer tok-1' (cached)", got)
	}
}

func TestCache_WithExpiry_RefetchAfterExpiry(t *testing.T) {
	store := memory.New()
	_ = store.Set(&vault.Credential{
		Provider: "test", Account: "user", AccessToken: "tok-1",
		Expiry: baseTime.Add(10 * time.Minute),
	})

	client, _, upstream, _, advance := setupProxyTest(t, store)

	// First request caches the token.
	got := doGet(t, client, upstream.URL+"/test")
	if got != "Bearer tok-1" {
		t.Fatalf("first request: got %q, want 'Bearer tok-1'", got)
	}

	// Update token in vault (simulating a refresh).
	_ = store.Set(&vault.Credential{
		Provider: "test", Account: "user", AccessToken: "tok-2",
		Expiry: baseTime.Add(20 * time.Minute),
	})

	// 5 minutes later: still within expiry, cache hit.
	advance(5 * time.Minute)
	got = doGet(t, client, upstream.URL+"/test")
	if got != "Bearer tok-1" {
		t.Fatalf("at t+5m: got %q, want 'Bearer tok-1' (cached)", got)
	}

	// 9m31s later: within 30s grace period → cache miss → refetch.
	advance(4*time.Minute + 31*time.Second)
	got = doGet(t, client, upstream.URL+"/test")
	if got != "Bearer tok-2" {
		t.Fatalf("at t+9m31s (grace period): got %q, want 'Bearer tok-2' (refetched)", got)
	}
}

func TestCache_TokenExpiresExactlyAtGraceBoundary(t *testing.T) {
	store := memory.New()
	_ = store.Set(&vault.Credential{
		Provider: "test", Account: "user", AccessToken: "tok-1",
		Expiry: baseTime.Add(5 * time.Minute),
	})

	client, _, upstream, _, advance := setupProxyTest(t, store)

	doGet(t, client, upstream.URL+"/test") // cache it

	_ = store.Set(&vault.Credential{
		Provider: "test", Account: "user", AccessToken: "tok-2",
		Expiry: baseTime.Add(15 * time.Minute),
	})

	// At 4m29s: 31s remaining > 30s grace → cached.
	advance(4*time.Minute + 29*time.Second)
	got := doGet(t, client, upstream.URL+"/test")
	if got != "Bearer tok-1" {
		t.Fatalf("at 4m29s (31s remaining): got %q, want 'Bearer tok-1' (cached)", got)
	}

	// At exactly 4m30s: 30s remaining == grace period → refetch (conservative).
	advance(1 * time.Second)
	got = doGet(t, client, upstream.URL+"/test")
	if got != "Bearer tok-2" {
		t.Fatalf("at exact grace boundary (30s remaining): got %q, want 'Bearer tok-2' (refetched)", got)
	}
}

func TestCache_ClearInvalidatesEverything(t *testing.T) {
	store := memory.New()
	_ = store.Set(&vault.Credential{
		Provider: "test", Account: "user", AccessToken: "tok-1",
	})

	client, srv, upstream, _, _ := setupProxyTest(t, store)

	doGet(t, client, upstream.URL+"/test") // cache it

	// Update vault and clear cache.
	_ = store.Set(&vault.Credential{
		Provider: "test", Account: "user", AccessToken: "tok-2",
	})
	srv.ClearCache()

	got := doGet(t, client, upstream.URL+"/test")
	if got != "Bearer tok-2" {
		t.Fatalf("after cache clear: got %q, want 'Bearer tok-2'", got)
	}
}

func TestCache_AccountResolution_CachedUntilClear(t *testing.T) {
	store := memory.New()
	_ = store.Set(&vault.Credential{
		Provider: "test", Account: "alice", AccessToken: "tok-alice",
	})

	client, srv, upstream, _, _ := setupProxyTest(t, store)

	// First request: auto-resolves single account "alice".
	got := doGet(t, client, upstream.URL+"/test")
	if got != "Bearer tok-alice" {
		t.Fatalf("got %q, want 'Bearer tok-alice'", got)
	}

	// Add a second account. Without cache clear, still resolves to alice.
	_ = store.Set(&vault.Credential{
		Provider: "test", Account: "bob", AccessToken: "tok-bob",
	})
	got = doGet(t, client, upstream.URL+"/test")
	if got != "Bearer tok-alice" {
		t.Fatalf("without cache clear: got %q, want 'Bearer tok-alice' (cached)", got)
	}

	// After cache clear, should fail (multiple accounts, no header).
	srv.ClearCache()
	resp, err := client.Get(upstream.URL + "/test")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusProxyAuthRequired {
		t.Fatalf("after adding 2nd account + cache clear: got status %d, want 407", resp.StatusCode)
	}
}

func TestCache_ExpiredToken_NotCached(t *testing.T) {
	store := memory.New()
	// Token already expired at baseTime.
	_ = store.Set(&vault.Credential{
		Provider: "test", Account: "user", AccessToken: "expired-tok",
		Expiry: baseTime.Add(-1 * time.Minute),
	})

	client, _, upstream, _, _ := setupProxyTest(t, store)

	// The expired token should still be returned (fallback path in resolveToken).
	// But it should NOT be cached — next vault update should take effect.
	got := doGet(t, client, upstream.URL+"/test")
	if got != "Bearer expired-tok" {
		t.Fatalf("got %q, want 'Bearer expired-tok' (fallback)", got)
	}

	// Update with a fresh token.
	_ = store.Set(&vault.Credential{
		Provider: "test", Account: "user", AccessToken: "fresh-tok",
		Expiry: baseTime.Add(10 * time.Minute),
	})

	// Should immediately get the new token (old one wasn't cached).
	got = doGet(t, client, upstream.URL+"/test")
	if got != "Bearer fresh-tok" {
		t.Fatalf("after update: got %q, want 'Bearer fresh-tok'", got)
	}
}

func TestCache_VaultFetchCount(t *testing.T) {
	// Verify that repeated requests don't hit the vault after caching.
	inner := memory.New()
	_ = inner.Set(&vault.Credential{
		Provider: "test", Account: "user", AccessToken: "tok",
		Expiry: baseTime.Add(10 * time.Minute),
	})
	counting := &countingStore{Store: inner}

	client, _, upstream, _, _ := setupProxyTest(t, counting)

	// First request: 1 List (account resolve) + 1 Get (token fetch).
	doGet(t, client, upstream.URL+"/test")
	if counting.gets.Load() != 1 {
		t.Errorf("after 1st request: gets=%d, want 1", counting.gets.Load())
	}
	if counting.lists.Load() != 1 {
		t.Errorf("after 1st request: lists=%d, want 1", counting.lists.Load())
	}

	// 10 more requests: all from cache, no vault calls.
	for i := 0; i < 10; i++ {
		doGet(t, client, upstream.URL+"/test")
	}
	if counting.gets.Load() != 1 {
		t.Errorf("after 11 requests: gets=%d, want 1", counting.gets.Load())
	}
	if counting.lists.Load() != 1 {
		t.Errorf("after 11 requests: lists=%d, want 1", counting.lists.Load())
	}
}

// countingStore wraps a vault.Store and counts Get/List calls.
type countingStore struct {
	vault.Store
	gets  atomic.Int64
	lists atomic.Int64
}

func (c *countingStore) Get(provider, account string) (*vault.Credential, error) {
	c.gets.Add(1)
	return c.Store.Get(provider, account)
}

func (c *countingStore) List() ([]*vault.Credential, error) {
	c.lists.Add(1)
	return c.Store.List()
}
