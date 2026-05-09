package charoncli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/xianxu/nous/lib/provider/providers/gcp"
	"github.com/xianxu/nous/lib/provider/vault"
	"github.com/xianxu/nous/lib/provider/vault/memory"
)

// freshGoogleCred returns a credential with cloud-platform granted
// and a future expiry, so the tokenSupplier path does not refresh.
func freshGoogleCred(account string) *vault.Credential {
	return &vault.Credential{
		Provider:     "google",
		Account:      account,
		AccessToken:  "fresh-access-token",
		RefreshToken: "rt",
		Expiry:       time.Now().Add(1 * time.Hour),
		Scopes: []string{
			"openid",
			"https://www.googleapis.com/auth/userinfo.email",
			"https://www.googleapis.com/auth/cloud-platform",
		},
	}
}

// happyGCPServer returns an httptest server with default success
// responses for list/create/enable/billing. Tests mutate the
// behavior they care about.
func happyGCPServer(t *testing.T, projectID string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/projects", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "GET":
			json.NewEncoder(w).Encode(struct {
				Projects []gcp.Project `json:"projects"`
			}{
				Projects: []gcp.Project{
					{ProjectID: projectID, Name: "Existing", LifecycleState: "ACTIVE"},
				},
			})
		case "POST":
			json.NewEncoder(w).Encode(map[string]any{"done": true})
		}
	})
	mux.HandleFunc("/v1/projects/"+projectID+"/services:batchEnable",
		func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]any{"done": true})
		})
	mux.HandleFunc("/v1/projects/"+projectID+"/billingInfo",
		func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]any{"billingEnabled": true})
		})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// gcpClientFor returns a gcp.Client wired to srv with a constant
// token (so the orchestrator never invokes oauth.Refresh).
func gcpClientFor(srv *httptest.Server) *gcp.Client {
	return &gcp.Client{
		HTTP:            srv.Client(),
		Tokens:          func(ctx context.Context) (string, error) { return "fresh-access-token", nil },
		ResourceManager: srv.URL,
		ServiceUsage:    srv.URL,
		CloudBilling:    srv.URL,
		PollInterval:    1 * time.Millisecond,
	}
}

// stubPickerCmd is a deterministic picker for cmd-package tests.
type stubPickerCmd struct {
	choice gcp.Choice
	region string
}

func (s *stubPickerCmd) PickProject(ctx context.Context, existing []gcp.Project) (gcp.Choice, error) {
	return s.choice, nil
}
func (s *stubPickerCmd) PickRegion(ctx context.Context) (string, error) { return s.region, nil }
func (s *stubPickerCmd) Notify(format string, args ...any)              {}
func (s *stubPickerCmd) HandleBillingBlock(ctx context.Context, projectID, fixURL string, recheck func(context.Context) (bool, error)) (bool, error) {
	// Default for cmd-package tests: proceed without blocking.
	// Tests that exercise the block can replace this stub with a
	// custom impl as needed.
	return true, nil
}

func TestExecuteGCPSetup_PersistsGCPSidecarOnExistingProject(t *testing.T) {
	srv := happyGCPServer(t, "alpha")
	v := memory.New()
	v.Set(freshGoogleCred("xianxu@gmail.com"))

	picker := &stubPickerCmd{
		choice: gcp.Choice{Existing: &gcp.Project{ProjectID: "alpha", Name: "Existing", LifecycleState: "ACTIVE"}},
		region: "us-central1",
	}

	var out bytes.Buffer
	err := executeGCPSetup(context.Background(), v, "xianxu@gmail.com", gcpClientFor(srv), picker, &out)
	if err != nil {
		t.Fatalf("executeGCPSetup: %v", err)
	}

	cred, err := v.Get("google", "xianxu@gmail.com")
	if err != nil {
		t.Fatalf("vault.Get: %v", err)
	}
	if cred.GCP == nil {
		t.Fatal("expected GCP sidecar to be persisted")
	}
	if cred.GCP.ProjectID != "alpha" {
		t.Errorf("ProjectID = %q", cred.GCP.ProjectID)
	}
	if cred.GCP.VertexRegion != "us-central1" {
		t.Errorf("VertexRegion = %q", cred.GCP.VertexRegion)
	}
	if cred.GCP.CreatedByCharon {
		t.Error("expected CreatedByCharon=false for existing project")
	}
	if !cred.GCP.BillingEnabled {
		t.Error("expected BillingEnabled=true")
	}
	if cred.GCP.UpdatedAt.IsZero() {
		t.Error("UpdatedAt should be set")
	}
	if !strings.Contains(out.String(), "Stored project alpha") {
		t.Errorf("expected confirmation in output:\n%s", out.String())
	}
}

