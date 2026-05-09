package anthropic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xianxu/nous/internal/charon/providers"
)

// fakeServer mounts the Anthropic Admin API endpoints we care about.
// Mirrors the OpenAI test server but with x-api-key auth, anthropic-
// version header check, and path-shaped org id.
type fakeServer struct {
	t          *testing.T
	expectKey  string
	orgID      string
	orgName    string
	workspaces map[string]workspaceResponse
	mintedKeys map[string]apiKeyMintResponse
	revoked    map[string]bool

	createCalls atomic.Int32
	revokeCalls atomic.Int32

	srv *httptest.Server
}

func newFakeServer(t *testing.T, adminKey string) *fakeServer {
	t.Helper()
	fs := &fakeServer{
		t:          t,
		expectKey:  adminKey,
		orgID:      "org_test_uuid",
		orgName:    "test-anthropic-org",
		workspaces: make(map[string]workspaceResponse),
		mintedKeys: make(map[string]apiKeyMintResponse),
		revoked:    make(map[string]bool),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/organizations/me", fs.handleOrgMe)
	mux.HandleFunc("/v1/organizations/", fs.handleOrgChild)
	fs.srv = httptest.NewServer(mux)
	t.Cleanup(fs.srv.Close)
	return fs
}

// authOK validates both x-api-key and anthropic-version headers.
// anthropic-version is required by the upstream — missing it returns
// 400 in production. We assert it here so a regression where charon
// forgets the header surfaces as a test failure.
func (fs *fakeServer) authOK(w http.ResponseWriter, r *http.Request) bool {
	if r.Header.Get("x-api-key") != fs.expectKey {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`))
		return false
	}
	if r.Header.Get("anthropic-version") == "" {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"invalid_request_error","message":"missing anthropic-version header"}}`))
		return false
	}
	return true
}

func (fs *fakeServer) handleOrgMe(w http.ResponseWriter, r *http.Request) {
	if !fs.authOK(w, r) {
		return
	}
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	_ = json.NewEncoder(w).Encode(orgResponse{ID: fs.orgID, Name: fs.orgName})
}

// handleOrgChild routes:
//   /v1/organizations/{org}/workspaces                       — GET list, POST create
//   /v1/organizations/{org}/workspaces/{ws}/api_keys         — POST mint
//   /v1/organizations/{org}/workspaces/{ws}/api_keys/{k}     — DELETE revoke
func (fs *fakeServer) handleOrgChild(w http.ResponseWriter, r *http.Request) {
	if !fs.authOK(w, r) {
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/v1/organizations/")
	parts := strings.Split(rest, "/")
	if len(parts) < 2 || parts[0] != fs.orgID || parts[1] != "workspaces" {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	switch len(parts) {
	case 2:
		fs.handleWorkspaces(w, r)
	case 4:
		if parts[3] != "api_keys" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		fs.handleMint(w, r, parts[2])
	case 5:
		if parts[3] != "api_keys" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		fs.handleRevoke(w, r, parts[2], parts[4])
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (fs *fakeServer) handleWorkspaces(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		out := workspacesListResponse{}
		for _, ws := range fs.workspaces {
			out.Data = append(out.Data, ws)
		}
		_ = json.NewEncoder(w).Encode(out)
	case http.MethodPost:
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["name"] == "" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"type":"error","error":{"type":"invalid_request_error","message":"name required"}}`))
			return
		}
		fs.createCalls.Add(1)
		ws := workspaceResponse{
			ID:   fmt.Sprintf("ws_%d", len(fs.workspaces)+1),
			Type: "workspace",
			Name: body["name"],
		}
		fs.workspaces[ws.ID] = ws
		_ = json.NewEncoder(w).Encode(ws)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (fs *fakeServer) handleMint(w http.ResponseWriter, r *http.Request, wsID string) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if _, ok := fs.workspaces[wsID]; !ok {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"invalid_request_error","message":"workspace not found"}}`))
		return
	}
	var body map[string]string
	_ = json.NewDecoder(r.Body).Decode(&body)
	k := apiKeyMintResponse{
		ID:             fmt.Sprintf("key_%d", len(fs.mintedKeys)+1),
		Type:           "api_key",
		Name:           body["name"],
		Key:            fmt.Sprintf("sk-ant-test-%s", body["name"]),
		PartialKeyHint: "sk-ant-…",
		WorkspaceID:    wsID,
	}
	fs.mintedKeys[k.ID] = k
	_ = json.NewEncoder(w).Encode(k)
}

