package gcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// stubPicker is a deterministic Picker for orchestrator tests.
type stubPicker struct {
	choice  Choice
	region  string
	pickErr error // returned from PickProject when non-nil

	// Billing-block behavior. Defaults are: HandleBillingBlock
	// returns proceed=true (so most tests don't have to opt in).
	billingProceed     bool  // honored when billingProceedSet=true
	billingProceedSet  bool
	billingErr         error // returned from HandleBillingBlock when non-nil
	billingRecheckOnce bool  // call recheck() once before returning

	mu                sync.Mutex
	notices           []string
	regCalls          int
	pickCalls         int
	billingCalls      int
	lastBillingFixURL string
}

func (s *stubPicker) PickProject(ctx context.Context, existing []Project) (Choice, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pickCalls++
	if s.pickErr != nil {
		return Choice{}, s.pickErr
	}
	return s.choice, nil
}

func (s *stubPicker) PickRegion(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.regCalls++
	return s.region, nil
}

func (s *stubPicker) Notify(format string, args ...any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.notices = append(s.notices, fmt.Sprintf(format, args...))
}

// HandleBillingBlock for stub: by default returns proceed=true so
// existing tests don't have to opt in. Tests that exercise the
// billing-block flow override stubPicker.billingProceed /
// billingErr / and consult billingCalls.
func (s *stubPicker) HandleBillingBlock(ctx context.Context, projectID, fixURL string, recheck func(context.Context) (bool, error)) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.billingCalls++
	s.lastBillingFixURL = fixURL
	if s.billingErr != nil {
		return false, s.billingErr
	}
	if s.billingRecheckOnce {
		// One re-check call for tests that want to exercise the
		// recheck path; ignore result.
		_, _ = recheck(ctx)
	}
	if s.billingProceedSet {
		return s.billingProceed, nil
	}
	return true, nil
}

func (s *stubPicker) noticesJoined() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.Join(s.notices, "\n")
}

// setupServer wires the four endpoints Setup hits with default
// success behavior. Tests override individual handlers as needed.
func setupServer(t *testing.T) (*httptest.Server, *http.ServeMux) {
	t.Helper()
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, mux
}

func TestSetup_PickExistingProject(t *testing.T) {
	srv, mux := setupServer(t)
	mux.HandleFunc("/v1/projects", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(projectsListResponse{
			Projects: []Project{
				{ProjectID: "alpha", Name: "Alpha", LifecycleState: "ACTIVE"},
				{ProjectID: "beta", Name: "Beta", LifecycleState: "ACTIVE"},
			},
		})
	})
	mux.HandleFunc("/v1/projects/alpha/services:batchEnable", func(w http.ResponseWriter, r *http.Request) {
		var req batchEnableRequest
		json.NewDecoder(r.Body).Decode(&req)
		if len(req.ServiceIds) != len(RequiredServices) {
			t.Errorf("enabled %d services, want %d", len(req.ServiceIds), len(RequiredServices))
		}
		json.NewEncoder(w).Encode(Operation{Done: true})
	})
	mux.HandleFunc("/v1/projects/alpha/billingInfo", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(BillingInfo{BillingEnabled: true})
	})

	picker := &stubPicker{
		choice: Choice{Existing: &Project{ProjectID: "alpha", Name: "Alpha", LifecycleState: "ACTIVE"}},
		region: "us-central1",
	}
	c := newTestClient(srv, "tok")

	res, err := Setup(context.Background(), c, picker)
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if res.Project.ProjectID != "alpha" {
		t.Errorf("project id = %q, want alpha", res.Project.ProjectID)
	}
	if res.CreatedNew {
		t.Error("expected CreatedNew=false for existing project pick")
	}
	if !res.BillingEnabled {
		t.Error("expected BillingEnabled=true")
	}
	if res.Region != "us-central1" {
		t.Errorf("region = %q", res.Region)
	}
	if len(res.EnabledServices) != len(RequiredServices) {
		t.Errorf("EnabledServices = %v", res.EnabledServices)
	}
}

