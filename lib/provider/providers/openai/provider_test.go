package openai

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

	"github.com/xianxu/nous/lib/provider/providers"
)

// fakeServer mounts the OpenAI Admin API endpoints we care about and
// records calls for assertions. Tests construct it with the response
// shapes they need; missing routes return 404 so misrouted calls
// surface as test failures.
type fakeServer struct {
	t          *testing.T
	expectAuth string

	orgID, orgName string

	projects        map[string]projectResponse
	serviceAccounts map[string]serviceAccountResponse // by svc_acct id
	revokedSAs      map[string]bool                   // by svc_acct id
	createCalls     atomic.Int32
	revokeCalls     atomic.Int32

	srv *httptest.Server
}

func newFakeServer(t *testing.T, adminKey string) *fakeServer {
	t.Helper()
	fs := &fakeServer{
		t:               t,
		expectAuth:      "Bearer " + adminKey,
		orgID:           "org-test-aB3cD4",
		orgName:         "test-org",
		projects:        make(map[string]projectResponse),
		serviceAccounts: make(map[string]serviceAccountResponse),
		revokedSAs:      make(map[string]bool),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/organization/projects", fs.handleProjects)
	mux.HandleFunc("/v1/organization/projects/", fs.handleProjectChild)
	fs.srv = httptest.NewServer(mux)
	t.Cleanup(fs.srv.Close)
	return fs
}

// authOK returns true if the request carries the expected Bearer
// header. When false it has already written a 401 response.
func (fs *fakeServer) authOK(w http.ResponseWriter, r *http.Request) bool {
	if r.Header.Get("Authorization") != fs.expectAuth {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"invalid api key","type":"invalid_request_error","code":"invalid_api_key"}}`))
		return false
	}
	return true
}

func (fs *fakeServer) handleProjects(w http.ResponseWriter, r *http.Request) {
	if !fs.authOK(w, r) {
		return
	}
	// Mirror the real API: every authenticated response carries the
	// org id in this header. DiscoverOrg uses this endpoint with
	// limit=1 specifically to read the header.
	w.Header().Set("OpenAI-Organization", fs.orgID)
	switch r.Method {
	case http.MethodGet:
		out := projectsListResponse{Object: "list"}
		for _, p := range fs.projects {
			out.Data = append(out.Data, p)
		}
		_ = json.NewEncoder(w).Encode(out)
	case http.MethodPost:
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["name"] == "" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"name required","type":"invalid_request_error"}}`))
			return
		}
		fs.createCalls.Add(1)
		p := projectResponse{
			ID:     fmt.Sprintf("proj_%d", len(fs.projects)+1),
			Name:   body["name"],
			Object: "organization.project",
			Status: "active",
		}
		fs.projects[p.ID] = p
		_ = json.NewEncoder(w).Encode(p)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// handleProjectChild handles
//
//	/v1/organization/projects/{pid}/service_accounts
//	/v1/organization/projects/{pid}/service_accounts/{sid}
//
// Path-pattern matching is open-coded since net/http's standard mux
// pre-1.22 doesn't support wildcards.
func (fs *fakeServer) handleProjectChild(w http.ResponseWriter, r *http.Request) {
	if !fs.authOK(w, r) {
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/v1/organization/projects/")
	parts := strings.Split(rest, "/")
	if len(parts) < 2 || parts[1] != "service_accounts" {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	pid := parts[0]
	if _, ok := fs.projects[pid]; !ok {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"message":"project not found","type":"invalid_request_error"}}`))
		return
	}
	if len(parts) == 2 {
		// POST /service_accounts (mint).
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		saID := fmt.Sprintf("svc_acct_%d", len(fs.serviceAccounts)+1)
		sa := serviceAccountResponse{
			ID:        saID,
			Object:    "organization.project.service_account",
			Name:      body["name"],
			Role:      "member",
			CreatedAt: 1714492800,
		}
		sa.APIKey.ID = fmt.Sprintf("key_%d", len(fs.serviceAccounts)+1)
		sa.APIKey.Object = "organization.project.service_account.api_key"
		sa.APIKey.Value = fmt.Sprintf("sk-test-%s", body["name"])
		sa.APIKey.Name = body["name"]
		sa.APIKey.CreatedAt = 1714492800
		fs.serviceAccounts[saID] = sa
		_ = json.NewEncoder(w).Encode(sa)
		return
	}
	// 3 parts → DELETE service_accounts/{sid}
	sid := parts[2]
	if r.Method != http.MethodDelete {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	fs.revokeCalls.Add(1)
	if fs.revokedSAs[sid] {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"message":"service account not found","type":"invalid_request_error"}}`))
		return
	}
	if _, ok := fs.serviceAccounts[sid]; !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	fs.revokedSAs[sid] = true
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
	if p.Name() != "openai" {
		t.Errorf("Name = %q", p.Name())
	}
	if p.Type() != "admin-key" {
		t.Errorf("Type = %q", p.Type())
	}
}