func (fs *fakeServer) handleRevoke(w http.ResponseWriter, r *http.Request, wsID, keyID string) {
	if r.Method != http.MethodDelete {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	fs.revokeCalls.Add(1)
	if fs.revoked[keyID] {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if _, ok := fs.mintedKeys[keyID]; !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	fs.revoked[keyID] = true
	w.WriteHeader(http.StatusNoContent)
}

func newTestProvider(t *testing.T, adminKey string) (*Provider, *fakeServer) {
	t.Helper()
	fs := newFakeServer(t, adminKey)
	p := &Provider{BaseURL: fs.srv.URL, HTTP: fs.srv.Client()}
	return p, fs
}

// ── Tests ────────────────────────────────────────────────────────────

func TestProvider_NameType(t *testing.T) {
	p := New()
	if p.Name() != "anthropic" {
		t.Errorf("Name = %q", p.Name())
	}
	if p.Type() != "admin-key" {
		t.Errorf("Type = %q", p.Type())
	}
}

func TestProvider_DiscoverOrg_HappyPath(t *testing.T) {
	p, fs := newTestProvider(t, "sk-ant-admin")
	fs.orgID = "org_uuid_1234"
	fs.orgName = "anthropic-test-co"

	id, name, err := p.DiscoverOrg(context.Background(), "sk-ant-admin")
	if err != nil {
		t.Fatalf("DiscoverOrg: %v", err)
	}
	if id != "org_uuid_1234" || name != "anthropic-test-co" {
		t.Errorf("got %q/%q", id, name)
	}
	// Cached for subsequent calls.
	if v, ok := p.orgIDCache.Load("sk-ant-admin"); !ok || v.(string) != id {
		t.Error("DiscoverOrg should populate orgIDCache")
	}
}

func TestProvider_DiscoverOrg_InvalidKey(t *testing.T) {
	p, _ := newTestProvider(t, "sk-ant-correct")
	_, _, err := p.DiscoverOrg(context.Background(), "sk-ant-wrong")
	if !errors.Is(err, providers.ErrInvalidAdminKey) {
		t.Errorf("expected ErrInvalidAdminKey, got %v", err)
	}
}

func TestProvider_DiscoverOrg_EmptyKey(t *testing.T) {
	p := New()
	_, _, err := p.DiscoverOrg(context.Background(), "")
	if !errors.Is(err, providers.ErrInvalidAdminKey) {
		t.Errorf("expected ErrInvalidAdminKey, got %v", err)
	}
}

// anthropic-version is non-negotiable per Anthropic's API contract.
// This test asserts the header is emitted on every call by removing it
// at the server side and verifying the request errors.
func TestProvider_AnthropicVersionHeaderRequired(t *testing.T) {
	// Build a server that rejects requests missing anthropic-version.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("anthropic-version") == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		// Any path: ack with empty body so do() doesn't choke.
		_ = json.NewEncoder(w).Encode(map[string]string{"id": "org_x", "name": "n"})
	}))
	defer srv.Close()

	p := &Provider{BaseURL: srv.URL, HTTP: srv.Client()}
	if _, _, err := p.DiscoverOrg(context.Background(), "sk-ant"); err != nil {
		t.Fatalf("DiscoverOrg should send anthropic-version, got error: %v", err)
	}
}

func TestProvider_ListProjects_FiltersArchived(t *testing.T) {
	p, fs := newTestProvider(t, "sk-ant")
	fs.workspaces["ws_1"] = workspaceResponse{ID: "ws_1", Name: "active-one"}
	fs.workspaces["ws_2"] = workspaceResponse{ID: "ws_2", Name: "old", ArchivedAt: "2024-01-01T00:00:00Z"}
	fs.workspaces["ws_3"] = workspaceResponse{ID: "ws_3", Name: "active-two"}

	got, err := p.ListProjects(context.Background(), "sk-ant")
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("expected 2 active workspaces, got %d (%+v)", len(got), got)
	}
	for _, pr := range got {
		if pr.Name == "old" {
			t.Errorf("archived workspace leaked: %+v", pr)
		}
	}
}

func TestProvider_CreateProject_HappyPath(t *testing.T) {
	p, fs := newTestProvider(t, "sk-ant")
	pr, err := p.CreateProject(context.Background(), "sk-ant", "work-workspace")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if pr.ID == "" || pr.Name != "work-workspace" {
		t.Errorf("unexpected workspace: %+v", pr)
	}
	if !strings.HasPrefix(pr.ID, "ws_") {
		t.Errorf("workspace id missing ws_ prefix: %q", pr.ID)
	}
	if fs.createCalls.Load() != 1 {
		t.Errorf("createCalls = %d, want 1", fs.createCalls.Load())
	}
}

