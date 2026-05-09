package providers

import (
	"context"
	"errors"
	"testing"
)

func TestFake_DiscoverOrg_AcceptsAnyKeyByDefault(t *testing.T) {
	f := NewFake()
	orgID, orgName, err := f.DiscoverOrg(context.Background(), "anything-non-empty")
	if err != nil {
		t.Fatalf("DiscoverOrg: %v", err)
	}
	if orgID != "org-fake-0001" || orgName != "fake-org" {
		t.Errorf("default identity wrong: got %q/%q", orgID, orgName)
	}
}

func TestFake_DiscoverOrg_RejectsEmptyKey(t *testing.T) {
	f := NewFake()
	_, _, err := f.DiscoverOrg(context.Background(), "")
	if !errors.Is(err, ErrInvalidAdminKey) {
		t.Errorf("expected ErrInvalidAdminKey, got %v", err)
	}
}

func TestFake_DiscoverOrg_GatedByValidAdminKey(t *testing.T) {
	f := NewFake()
	f.ValidAdminKey = "sk-admin-correct"

	if _, _, err := f.DiscoverOrg(context.Background(), "sk-admin-wrong"); !errors.Is(err, ErrInvalidAdminKey) {
		t.Errorf("wrong key should be rejected, got %v", err)
	}
	if _, _, err := f.DiscoverOrg(context.Background(), "sk-admin-correct"); err != nil {
		t.Errorf("correct key should be accepted, got %v", err)
	}
}

func TestFake_CreateProject_MintKey_RevokeKey_HappyPath(t *testing.T) {
	f := NewFake()
	ctx := context.Background()
	const adminKey = "sk-admin"

	p, err := f.CreateProject(ctx, adminKey, "work-project")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if p.Name != "work-project" || p.ID == "" {
		t.Errorf("CreateProject returned unexpected project: %+v", p)
	}

	keyID, material, err := f.MintKey(ctx, adminKey, p.ID, "charon-mint")
	if err != nil {
		t.Fatalf("MintKey: %v", err)
	}
	if keyID == "" || material == "" {
		t.Errorf("MintKey returned empty fields: id=%q material=%q", keyID, material)
	}

	// Revoke succeeds the first time…
	if err := f.RevokeKey(ctx, adminKey, p.ID, keyID); err != nil {
		t.Fatalf("RevokeKey first call: %v", err)
	}
	// …and reports already-revoked the second time.
	if err := f.RevokeKey(ctx, adminKey, p.ID, keyID); !errors.Is(err, ErrAlreadyRevoked) {
		t.Errorf("second RevokeKey should return ErrAlreadyRevoked, got %v", err)
	}
}

func TestFake_RevokeKey_UnknownKey_AlreadyRevoked(t *testing.T) {
	f := NewFake()
	err := f.RevokeKey(context.Background(), "sk-admin", "proj_x", "key_does_not_exist")
	if !errors.Is(err, ErrAlreadyRevoked) {
		t.Errorf("revoke of unknown key should report ErrAlreadyRevoked, got %v", err)
	}
}

func TestFake_MintKey_RejectsUnknownProject(t *testing.T) {
	f := NewFake()
	_, _, err := f.MintKey(context.Background(), "sk-admin", "proj_does_not_exist", "name")
	if err == nil {
		t.Error("MintKey should fail for unknown project")
	}
	if errors.Is(err, ErrAlreadyRevoked) || errors.Is(err, ErrInvalidAdminKey) {
		t.Errorf("MintKey should not return sentinel errors for unknown project, got %v", err)
	}
}

func TestFake_ListProjects_ReturnsSeeded(t *testing.T) {
	f := NewFake()
	f.SeedProject("proj_seed_1", "alpha")
	f.SeedProject("proj_seed_2", "beta")

	got, err := f.ListProjects(context.Background(), "sk-admin")
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("expected 2 projects, got %d", len(got))
	}
}

func TestFake_WithName_OverridesIdentity(t *testing.T) {
	f := NewFake().WithName("openai")
	if f.Name() != "openai" {
		t.Errorf("WithName not honored: got %q", f.Name())
	}
}

func TestFake_Snapshot_TracksMintedKeys(t *testing.T) {
	f := NewFake()
	ctx := context.Background()
	p, _ := f.CreateProject(ctx, "sk-admin", "p1")
	keyID, _, _ := f.MintKey(ctx, "sk-admin", p.ID, "k1")

	_, keys := f.Snapshot()
	if len(keys) != 1 {
		t.Fatalf("expected 1 key in snapshot, got %d", len(keys))
	}
	if keys[0].KeyID != keyID || keys[0].Revoked {
		t.Errorf("snapshot mismatch: %+v", keys[0])
	}

	_ = f.RevokeKey(ctx, "sk-admin", p.ID, keyID)
	_, keys = f.Snapshot()
	if !keys[0].Revoked {
		t.Error("snapshot should reflect revoked state")
	}
}

// Compile-time guard: Fake must implement Provider. If this fails to
// compile, the interface or the Fake have drifted.
var _ Provider = (*Fake)(nil)
