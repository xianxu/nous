package gcp

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
)

// constToken returns a TokenSupplier that always returns the same
// token, for tests that don't care about refresh behavior.
func constToken(tok string) TokenSupplier {
	return func(ctx context.Context) (string, error) { return tok, nil }
}

// newTestClient returns a Client whose three base URLs all point at
// srv. Tests mount whatever paths they need on srv's mux.
func newTestClient(srv *httptest.Server, tok string) *Client {
	return &Client{
		HTTP:            srv.Client(),
		Tokens:          constToken(tok),
		ResourceManager: srv.URL,
		ServiceUsage:    srv.URL,
		CloudBilling:    srv.URL,
		PollInterval:    1 * time.Millisecond,
	}
}

// requireBearer fails the test if the request's Authorization header
// doesn't match the expected token. Returns true on match, so callers
// can early-return on mismatch without piling on follow-up assertions.
func requireBearer(t *testing.T, r *http.Request, want string) bool {
	t.Helper()
	got := r.Header.Get("Authorization")
	if got != "Bearer "+want {
		t.Errorf("Authorization = %q, want Bearer %s", got, want)
		return false
	}
	return true
}

func TestClient_TokenSupplierError(t *testing.T) {
	srv := httptest.NewServer(http.NewServeMux())
	defer srv.Close()
	c := newTestClient(srv, "")
	c.Tokens = func(ctx context.Context) (string, error) {
		return "", errors.New("refresh failed")
	}
	_, err := c.ListProjects(context.Background())
	if err == nil {
		t.Fatal("expected error from token supplier failure")
	}
	if !strings.Contains(err.Error(), "refresh failed") {
		t.Errorf("error chain missing supplier message: %v", err)
	}
}