func TestProvider_MintKey_CapturesKeyOnce(t *testing.T) {
	p, _ := newTestProvider(t, "sk-ant")
	pr, err := p.CreateProject(context.Background(), "sk-ant", "work")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	keyID, material, err := p.MintKey(context.Background(), "sk-ant", pr.ID, "charon-mint")
	if err != nil {
		t.Fatalf("MintKey: %v", err)
	}
	if keyID == "" || material == "" {
		t.Errorf("MintKey returned empty fields: id=%q material=%q", keyID, material)
	}
	if !strings.HasPrefix(material, "sk-ant-") {
		t.Errorf("material missing sk-ant- prefix: %q", material)
	}
}

func TestProvider_RevokeKey_HappyPath(t *testing.T) {
	p, fs := newTestProvider(t, "sk-ant")
	pr, _ := p.CreateProject(context.Background(), "sk-ant", "work")
	keyID, _, _ := p.MintKey(context.Background(), "sk-ant", pr.ID, "charon-mint")

	if err := p.RevokeKey(context.Background(), "sk-ant", pr.ID, keyID); err != nil {
		t.Fatalf("RevokeKey: %v", err)
	}
	if fs.revokeCalls.Load() != 1 {
		t.Errorf("revokeCalls = %d, want 1", fs.revokeCalls.Load())
	}
}

func TestProvider_RevokeKey_AlreadyRevoked(t *testing.T) {
	p, _ := newTestProvider(t, "sk-ant")
	pr, _ := p.CreateProject(context.Background(), "sk-ant", "work")
	keyID, _, _ := p.MintKey(context.Background(), "sk-ant", pr.ID, "charon-mint")

	if err := p.RevokeKey(context.Background(), "sk-ant", pr.ID, keyID); err != nil {
		t.Fatalf("first RevokeKey: %v", err)
	}
	if err := p.RevokeKey(context.Background(), "sk-ant", pr.ID, keyID); !errors.Is(err, providers.ErrAlreadyRevoked) {
		t.Errorf("second RevokeKey should return ErrAlreadyRevoked, got %v", err)
	}
}

// resolveOrgID memoization: ListProjects after DiscoverOrg should not
// re-issue a /v1/organizations/me round-trip.
func TestProvider_OrgIDCache_SingleDiscovery(t *testing.T) {
	var meCalls atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/organizations/me", func(w http.ResponseWriter, r *http.Request) {
		meCalls.Add(1)
		_ = json.NewEncoder(w).Encode(orgResponse{ID: "org_x", Name: "n"})
	})
	mux.HandleFunc("/v1/organizations/", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(workspacesListResponse{})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	p := &Provider{BaseURL: srv.URL, HTTP: srv.Client()}
	ctx := context.Background()

	if _, _, err := p.DiscoverOrg(ctx, "sk-ant"); err != nil {
		t.Fatal(err)
	}
	if _, err := p.ListProjects(ctx, "sk-ant"); err != nil {
		t.Fatal(err)
	}
	if _, err := p.ListProjects(ctx, "sk-ant"); err != nil {
		t.Fatal(err)
	}

	if got := meCalls.Load(); got != 1 {
		t.Errorf("expected 1 /me discovery call, got %d (cache miss)", got)
	}
}

// resolveOrgID lazy-discovers when ListProjects is the first call.
func TestProvider_OrgIDCache_LazyDiscovery(t *testing.T) {
	p, _ := newTestProvider(t, "sk-ant")
	// No prior DiscoverOrg call. ListProjects should still succeed,
	// using a lazy /me hop internally.
	if _, err := p.ListProjects(context.Background(), "sk-ant"); err != nil {
		t.Fatalf("ListProjects without prior DiscoverOrg: %v", err)
	}
}

func TestProvider_UpstreamError_PreservesMessage(t *testing.T) {
	p, _ := newTestProvider(t, "sk-ant")
	_, err := p.CreateProject(context.Background(), "sk-ant", "")
	if err == nil {
		t.Fatal("expected upstream error")
	}
	if !strings.Contains(err.Error(), "name required") {
		t.Errorf("upstream message should be preserved; got %v", err)
	}
}

