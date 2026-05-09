package keychain

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/xianxu/nous/internal/charon/vault"
)

// Round-trip every payload through the keychain's storedCredential
// shape so additions to vault.Credential (like #14's GCP sidecar)
// don't get silently dropped on disk. Memory vault works in tests
// because it stores Credential directly; keychain's intermediate
// JSON struct must mirror every field.
func TestStoredCredentialRoundTripsAllPayloads(t *testing.T) {
	in := &vault.Credential{
		Type:         vault.TypeOAuth,
		Provider:     "google",
		Account:      "alice@gmail.com",
		AccessToken:  "at",
		RefreshToken: "rt",
		Expiry:       time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
		Scopes:       []string{"openid", "https://www.googleapis.com/auth/cloud-platform"},
		GCP: &vault.GCPData{
			ProjectID:       "alice-charon",
			ProjectName:     "Alice Charon",
			VertexRegion:    "us-central1",
			CreatedByCharon: true,
			BillingEnabled:  true,
			UpdatedAt:       time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC),
		},
		AIStudio: &vault.AIStudioData{
			Name:        "projects/alice-charon/locations/global/keys/abc-uid",
			UID:         "abc-uid",
			DisplayName: "charon-aistudio",
			KeyMaterial: "AIzaSy_FAKE",
			ProjectID:   "alice-charon",
			CreatedAt:   time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC),
		},
		// AdminKey / Catalog typically don't co-exist with TypeOAuth
		// in production, but storedCredential's job is to round-trip
		// every payload faithfully — adding a new field to
		// vault.Credential without matching it here silently drops
		// data on the production keychain path.
		AdminKey: &vault.AdminKeyData{
			OrgID:       "org-x",
			OrgLabel:    "alice@gmail.com",
			ProjectID:   "proj-y",
			KeyID:       "key_z",
			KeyMaterial: "sk-fake",
		},
		Catalog: &vault.CatalogData{
			KeyMaterial: "paste-key-fake",
			AddedAt:     time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC),
		},
	}

	// Marshal → unmarshal mirrors the production write/read path.
	data, err := json.Marshal(fromCredential(in))
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var sc storedCredential
	if err := json.Unmarshal(data, &sc); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	out := sc.toCredential()

	if out.GCP == nil {
		t.Fatalf("GCP sidecar lost in keychain round-trip; serialized form: %s", data)
	}
	if out.GCP.ProjectID != in.GCP.ProjectID {
		t.Errorf("ProjectID = %q, want %q", out.GCP.ProjectID, in.GCP.ProjectID)
	}
	if out.GCP.VertexRegion != in.GCP.VertexRegion {
		t.Errorf("VertexRegion = %q, want %q", out.GCP.VertexRegion, in.GCP.VertexRegion)
	}
	if !out.GCP.BillingEnabled {
		t.Error("BillingEnabled lost")
	}
	if !out.GCP.CreatedByCharon {
		t.Error("CreatedByCharon lost")
	}

	if out.AIStudio == nil {
		t.Fatalf("AIStudio sidecar lost in keychain round-trip; serialized form: %s", data)
	}
	if out.AIStudio.KeyMaterial != in.AIStudio.KeyMaterial {
		t.Errorf("KeyMaterial = %q, want %q", out.AIStudio.KeyMaterial, in.AIStudio.KeyMaterial)
	}
	if out.AIStudio.UID != in.AIStudio.UID {
		t.Errorf("UID = %q, want %q", out.AIStudio.UID, in.AIStudio.UID)
	}
	if out.AdminKey == nil {
		t.Fatalf("AdminKey sidecar lost in keychain round-trip; serialized form: %s", data)
	}
	if out.AdminKey.OrgID != in.AdminKey.OrgID {
		t.Errorf("AdminKey.OrgID = %q, want %q", out.AdminKey.OrgID, in.AdminKey.OrgID)
	}
	if out.Catalog == nil {
		t.Fatalf("Catalog sidecar lost in keychain round-trip; serialized form: %s", data)
	}
	if out.Catalog.KeyMaterial != in.Catalog.KeyMaterial {
		t.Errorf("Catalog.KeyMaterial = %q, want %q", out.Catalog.KeyMaterial, in.Catalog.KeyMaterial)
	}
}