func TestProvider_DiscoverOrg_HappyPath(t *testing.T) {
	p, fs := newTestProvider(t, "sk-admin-good")
	fs.orgID = "org-aB3cD4eF5"

	id, name, err := p.DiscoverOrg(context.Background(), "sk-admin-good")
	if err != nil {
		t.Fatalf("DiscoverOrg: %v", err)
	}
	if id != "org-aB3cD4eF5" {
		t.Errorf("orgID = %q, want org-aB3cD4eF5", id)
	}
	// OpenAI doesn't expose an org-name endpoint; OrgName is always
	// empty from DiscoverOrg. The TUI uses OrgLabel as fallback.
	if name != "" {
		t.Errorf("orgName = %q, want empty (no org-name endpoint exposed)", name)
	}
}

func TestProvider_DiscoverOrg_InvalidKey(t *testing.T) {
	p, _ := newTestProvider(t, "sk-admin-correct")

	_, _, err := p.DiscoverOrg(context.Background(), "sk-admin-wrong")
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

// DiscoverOrg fails closed if the upstream stops emitting the
// OpenAI-Organization header — surfaces a clear "API may have
// changed" error rather than silently storing an empty OrgID.
func TestProvider_DiscoverOrg_MissingHeader(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// No OpenAI-Organization header set.
		_ = json.NewEncoder(w).Encode(projectsListResponse{Object: "list"})
	}))
	defer srv.Close()
	p := &Provider{BaseURL: srv.URL, HTTP: srv.Client()}

	_, _, err := p.DiscoverOrg(context.Background(), "sk-admin")
	if err == nil {
		t.Fatal("DiscoverOrg should error when openai-organization header is missing")
	}
	if !strings.Contains(err.Error(), "no openai-organization header") {
		t.Errorf("error should mention missing header, got %v", err)
	}
}

func TestProvider_ListProjects_FiltersArchived(t *testing.T) {
	p, fs := newTestProvider(t, "sk-admin")
	fs.projects["proj_1"] = projectResponse{ID: "proj_1", Name: "active-one", Status: "active"}
	fs.projects["proj_2"] = projectResponse{ID: "proj_2", Name: "old", Status: "archived"}
	fs.projects["proj_3"] = projectResponse{ID: "proj_3", Name: "active-two", Status: "active"}

	got, err := p.ListProjects(context.Background(), "sk-admin")
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("expected 2 active projects, got %d (%+v)", len(got), got)
	}
	for _, pr := range got {
		if pr.Name == "old" {
			t.Errorf("archived project leaked into result: %+v", pr)
		}
	}
}

func TestProvider_CreateProject_HappyPath(t *testing.T) {
	p, fs := newTestProvider(t, "sk-admin")
	pr, err := p.CreateProject(context.Background(), "sk-admin", "work-project")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if pr.ID == "" || pr.Name != "work-project" {
		t.Errorf("unexpected project: %+v", pr)
	}
	if fs.createCalls.Load() != 1 {
		t.Errorf("createCalls = %d, want 1", fs.createCalls.Load())
	}
}

func TestProvider_MintKey_CapturesValueOnce(t *testing.T) {
	p, _ := newTestProvider(t, "sk-admin")
	pr, err := p.CreateProject(context.Background(), "sk-admin", "work")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	keyID, material, err := p.MintKey(context.Background(), "sk-admin", pr.ID, "charon-mint")
	if err != nil {
		t.Fatalf("MintKey: %v", err)
	}
	if keyID == "" || material == "" {
		t.Errorf("MintKey returned empty fields: id=%q material=%q", keyID, material)
	}
	if !strings.HasPrefix(material, "sk-") {
		t.Errorf("material missing sk- prefix: %q", material)
	}
}

func TestProvider_MintKey_UnknownProject(t *testing.T) {
	p, _ := newTestProvider(t, "sk-admin")
	_, _, err := p.MintKey(context.Background(), "sk-admin", "proj_nope", "k")
	if err == nil {
		t.Fatal("expected error for unknown project")
	}
	// Should be an upstream-wrapped error, not a sentinel.
	if errors.Is(err, providers.ErrAlreadyRevoked) || errors.Is(err, providers.ErrInvalidAdminKey) {
		t.Errorf("MintKey on unknown project should not return a sentinel, got %v", err)
	}
}

func TestProvider_RevokeKey_HappyPath(t *testing.T) {
	p, fs := newTestProvider(t, "sk-admin")
	pr, _ := p.CreateProject(context.Background(), "sk-admin", "work")
	keyID, _, _ := p.MintKey(context.Background(), "sk-admin", pr.ID, "charon-mint")

	if err := p.RevokeKey(context.Background(), "sk-admin", pr.ID, keyID); err != nil {
		t.Fatalf("RevokeKey: %v", err)
	}
	if fs.revokeCalls.Load() != 1 {
		t.Errorf("revokeCalls = %d, want 1", fs.revokeCalls.Load())
	}
}

