package main

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/xianxu/nous/internal/charon/vault"
	"github.com/xianxu/nous/internal/charon/vault/memory"
)

// fixtureVault returns a memory vault with two google accounts and one
// dropbox account, each with distinct scope sets.
func fixtureVault(t *testing.T) vault.Store {
	t.Helper()
	v := memory.New()
	for _, c := range []*vault.Credential{
		{
			Provider: "google",
			Account:  "alice@gmail.com",
			Scopes: []string{
				"openid",
				"https://www.googleapis.com/auth/userinfo.email",
				"https://www.googleapis.com/auth/gmail.readonly",
			},
		},
		{
			Provider: "google",
			Account:  "bob@gmail.com",
			Scopes: []string{
				"openid",
				"https://www.googleapis.com/auth/userinfo.email",
			},
		},
		{
			Provider: "dropbox",
			Account:  "alice@dropbox.com",
			Scopes:   []string{"files.content.read"},
		},
	} {
		if err := v.Set(c); err != nil {
			t.Fatalf("vault.Set: %v", err)
		}
	}
	return v
}

func TestPermissionsPayload_AllProviders(t *testing.T) {
	got, err := permissionsPayload(fixtureVault(t))
	if err != nil {
		t.Fatalf("permissionsPayload: %v", err)
	}
	want := map[string]map[string]AccountPermissions{
		"google": {
			"alice@gmail.com": {Scopes: []string{
				"openid",
				"https://www.googleapis.com/auth/userinfo.email",
				"https://www.googleapis.com/auth/gmail.readonly",
			}},
			"bob@gmail.com": {Scopes: []string{
				"openid",
				"https://www.googleapis.com/auth/userinfo.email",
			}},
		},
		"dropbox": {
			"alice@dropbox.com": {Scopes: []string{"files.content.read"}},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("payload mismatch.\n got=%v\nwant=%v", got, want)
	}
}

func TestPermissionsPayload_EmptyVault(t *testing.T) {
	got, err := permissionsPayload(memory.New())
	if err != nil {
		t.Fatalf("permissionsPayload: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty payload from empty vault, got %v", got)
	}
}

func TestPermissionsPayload_NilScopesNormalizedToEmptySlice(t *testing.T) {
	v := memory.New()
	v.Set(&vault.Credential{Provider: "google", Account: "noscopes@gmail.com"})
	got, err := permissionsPayload(v)
	if err != nil {
		t.Fatalf("permissionsPayload: %v", err)
	}
	entry := got["google"]["noscopes@gmail.com"]
	if entry.Scopes == nil {
		t.Errorf("expected non-nil empty slice, got nil")
	}
	if len(entry.Scopes) != 0 {
		t.Errorf("expected empty scopes, got %v", entry.Scopes)
	}
	if entry.Vertex != nil {
		t.Errorf("expected no Vertex ref, got %+v", entry.Vertex)
	}
	if entry.AIStudio != nil {
		t.Errorf("expected no AIStudio ref, got %+v", entry.AIStudio)
	}
}

// AI Studio surfaces in the manifest with project_id only. The
// proxy attaches the key transparently so agents don't need
// KeyMaterial; project_id is the one piece an agent or human
// debugger genuinely needs (quota/billing inquiries on
// RESOURCE_EXHAUSTED). Critically the secret KeyMaterial must
// NOT leak into the JSON output, and other internal fields
// (uid, display_name, created_at) must stay out for cleanliness.
func TestPermissionsPayload_AIStudioSurfacedWithProjectIDOnly(t *testing.T) {
	v := memory.New()
	v.Set(&vault.Credential{
		Provider: "google",
		Account:  "alice@gmail.com",
		Scopes:   []string{"https://www.googleapis.com/auth/cloud-platform"},
		AIStudio: &vault.AIStudioData{
			Name:        "projects/alice-charon/locations/global/keys/uid-1",
			UID:         "uid-1",
			DisplayName: "charon-aistudio",
			KeyMaterial: "AIzaSy_THIS_IS_THE_SECRET",
			ProjectID:   "alice-charon",
			CreatedAt:   time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
		},
	})
	got, err := permissionsPayload(v)
	if err != nil {
		t.Fatalf("permissionsPayload: %v", err)
	}
	entry := got["google"]["alice@gmail.com"]
	if entry.AIStudio == nil {
		t.Fatal("expected AIStudio presence in manifest")
	}
	if entry.AIStudio.ProjectID != "alice-charon" {
		t.Errorf("AIStudio.ProjectID = %q, want alice-charon", entry.AIStudio.ProjectID)
	}

	// Secret must not leak through any field.
	b, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if bytes.Contains(b, []byte("AIzaSy_THIS_IS_THE_SECRET")) {
		t.Errorf("KeyMaterial leaked into manifest JSON:\n%s", b)
	}
	if bytes.Contains(b, []byte("key_material")) {
		t.Errorf("manifest should not have a key_material field at all:\n%s", b)
	}
	// Internal-only fields (debug metadata) must not appear in
	// agent-facing output.
	for _, want := range []string{"uid", "display_name", "created_at"} {
		if bytes.Contains(b, []byte(`"`+want+`"`)) {
			t.Errorf("internal-only field %q leaked into manifest:\n%s", want, b)
		}
	}
	if !bytes.Contains(b, []byte(`"ai-studio":{"project_id":"alice-charon"}`)) {
		t.Errorf("expected ai-studio to render with project_id only, got:\n%s", b)
	}
}

// Vertex ref carries only the two fields agents need to construct a
// URL. Internal metadata (project_name, billing_enabled,
// created_by_charon, updated_at) must not appear in manifest output.
func TestPermissionsPayload_VertexRefIsMinimal(t *testing.T) {
	v := memory.New()
	v.Set(&vault.Credential{
		Provider: "google",
		Account:  "alice@gmail.com",
		Scopes: []string{
			"openid",
			"https://www.googleapis.com/auth/cloud-platform",
		},
		GCP: &vault.GCPData{
			ProjectID:       "alice-charon",
			ProjectName:     "Alice Charon",
			VertexRegion:    "us-central1",
			CreatedByCharon: true,
			BillingEnabled:  true,
			UpdatedAt:       time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
		},
	})
	got, err := permissionsPayload(v)
	if err != nil {
		t.Fatalf("permissionsPayload: %v", err)
	}
	entry := got["google"]["alice@gmail.com"]
	if entry.Vertex == nil {
		t.Fatal("expected Vertex ref to be surfaced")
	}
	if entry.Vertex.ProjectID != "alice-charon" {
		t.Errorf("ProjectID = %q, want alice-charon", entry.Vertex.ProjectID)
	}
	if entry.Vertex.Region != "us-central1" {
		t.Errorf("Region = %q", entry.Vertex.Region)
	}

	// Internal-only fields must not leak into manifest JSON.
	b, _ := json.Marshal(got)
	for _, want := range []string{
		"Alice Charon",        // project_name
		"billing_enabled",
		"created_by_charon",
		"updated_at",
	} {
		if bytes.Contains(b, []byte(want)) {
			t.Errorf("internal field %q leaked into manifest:\n%s", want, b)
		}
	}
}