func TestProvider_NetworkError_Wrapped(t *testing.T) {
	p := &Provider{BaseURL: "http://127.0.0.1:1"}
	_, _, err := p.DiscoverOrg(context.Background(), "sk-ant")
	if err == nil {
		t.Fatal("expected network error")
	}
	if errors.Is(err, providers.ErrInvalidAdminKey) || errors.Is(err, providers.ErrAlreadyRevoked) {
		t.Errorf("network error should not map to a sentinel: %v", err)
	}
}

// 429 must NOT map to a sentinel — see openai equivalent.
func TestProvider_RateLimit_NotMappedToSentinel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"rate_limit_error","message":"rate limit hit"}}`))
	}))
	defer srv.Close()

	p := &Provider{BaseURL: srv.URL, HTTP: srv.Client()}
	_, _, err := p.DiscoverOrg(context.Background(), "sk-ant")
	if err == nil {
		t.Fatal("expected rate-limit error")
	}
	if errors.Is(err, providers.ErrInvalidAdminKey) || errors.Is(err, providers.ErrAlreadyRevoked) {
		t.Errorf("429 should not map to a sentinel, got %v", err)
	}
	if !strings.Contains(err.Error(), "rate limit hit") {
		t.Errorf("upstream message should be preserved, got %v", err)
	}
}

// 5xx must NOT map to a sentinel — see openai equivalent.
func TestProvider_5xx_NotMappedToSentinel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// /me discovery happens first; serve discovery so the test
		// reaches ListProjects' real upstream call.
		if strings.HasSuffix(r.URL.Path, "/me") {
			_ = json.NewEncoder(w).Encode(orgResponse{ID: "org_x", Name: "n"})
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"api_error","message":"upstream blew up"}}`))
	}))
	defer srv.Close()

	p := &Provider{BaseURL: srv.URL, HTTP: srv.Client()}
	_, err := p.ListProjects(context.Background(), "sk-ant")
	if err == nil {
		t.Fatal("expected upstream error")
	}
	if errors.Is(err, providers.ErrInvalidAdminKey) || errors.Is(err, providers.ErrAlreadyRevoked) {
		t.Errorf("5xx should not map to a sentinel, got %v", err)
	}
	if !strings.Contains(err.Error(), "upstream blew up") {
		t.Errorf("upstream message should be preserved, got %v", err)
	}
}

// Context cancellation propagates — see openai equivalent.
func TestProvider_ContextCancellation_Propagates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()

	p := &Provider{BaseURL: srv.URL, HTTP: srv.Client()}
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		_, _, err := p.DiscoverOrg(ctx, "sk-ant")
		done <- err
	}()
	cancel()

	select {
	case err := <-done:
		if err == nil {
			t.Error("DiscoverOrg should return an error when context is cancelled")
		}
		if errors.Is(err, providers.ErrInvalidAdminKey) || errors.Is(err, providers.ErrAlreadyRevoked) {
			t.Errorf("cancellation should not map to a sentinel: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Error("DiscoverOrg did not return after context cancel — context not propagated")
	}
}

// InvalidateAdminKey clears a cached entry; the next call re-runs
// discovery. Closes the loop on the M3 cache lifecycle issue raised
// in chunk-1 review.
func TestProvider_InvalidateAdminKey_ForcesRediscovery(t *testing.T) {
	var meCalls atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/organizations/me", func(w http.ResponseWriter, r *http.Request) {
		meCalls.Add(1)
		_ = json.NewEncoder(w).Encode(orgResponse{ID: "org_x", Name: "n"})
	})
	mux.HandleFunc("/v1/organizations/", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(workspacesListResponse{})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	p := &Provider{BaseURL: srv.URL, HTTP: srv.Client()}
	ctx := context.Background()

	if _, _, err := p.DiscoverOrg(ctx, "sk-ant"); err != nil {
		t.Fatal(err)
	}
	if _, err := p.ListProjects(ctx, "sk-ant"); err != nil {
		t.Fatal(err)
	}
	if got := meCalls.Load(); got != 1 {
		t.Errorf("expected 1 /me call before invalidate, got %d", got)
	}

	p.InvalidateAdminKey("sk-ant")

	if _, err := p.ListProjects(ctx, "sk-ant"); err != nil {
		t.Fatal(err)
	}
	if got := meCalls.Load(); got != 2 {
		t.Errorf("expected 2 /me calls after invalidate (forced rediscovery), got %d", got)
	}

	// Invalidate of an unknown key is a no-op (no panic, no extra call).
	p.InvalidateAdminKey("sk-never-seen")
}