func TestProvider_RevokeKey_AlreadyRevoked(t *testing.T) {
	p, _ := newTestProvider(t, "sk-admin")
	pr, _ := p.CreateProject(context.Background(), "sk-admin", "work")
	keyID, _, _ := p.MintKey(context.Background(), "sk-admin", pr.ID, "charon-mint")

	// First revoke succeeds.
	if err := p.RevokeKey(context.Background(), "sk-admin", pr.ID, keyID); err != nil {
		t.Fatalf("first RevokeKey: %v", err)
	}
	// Second returns ErrAlreadyRevoked (mapped from 404 on DELETE).
	if err := p.RevokeKey(context.Background(), "sk-admin", pr.ID, keyID); !errors.Is(err, providers.ErrAlreadyRevoked) {
		t.Errorf("second RevokeKey should return ErrAlreadyRevoked, got %v", err)
	}
}

func TestProvider_RevokeKey_UnknownKey_AlreadyRevoked(t *testing.T) {
	p, _ := newTestProvider(t, "sk-admin")
	pr, _ := p.CreateProject(context.Background(), "sk-admin", "work")

	err := p.RevokeKey(context.Background(), "sk-admin", pr.ID, "key_does_not_exist")
	if !errors.Is(err, providers.ErrAlreadyRevoked) {
		t.Errorf("revoke of unknown key should be treated as ErrAlreadyRevoked, got %v", err)
	}
}

func TestProvider_UpstreamError_PreservesMessage(t *testing.T) {
	p, _ := newTestProvider(t, "sk-admin")
	// Try to create a project with an empty name — fake server returns 400.
	_, err := p.CreateProject(context.Background(), "sk-admin", "")
	if err == nil {
		t.Fatal("expected upstream error")
	}
	if !strings.Contains(err.Error(), "name required") {
		t.Errorf("upstream message should be preserved; got %v", err)
	}
}

func TestProvider_NetworkError_Wrapped(t *testing.T) {
	// Point at a closed port so the request fails at connect.
	p := &Provider{BaseURL: "http://127.0.0.1:1"}
	_, _, err := p.DiscoverOrg(context.Background(), "sk-admin")
	if err == nil {
		t.Fatal("expected network error")
	}
	if errors.Is(err, providers.ErrInvalidAdminKey) || errors.Is(err, providers.ErrAlreadyRevoked) {
		t.Errorf("network error should not map to a sentinel: %v", err)
	}
}

// 429 (rate-limited) must NOT map to either sentinel — the TUI needs to
// distinguish "key invalid, paste a new one" from "we hit the rate
// limit, retry shortly." The upstream message must survive the wrap so
// the user sees what happened.
func TestProvider_RateLimit_NotMappedToSentinel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"rate limit exceeded","type":"rate_limit_exceeded"}}`))
	}))
	defer srv.Close()

	p := &Provider{BaseURL: srv.URL, HTTP: srv.Client()}
	_, _, err := p.DiscoverOrg(context.Background(), "sk-admin")
	if err == nil {
		t.Fatal("expected rate-limit error")
	}
	if errors.Is(err, providers.ErrInvalidAdminKey) || errors.Is(err, providers.ErrAlreadyRevoked) {
		t.Errorf("429 should not map to a sentinel, got %v", err)
	}
	if !strings.Contains(err.Error(), "rate limit exceeded") {
		t.Errorf("upstream rate-limit message should be preserved, got %v", err)
	}
}

// 5xx must NOT map to either sentinel — same reasoning as 429.
func TestProvider_5xx_NotMappedToSentinel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"message":"internal server error","type":"server_error"}}`))
	}))
	defer srv.Close()

	p := &Provider{BaseURL: srv.URL, HTTP: srv.Client()}
	_, err := p.ListProjects(context.Background(), "sk-admin")
	if err == nil {
		t.Fatal("expected upstream error")
	}
	if errors.Is(err, providers.ErrInvalidAdminKey) || errors.Is(err, providers.ErrAlreadyRevoked) {
		t.Errorf("5xx should not map to a sentinel, got %v", err)
	}
	if !strings.Contains(err.Error(), "internal server error") {
		t.Errorf("upstream message should be preserved, got %v", err)
	}
}

// Context cancellation propagates: the TUI passes cancellable contexts
// for slow mints; the request must abort promptly when ctx fires.
func TestProvider_ContextCancellation_Propagates(t *testing.T) {
	// Server hangs on every request; cancellation is the only way out.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()

	p := &Provider{BaseURL: srv.URL, HTTP: srv.Client()}
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		_, _, err := p.DiscoverOrg(ctx, "sk-admin")
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