func TestSetup_CreateNewProject(t *testing.T) {
	srv, mux := setupServer(t)
	mux.HandleFunc("/v1/projects", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "GET":
			// User has no existing projects yet.
			json.NewEncoder(w).Encode(projectsListResponse{})
		case "POST":
			var req createProjectRequest
			json.NewDecoder(r.Body).Decode(&req)
			if req.ProjectID != "test-id" {
				t.Errorf("project id = %q, want test-id", req.ProjectID)
			}
			if req.Name != "Test Project" {
				t.Errorf("name = %q", req.Name)
			}
			if req.Parent != nil {
				t.Errorf("expected no parent (MVP), got %+v", req.Parent)
			}
			json.NewEncoder(w).Encode(Operation{Name: "operations/create-test", Done: false})
		default:
			t.Errorf("unexpected method %s", r.Method)
		}
	})
	mux.HandleFunc("/v1/operations/create-test", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(Operation{Name: "operations/create-test", Done: true})
	})
	mux.HandleFunc("/v1/projects/test-id/services:batchEnable", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(Operation{Done: true})
	})
	mux.HandleFunc("/v1/projects/test-id/billingInfo", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(BillingInfo{BillingEnabled: false})
	})

	picker := &stubPicker{
		choice: Choice{NewName: "Test Project", NewID: "test-id"},
		region: "us-east1",
	}
	c := newTestClient(srv, "tok")

	res, err := Setup(context.Background(), c, picker)
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if res.Project.ProjectID != "test-id" {
		t.Errorf("project id = %q", res.Project.ProjectID)
	}
	if !res.CreatedNew {
		t.Error("expected CreatedNew=true after projects.create")
	}
	if res.BillingEnabled {
		t.Error("expected BillingEnabled=false")
	}
	if res.Region != "us-east1" {
		t.Errorf("region = %q", res.Region)
	}
	if picker.billingCalls != 1 {
		t.Errorf("expected exactly one HandleBillingBlock call, got %d", picker.billingCalls)
	}
	if picker.lastBillingFixURL != "https://console.cloud.google.com/billing/linkedaccount?project=test-id" {
		t.Errorf("expected billing fix URL with project=test-id, got %q", picker.lastBillingFixURL)
	}
	notices := picker.noticesJoined()
	if !strings.Contains(notices, "Billing not linked on test-id") {
		t.Errorf("expected billing-not-linked notice, got:\n%s", notices)
	}
}

func TestSetup_GeneratesProjectIDWhenChoiceOmitsIt(t *testing.T) {
	srv, mux := setupServer(t)
	var capturedID string
	mux.HandleFunc("/v1/projects", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "GET":
			json.NewEncoder(w).Encode(projectsListResponse{})
		case "POST":
			var req createProjectRequest
			json.NewDecoder(r.Body).Decode(&req)
			capturedID = req.ProjectID
			json.NewEncoder(w).Encode(Operation{Name: "operations/x", Done: true})
		}
	})
	// batchEnable must match whatever id was generated.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/services:batchEnable"):
			json.NewEncoder(w).Encode(Operation{Done: true})
		case strings.HasSuffix(r.URL.Path, "/billingInfo"):
			json.NewEncoder(w).Encode(BillingInfo{BillingEnabled: true})
		default:
			http.NotFound(w, r)
		}
	})

	picker := &stubPicker{
		choice: Choice{NewName: "Auto Generated"},
		region: "us-central1",
	}
	c := newTestClient(srv, "tok")
	if _, err := Setup(context.Background(), c, picker); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if !strings.HasPrefix(capturedID, "charon-gemini-") {
		t.Errorf("generated id %q should start with charon-gemini-", capturedID)
	}
	if len(capturedID) != len("charon-gemini-")+8 {
		t.Errorf("generated id %q should be charon-gemini- + 8 hex chars", capturedID)
	}
}

func TestSetup_PropagatesPickerError(t *testing.T) {
	srv, mux := setupServer(t)
	mux.HandleFunc("/v1/projects", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(projectsListResponse{})
	})
	picker := &stubPicker{pickErr: errors.New("user cancelled")}
	c := newTestClient(srv, "tok")
	_, err := Setup(context.Background(), c, picker)
	if err == nil {
		t.Fatal("expected error from picker")
	}
	if !strings.Contains(err.Error(), "user cancelled") {
		t.Errorf("error chain missing picker error: %v", err)
	}
}

func TestSetup_RejectsEmptyChoice(t *testing.T) {
	srv, mux := setupServer(t)
	mux.HandleFunc("/v1/projects", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(projectsListResponse{})
	})
	picker := &stubPicker{choice: Choice{}}
	c := newTestClient(srv, "tok")
	_, err := Setup(context.Background(), c, picker)
	if err == nil {
		t.Fatal("expected error for empty choice")
	}
	if !strings.Contains(err.Error(), "empty choice") {
		t.Errorf("error message: %v", err)
	}
}

// Setup must abort when the picker says cancel (proceed=false) on
// the billing-block. Vertex/AI Studio depend on billing for
// charon-created projects, so silently proceeding leaves the user
// with a setup that won't work.
func TestSetup_BillingDisabled_PickerCancels_AbortsSetup(t *testing.T) {
	srv, mux := setupServer(t)
	mux.HandleFunc("/v1/projects", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(projectsListResponse{
			Projects: []Project{{ProjectID: "p", Name: "P", LifecycleState: "ACTIVE"}},
		})
	})
	mux.HandleFunc("/v1/projects/p/services:batchEnable", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(Operation{Done: true})
	})
	mux.HandleFunc("/v1/projects/p/billingInfo", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(BillingInfo{BillingEnabled: false})
	})

	picker := &stubPicker{
		choice:            Choice{Existing: &Project{ProjectID: "p", Name: "P", LifecycleState: "ACTIVE"}},
		region:            "us-central1",
		billingProceedSet: true,
		billingProceed:    false,
	}
	c := newTestClient(srv, "tok")

	_, err := Setup(context.Background(), c, picker)
	if err == nil {
		t.Fatal("expected setup to abort when picker cancels billing block")
	}
	if !strings.Contains(err.Error(), "setup cancelled") {
		t.Errorf("error message should explain cancellation: %v", err)
	}
}