func TestExecuteGCPSetup_PreservesOAuthFields(t *testing.T) {
	// Persisting the GCP sidecar must not clobber AccessToken / Scopes /
	// RefreshToken / Expiry — the OAuth credential continues to function
	// after setup.
	srv := happyGCPServer(t, "alpha")
	v := memory.New()
	original := freshGoogleCred("xianxu@gmail.com")
	v.Set(original)

	picker := &stubPickerCmd{
		choice: gcp.Choice{Existing: &gcp.Project{ProjectID: "alpha", Name: "Existing", LifecycleState: "ACTIVE"}},
		region: "us-central1",
	}
	if err := executeGCPSetup(context.Background(), v, "xianxu@gmail.com", gcpClientFor(srv), picker, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	cred, _ := v.Get("google", "xianxu@gmail.com")
	if cred.AccessToken != original.AccessToken {
		t.Errorf("AccessToken changed: %q vs %q", cred.AccessToken, original.AccessToken)
	}
	if cred.RefreshToken != original.RefreshToken {
		t.Errorf("RefreshToken changed")
	}
	if len(cred.Scopes) != len(original.Scopes) {
		t.Errorf("Scopes changed: %v vs %v", cred.Scopes, original.Scopes)
	}
}

func TestExecuteGCPSetup_BillingDisabledShowsReminder(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/projects", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(struct {
			Projects []gcp.Project `json:"projects"`
		}{Projects: []gcp.Project{{ProjectID: "p", Name: "P", LifecycleState: "ACTIVE"}}})
	})
	mux.HandleFunc("/v1/projects/p/services:batchEnable", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"done": true})
	})
	mux.HandleFunc("/v1/projects/p/billingInfo", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"billingEnabled": false})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	v := memory.New()
	v.Set(freshGoogleCred("a@gmail.com"))
	picker := &stubPickerCmd{
		choice: gcp.Choice{Existing: &gcp.Project{ProjectID: "p", Name: "P", LifecycleState: "ACTIVE"}},
		region: "us-central1",
	}
	var out bytes.Buffer
	if err := executeGCPSetup(context.Background(), v, "a@gmail.com", gcpClientFor(srv), picker, &out); err != nil {
		t.Fatal(err)
	}
	cred, _ := v.Get("google", "a@gmail.com")
	if cred.GCP.BillingEnabled {
		t.Error("expected BillingEnabled=false in persisted GCP")
	}
	if !strings.Contains(out.String(), "billing is not linked") {
		t.Errorf("expected billing reminder, got:\n%s", out.String())
	}
}

func TestRunGCPSetup_RejectsAccountMissingCloudPlatform(t *testing.T) {
	v := memory.New()
	v.Set(&vault.Credential{
		Provider:     "google",
		Account:      "x@gmail.com",
		AccessToken:  "tok",
		RefreshToken: "rt",
		Expiry:       time.Now().Add(time.Hour),
		Scopes: []string{
			"openid",
			"https://www.googleapis.com/auth/gmail.readonly",
		},
	})
	err := runGCPSetup(context.Background(), strings.NewReader(""), &bytes.Buffer{}, v, "x@gmail.com", "")
	if err == nil {
		t.Fatal("expected error when cloud-platform scope is missing")
	}
	if !strings.Contains(err.Error(), "cloud-platform") {
		t.Errorf("error message should mention cloud-platform: %v", err)
	}
}

func TestRunGCPSetup_RejectsUnknownAccount(t *testing.T) {
	v := memory.New()
	err := runGCPSetup(context.Background(), strings.NewReader(""), &bytes.Buffer{}, v, "ghost@gmail.com", "")
	if err == nil {
		t.Fatal("expected error for unknown account")
	}
}

// stdinPicker UX tests — exercise the prompts/parsing without
// touching the network or the orchestrator.

func TestStdinPicker_PickProject_ExistingByNumber(t *testing.T) {
	in := strings.NewReader("2\n")
	var out bytes.Buffer
	p := newStdinPicker(in, &out)
	choice, err := p.PickProject(context.Background(), []gcp.Project{
		{ProjectID: "alpha", Name: "Alpha", LifecycleState: "ACTIVE"},
		{ProjectID: "beta", Name: "Beta", LifecycleState: "ACTIVE"},
	})
	if err != nil {
		t.Fatalf("PickProject: %v", err)
	}
	if choice.Existing == nil || choice.Existing.ProjectID != "beta" {
		t.Errorf("expected beta, got %+v", choice)
	}
}

func TestStdinPicker_PickProject_NewWithName(t *testing.T) {
	in := strings.NewReader("n\nMy Charon\n")
	var out bytes.Buffer
	p := newStdinPicker(in, &out)
	choice, err := p.PickProject(context.Background(), nil)
	if err != nil {
		t.Fatalf("PickProject: %v", err)
	}
	if choice.NewName != "My Charon" {
		t.Errorf("NewName = %q", choice.NewName)
	}
	if choice.Existing != nil {
		t.Errorf("expected no Existing for new-project choice")
	}
}

func TestStdinPicker_PickProject_RejectsBadIndex(t *testing.T) {
	in := strings.NewReader("99\n")
	var out bytes.Buffer
	p := newStdinPicker(in, &out)
	_, err := p.PickProject(context.Background(), []gcp.Project{
		{ProjectID: "a", Name: "A", LifecycleState: "ACTIVE"},
	})
	if err == nil {
		t.Fatal("expected error for out-of-range pick")
	}
}

func TestStdinPicker_PickRegion_DefaultOnEmpty(t *testing.T) {
	in := strings.NewReader("\n")
	var out bytes.Buffer
	p := newStdinPicker(in, &out)
	r, err := p.PickRegion(context.Background())
	if err != nil {
		t.Fatalf("PickRegion: %v", err)
	}
	if r != gcp.DefaultVertexRegion {
		t.Errorf("region = %q, want default %q", r, gcp.DefaultVertexRegion)
	}
}

func TestStdinPicker_PickRegion_ByNumber(t *testing.T) {
	in := strings.NewReader("3\n")
	var out bytes.Buffer
	p := newStdinPicker(in, &out)
	r, err := p.PickRegion(context.Background())
	if err != nil {
		t.Fatalf("PickRegion: %v", err)
	}
	if r != gcp.SupportedVertexRegions[2] {
		t.Errorf("region = %q, want %q", r, gcp.SupportedVertexRegions[2])
	}
}

func TestStdinPicker_PickRegion_FreeForm(t *testing.T) {
	in := strings.NewReader("us-central2-experimental\n")
	var out bytes.Buffer
	p := newStdinPicker(in, &out)
	r, err := p.PickRegion(context.Background())
	if err != nil {
		t.Fatalf("PickRegion: %v", err)
	}
	if r != "us-central2-experimental" {
		t.Errorf("expected free-form region, got %q", r)
	}
}