func TestClient_NonJSONErrorBodyPreserved(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/projects", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"error":{"code":403,"message":"BILLING_DISABLED"}}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestClient(srv, "tok")
	_, err := c.ListProjects(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if !IsHTTPStatus(err, http.StatusForbidden) {
		t.Errorf("expected 403 classified, got: %v", err)
	}
	if !strings.Contains(HTTPBody(err), "BILLING_DISABLED") {
		t.Errorf("expected raw body in error, got: %v", err)
	}
}

func TestListProjects_FiltersInactiveAndPaginates(t *testing.T) {
	var calls atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/projects", func(w http.ResponseWriter, r *http.Request) {
		if !requireBearer(t, r, "tok") {
			return
		}
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		// Page 1: one ACTIVE, one DELETE_REQUESTED, with a next page token.
		// Page 2: one ACTIVE, no next token.
		switch r.URL.Query().Get("pageToken") {
		case "":
			json.NewEncoder(w).Encode(projectsListResponse{
				Projects: []Project{
					{ProjectID: "active-1", Name: "Active One", LifecycleState: "ACTIVE"},
					{ProjectID: "doomed", Name: "Going Away", LifecycleState: "DELETE_REQUESTED"},
				},
				NextPageToken: "next",
			})
		case "next":
			json.NewEncoder(w).Encode(projectsListResponse{
				Projects: []Project{
					{ProjectID: "active-2", Name: "Active Two", LifecycleState: "ACTIVE"},
				},
			})
		default:
			t.Errorf("unexpected pageToken=%q", r.URL.Query().Get("pageToken"))
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestClient(srv, "tok")
	got, err := c.ListProjects(context.Background())
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if calls.Load() != 2 {
		t.Errorf("expected 2 page fetches, got %d", calls.Load())
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 ACTIVE projects, got %d: %v", len(got), got)
	}
	if got[0].ProjectID != "active-1" || got[1].ProjectID != "active-2" {
		t.Errorf("project order wrong: %v", got)
	}
}

func TestCreateProject_PostsBodyAndReturnsOperation(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/projects", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("missing Content-Type")
		}
		var got createProjectRequest
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if got.ProjectID != "charon-gemini-1234" {
			t.Errorf("projectId = %q", got.ProjectID)
		}
		if got.Name != "Charon Gemini" {
			t.Errorf("name = %q", got.Name)
		}
		if got.Parent != nil {
			t.Errorf("expected no parent, got %+v", got.Parent)
		}
		json.NewEncoder(w).Encode(Operation{
			Name: "operations/cp.create-1234",
			Done: false,
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestClient(srv, "tok")
	op, err := c.CreateProject(context.Background(), "charon-gemini-1234", "Charon Gemini", nil)
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if op.Done {
		t.Error("freshly returned op should not be Done")
	}
	if op.Name != "operations/cp.create-1234" {
		t.Errorf("op.Name = %q", op.Name)
	}
}

func TestWaitOperation_PollsUntilDone(t *testing.T) {
	var calls atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/operations/cp.x", func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		op := Operation{Name: "operations/cp.x", Done: n >= 3}
		json.NewEncoder(w).Encode(op)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestClient(srv, "tok")
	if err := c.WaitOperation(context.Background(), "operations/cp.x", 1*time.Millisecond); err != nil {
		t.Fatalf("WaitOperation: %v", err)
	}
	if calls.Load() != 3 {
		t.Errorf("expected 3 polls, got %d", calls.Load())
	}
}

func TestWaitOperation_PropagatesOperationError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/operations/cp.bad", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(Operation{
			Name: "operations/cp.bad",
			Done: true,
			Error: &OperationError{
				Code:    7,
				Message: "PERMISSION_DENIED: caller lacks resourcemanager.projects.create",
			},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestClient(srv, "tok")
	err := c.WaitOperation(context.Background(), "operations/cp.bad", 1*time.Millisecond)
	if err == nil {
		t.Fatal("expected operation error")
	}
	var opErr *OperationError
	if !errors.As(err, &opErr) {
		t.Fatalf("expected *OperationError, got %T: %v", err, err)
	}
	if opErr.Code != 7 {
		t.Errorf("code = %d, want 7", opErr.Code)
	}
}

func TestWaitOperation_RespectsContextCancel(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/operations/forever", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(Operation{Name: "operations/forever", Done: false})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestClient(srv, "tok")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := c.WaitOperation(ctx, "operations/forever", 5*time.Millisecond)
	if err == nil {
		t.Fatal("expected context-deadline error")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected DeadlineExceeded, got: %v", err)
	}
}

func TestBatchEnableServices_NoOpOnEmpty(t *testing.T) {
	// No mux: a real call would 404. Empty list must short-circuit.
	srv := httptest.NewServer(http.NewServeMux())
	defer srv.Close()
	c := newTestClient(srv, "tok")
	if err := c.BatchEnableServices(context.Background(), "p", nil); err != nil {
		t.Errorf("empty enable should be no-op, got: %v", err)
	}
}

func TestBatchEnableServices_SyncDone(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/projects/myproj/services:batchEnable", func(w http.ResponseWriter, r *http.Request) {
		var got batchEnableRequest
		json.NewDecoder(r.Body).Decode(&got)
		want := []string{"aiplatform.googleapis.com", "apikeys.googleapis.com"}
		if len(got.ServiceIds) != len(want) {
			t.Errorf("serviceIds count = %d, want %d", len(got.ServiceIds), len(want))
		}
		json.NewEncoder(w).Encode(Operation{Name: "operations/su.x", Done: true})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestClient(srv, "tok")
	err := c.BatchEnableServices(context.Background(), "myproj", []string{
		"aiplatform.googleapis.com",
		"apikeys.googleapis.com",
	})
	if err != nil {
		t.Fatalf("BatchEnableServices: %v", err)
	}
}

func TestBatchEnableServices_AsyncPolls(t *testing.T) {
	var enableCalls, pollCalls atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/projects/myproj/services:batchEnable", func(w http.ResponseWriter, r *http.Request) {
		enableCalls.Add(1)
		json.NewEncoder(w).Encode(Operation{Name: "operations/su.async", Done: false})
	})
	mux.HandleFunc("/v1/operations/su.async", func(w http.ResponseWriter, r *http.Request) {
		n := pollCalls.Add(1)
		json.NewEncoder(w).Encode(Operation{Name: "operations/su.async", Done: n >= 2})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestClient(srv, "tok")
	err := c.BatchEnableServices(context.Background(), "myproj", []string{"aiplatform.googleapis.com"})
	if err != nil {
		t.Fatalf("BatchEnableServices: %v", err)
	}
	if enableCalls.Load() != 1 {
		t.Errorf("expected 1 enable call, got %d", enableCalls.Load())
	}
	if pollCalls.Load() < 2 {
		t.Errorf("expected at least 2 polls, got %d", pollCalls.Load())
	}
}

func TestGetBillingInfo_Enabled(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/projects/p1/billingInfo", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(BillingInfo{
			Name:               "projects/p1/billingInfo",
			ProjectID:          "p1",
			BillingAccountName: "billingAccounts/000000-000000-000000",
			BillingEnabled:     true,
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestClient(srv, "tok")
	got, err := c.GetBillingInfo(context.Background(), "p1")
	if err != nil {
		t.Fatalf("GetBillingInfo: %v", err)
	}
	if !got.BillingEnabled {
		t.Error("expected BillingEnabled=true")
	}
	if got.BillingAccountName == "" {
		t.Error("expected non-empty billing account")
	}
}

func TestGetBillingInfo_Disabled(t *testing.T) {
	// Cloud Billing's response for an unbilled project: 200 OK with
	// billingEnabled=false and no account name. Charon must NOT treat
	// this as an error — it's the expected shape that triggers the
	// warning UX, not a failure.
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/projects/p2/billingInfo", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(BillingInfo{
			Name:           "projects/p2/billingInfo",
			ProjectID:      "p2",
			BillingEnabled: false,
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestClient(srv, "tok")
	got, err := c.GetBillingInfo(context.Background(), "p2")
	if err != nil {
		t.Fatalf("GetBillingInfo: %v", err)
	}
	if got.BillingEnabled {
		t.Error("expected BillingEnabled=false")
	}
	if got.BillingAccountName != "" {
		t.Errorf("expected empty BillingAccountName, got %q", got.BillingAccountName)
	}
}