// When the picker calls recheck and billing is now enabled, Setup
// updates BillingEnabled in the result so the caller persists the
// fresh state.
func TestSetup_BillingDisabled_RecheckAfterLink_UpdatesState(t *testing.T) {
	srv, mux := setupServer(t)
	mux.HandleFunc("/v1/projects", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(projectsListResponse{
			Projects: []Project{{ProjectID: "p", Name: "P", LifecycleState: "ACTIVE"}},
		})
	})
	mux.HandleFunc("/v1/projects/p/services:batchEnable", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(Operation{Done: true})
	})
	// Billing endpoint flips: false on first call, true on subsequent.
	billingCalls := 0
	mux.HandleFunc("/v1/projects/p/billingInfo", func(w http.ResponseWriter, r *http.Request) {
		billingCalls++
		json.NewEncoder(w).Encode(BillingInfo{BillingEnabled: billingCalls > 1})
	})

	picker := &stubPicker{
		choice:             Choice{Existing: &Project{ProjectID: "p", Name: "P", LifecycleState: "ACTIVE"}},
		region:             "us-central1",
		billingProceedSet:  true,
		billingProceed:     true,
		billingRecheckOnce: true,
	}
	c := newTestClient(srv, "tok")

	res, err := Setup(context.Background(), c, picker)
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if !res.BillingEnabled {
		t.Errorf("expected BillingEnabled=true after picker re-check found it linked")
	}
	if billingCalls < 3 {
		// 1 = initial check, 2 = picker recheck, 3 = orchestrator's
		// re-stamp after picker proceed=true. Anything fewer would
		// mean we didn't honor the recheck.
		t.Errorf("expected at least 3 billing endpoint calls (initial+recheck+restamp), got %d", billingCalls)
	}
}

func TestSetup_BillingReadFailureNonFatal(t *testing.T) {
	srv, mux := setupServer(t)
	mux.HandleFunc("/v1/projects", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(projectsListResponse{
			Projects: []Project{{ProjectID: "p1", Name: "P1", LifecycleState: "ACTIVE"}},
		})
	})
	mux.HandleFunc("/v1/projects/p1/services:batchEnable", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(Operation{Done: true})
	})
	mux.HandleFunc("/v1/projects/p1/billingInfo", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"error":{"code":403,"message":"PERMISSION_DENIED"}}`)
	})
	picker := &stubPicker{
		choice: Choice{Existing: &Project{ProjectID: "p1", Name: "P1", LifecycleState: "ACTIVE"}},
		region: "us-central1",
	}
	c := newTestClient(srv, "tok")
	res, err := Setup(context.Background(), c, picker)
	if err != nil {
		t.Fatalf("expected billing read failure to be non-fatal, got: %v", err)
	}
	if res.BillingEnabled {
		t.Error("expected BillingEnabled=false when billing read failed")
	}
	if !strings.Contains(picker.noticesJoined(), "Couldn't read billing info") {
		t.Errorf("expected billing-read warning, got:\n%s", picker.noticesJoined())
	}
}

func TestSetup_RejectsEmptyRegion(t *testing.T) {
	srv, mux := setupServer(t)
	mux.HandleFunc("/v1/projects", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(projectsListResponse{
			Projects: []Project{{ProjectID: "p1", Name: "P1", LifecycleState: "ACTIVE"}},
		})
	})
	mux.HandleFunc("/v1/projects/p1/services:batchEnable", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(Operation{Done: true})
	})
	mux.HandleFunc("/v1/projects/p1/billingInfo", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(BillingInfo{BillingEnabled: true})
	})
	picker := &stubPicker{
		choice: Choice{Existing: &Project{ProjectID: "p1", Name: "P1", LifecycleState: "ACTIVE"}},
		region: "   ", // whitespace-only — must reject
	}
	c := newTestClient(srv, "tok")
	_, err := Setup(context.Background(), c, picker)
	if err == nil {
		t.Fatal("expected error for empty region")
	}
}

func TestGenerateProjectID_FormatAndUniqueness(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		id := GenerateProjectID()
		if !strings.HasPrefix(id, "charon-gemini-") {
			t.Errorf("id %q missing prefix", id)
		}
		if len(id) != len("charon-gemini-")+8 {
			t.Errorf("id %q length wrong", id)
		}
		if seen[id] {
			t.Errorf("collision on id %q after %d generations", id, i)
		}
		seen[id] = true
	}
}
