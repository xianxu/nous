package tui

import (
	"context"
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xianxu/nous/internal/charon/providers"
	"github.com/xianxu/nous/internal/charon/vault"
	"github.com/xianxu/nous/internal/charon/vault/memory"
)

func newRevokeFixture(t *testing.T) (*memory.Store, *providers.AdminKeyStore, *providers.Fake) {
	t.Helper()
	v := memory.New()
	store := fakeAdminStore(t, "openai", true, "me@example.com")
	fake := providers.NewFake().WithName("openai")
	return v, store, fake
}

func TestRevokeProject_HappyPath(t *testing.T) {
	v, store, fake := newRevokeFixture(t)
	// Mint a key in the fake's project so RevokeKey has a real id to delete.
	pj, _ := fake.CreateProject(context.Background(), "sk-test-admin", "p1")
	keyID, mat, _ := fake.MintKey(context.Background(), "sk-test-admin", pj.ID, "charon-mint")

	_ = v.Set(&vault.Credential{
		Type: vault.TypeAdminKey, Provider: "openai", Account: "work",
		AdminKey: &vault.AdminKeyData{
			OrgID: "org-test-001", ProjectID: pj.ID, KeyID: keyID, KeyMaterial: mat,
		},
	})

	rm, err := newProjectRevokeModel("openai", fake, store, v, "work")
	if err != nil {
		t.Fatalf("newProjectRevokeModel: %v", err)
	}

	view := rm.View()
	for _, want := range []string{"Revoke openai/work", "Project ID:", "Key ID:"} {
		if !strings.Contains(view, want) {
			t.Errorf("confirm view missing %q\n%s", want, view)
		}
	}

	// Confirm with y.
	updated, cmd := rm.Update(tea.KeyMsg{Runes: []rune{'y'}, Type: tea.KeyRunes})
	rm = updated
	if rm.state != revokeStateInProgress {
		t.Fatalf("after y: state = %d, want inProgress", rm.state)
	}
	rs := cmd().(adminRevokeResultMsg)
	if rs.err != nil {
		t.Fatalf("upstream revoke err: %v", rs.err)
	}
	updated, doneCmd := rm.Update(rs)
	rm = updated
	if doneCmd == nil {
		t.Fatal("on success, revoke should emit done cmd")
	}
	if _, ok := doneCmd().(adminRevokeDoneMsg); !ok {
		t.Errorf("expected adminRevokeDoneMsg, got %T", doneCmd())
	}

	// Vault entry gone.
	if _, err := v.Get("openai", "work"); err == nil {
		t.Error("vault entry should be deleted after revoke")
	}
}

func TestRevokeProject_AlreadyRevokedTreatedAsSuccess(t *testing.T) {
	v, store, fake := newRevokeFixture(t)
	pj, _ := fake.CreateProject(context.Background(), "sk-test-admin", "p1")
	keyID, mat, _ := fake.MintKey(context.Background(), "sk-test-admin", pj.ID, "k")
	// Pre-revoke upstream so the next call returns ErrAlreadyRevoked.
	_ = fake.RevokeKey(context.Background(), "sk-test-admin", pj.ID, keyID)

	_ = v.Set(&vault.Credential{
		Type: vault.TypeAdminKey, Provider: "openai", Account: "work",
		AdminKey: &vault.AdminKeyData{ProjectID: pj.ID, KeyID: keyID, KeyMaterial: mat},
	})
	rm, _ := newProjectRevokeModel("openai", fake, store, v, "work")
	updated, cmd := rm.Update(tea.KeyMsg{Runes: []rune{'y'}, Type: tea.KeyRunes})
	rm = updated
	rs := cmd().(adminRevokeResultMsg)
	updated, doneCmd := rm.Update(rs)
	rm = updated
	if doneCmd == nil {
		t.Fatal("already-revoked should still produce adminRevokeDoneMsg")
	}
	if _, ok := doneCmd().(adminRevokeDoneMsg); !ok {
		t.Errorf("expected adminRevokeDoneMsg, got %T", doneCmd())
	}
	// Vault still cleaned up.
	if _, err := v.Get("openai", "work"); err == nil {
		t.Error("vault entry should be deleted even on already-revoked")
	}
}

func TestRevokeProject_UpstreamErrorSurfaced(t *testing.T) {
	v, store, fake := newRevokeFixture(t)
	// Set ValidAdminKey to something other than what's in the store.
	fake.ValidAdminKey = "sk-different-admin"
	pj, _ := fake.CreateProject(context.Background(), "sk-different-admin", "p1")
	keyID, mat, _ := fake.MintKey(context.Background(), "sk-different-admin", pj.ID, "k")

	_ = v.Set(&vault.Credential{
		Type: vault.TypeAdminKey, Provider: "openai", Account: "work",
		AdminKey: &vault.AdminKeyData{ProjectID: pj.ID, KeyID: keyID, KeyMaterial: mat},
	})
	rm, _ := newProjectRevokeModel("openai", fake, store, v, "work")
	updated, cmd := rm.Update(tea.KeyMsg{Runes: []rune{'y'}, Type: tea.KeyRunes})
	rm = updated
	rs := cmd().(adminRevokeResultMsg)
	updated, _ = rm.Update(rs)
	rm = updated
	if rm.state != revokeStateError {
		t.Fatalf("state = %d, want error", rm.state)
	}
	if !strings.Contains(rm.err.Error(), "upstream revoke") {
		t.Errorf("err should mention upstream revoke, got %v", rm.err)
	}
	// Vault entry NOT deleted on upstream error — user can retry.
	if _, err := v.Get("openai", "work"); err != nil {
		t.Error("vault entry should be preserved on upstream error")
	}
}

func TestRevokeProject_CancelKeepsEverything(t *testing.T) {
	v, store, fake := newRevokeFixture(t)
	_ = v.Set(&vault.Credential{
		Type: vault.TypeAdminKey, Provider: "openai", Account: "work",
		AdminKey: &vault.AdminKeyData{ProjectID: "p1", KeyID: "k1", KeyMaterial: "sk"},
	})
	rm, _ := newProjectRevokeModel("openai", fake, store, v, "work")
	_, cmd := rm.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd == nil {
		t.Fatal("esc should emit cancel msg")
	}
	if _, ok := cmd().(adminRevokeCancelMsg); !ok {
		t.Errorf("expected adminRevokeCancelMsg, got %T", cmd())
	}
	if _, err := v.Get("openai", "work"); err != nil {
		t.Error("vault entry should be untouched on cancel")
	}
}

func TestRevokeAdminKey_HappyPath_WithCascade(t *testing.T) {
	v, store, _ := newRevokeFixture(t)
	// Mint two creds under the same OrgID + a cross-provider one.
	_ = v.Set(&vault.Credential{
		Type: vault.TypeAdminKey, Provider: "openai", Account: "work",
		AdminKey: &vault.AdminKeyData{OrgID: "org-test-001", ProjectID: "p1", KeyMaterial: "sk-1"},
	})
	_ = v.Set(&vault.Credential{
		Type: vault.TypeAdminKey, Provider: "openai", Account: "personal",
		AdminKey: &vault.AdminKeyData{OrgID: "org-test-001", ProjectID: "p2", KeyMaterial: "sk-2"},
	})
	_ = v.Set(&vault.Credential{
		Type: vault.TypeAdminKey, Provider: "anthropic", Account: "ant",
		AdminKey: &vault.AdminKeyData{OrgID: "org-test-001", ProjectID: "ws_1", KeyMaterial: "sk-ant"},
	})

	rm, err := newAdminKeyRevokeModel("openai", store, v)
	if err != nil {
		t.Fatalf("newAdminKeyRevokeModel: %v", err)
	}
	if len(rm.cascadeAccounts) != 2 {
		t.Errorf("cascade should be 2 (openai-only), got %d: %v", len(rm.cascadeAccounts), rm.cascadeAccounts)
	}

	view := rm.View()
	for _, want := range []string{"Revoke admin key", "work", "personal", "underlying API keys keep working"} {
		if !strings.Contains(view, want) {
			t.Errorf("confirm view missing %q\n%s", want, view)
		}
	}

	updated, cmd := rm.Update(tea.KeyMsg{Runes: []rune{'y'}, Type: tea.KeyRunes})
	rm = updated
	rs := cmd().(adminRevokeResultMsg)
	updated, doneCmd := rm.Update(rs)
	rm = updated
	if doneCmd == nil {
		t.Fatal("admin-key revoke should emit done")
	}
	if _, ok := doneCmd().(adminRevokeDoneMsg); !ok {
		t.Errorf("expected adminRevokeDoneMsg, got %T", doneCmd())
	}

	// OpenAI creds gone; anthropic preserved.
	if _, err := v.Get("openai", "work"); err == nil {
		t.Error("openai/work should be cascade-deleted")
	}
	if _, err := v.Get("openai", "personal"); err == nil {
		t.Error("openai/personal should be cascade-deleted")
	}
	if _, err := v.Get("anthropic", "ant"); err != nil {
		t.Error("anthropic/ant should NOT be deleted (different provider)")
	}
	// Admin entry gone.
	if store.IsSet() {
		t.Error("admin key store should be cleared after revoke")
	}
}

func TestRevokeAdminKey_NoCascadeWhenEmpty(t *testing.T) {
	v, store, _ := newRevokeFixture(t)
	rm, err := newAdminKeyRevokeModel("openai", store, v)
	if err != nil {
		t.Fatalf("newAdminKeyRevokeModel: %v", err)
	}
	if len(rm.cascadeAccounts) != 0 {
		t.Errorf("cascade should be empty, got %v", rm.cascadeAccounts)
	}
	view := rm.View()
	if !strings.Contains(view, "No minted credentials under this OrgID") {
		t.Errorf("empty-cascade view missing 'no minted credentials' note\n%s", view)
	}
}

func TestRevokeAdminKey_CancelKeepsEverything(t *testing.T) {
	v, store, _ := newRevokeFixture(t)
	_ = v.Set(&vault.Credential{
		Type: vault.TypeAdminKey, Provider: "openai", Account: "work",
		AdminKey: &vault.AdminKeyData{OrgID: "org-test-001", ProjectID: "p1", KeyMaterial: "sk"},
	})
	rm, _ := newAdminKeyRevokeModel("openai", store, v)

	_, cmd := rm.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd == nil {
		t.Fatal("esc should emit cancel msg")
	}
	if _, ok := cmd().(adminRevokeCancelMsg); !ok {
		t.Errorf("expected adminRevokeCancelMsg, got %T", cmd())
	}
	if !store.IsSet() {
		t.Error("admin key should still be set after cancel")
	}
	if _, err := v.Get("openai", "work"); err != nil {
		t.Error("cascade target should be preserved on cancel")
	}
}

// failingDeleteVault lets specific accounts fail Delete; used to
// exercise the cascade continue-and-aggregate path.
type failingDeleteVault struct {
	*memory.Store
	failOn map[string]bool // account names that should fail
}

func (f *failingDeleteVault) Delete(provider, account string) error {
	if f.failOn[account] {
		return fmt.Errorf("simulated delete failure")
	}
	return f.Store.Delete(provider, account)
}

// Chunk-2 review hardening: cascade continues past per-account
// Delete failures, aggregates errors, surfaces all of them rather
// than abandoning the loop on the first failure.
func TestRevokeAdminKey_CascadeContinuesPastFailures(t *testing.T) {
	store := fakeAdminStore(t, "openai", true, "me@example.com")
	mem := memory.New()
	v := &failingDeleteVault{Store: mem, failOn: map[string]bool{"second": true}}

	for _, acct := range []string{"first", "second", "third"} {
		_ = mem.Set(&vault.Credential{
			Type: vault.TypeAdminKey, Provider: "openai", Account: acct,
			AdminKey: &vault.AdminKeyData{OrgID: "org-test-001", ProjectID: "p_" + acct, KeyMaterial: "sk-" + acct},
		})
	}

	rm, err := newAdminKeyRevokeModel("openai", store, v)
	if err != nil {
		t.Fatalf("newAdminKeyRevokeModel: %v", err)
	}
	if len(rm.cascadeAccounts) != 3 {
		t.Fatalf("expected 3 cascade accounts, got %d", len(rm.cascadeAccounts))
	}

	// Drive: y → cascade loop → aggregated error.
	updated, cmd := rm.Update(tea.KeyMsg{Runes: []rune{'y'}, Type: tea.KeyRunes})
	rm = updated
	rs := cmd().(adminRevokeResultMsg)
	updated, _ = rm.Update(rs)
	rm = updated

	if rm.state != revokeStateError {
		t.Fatalf("expected revokeStateError, got %d", rm.state)
	}
	// Error should mention the failing account by name and indicate
	// partial-failure semantics ("partially failed").
	if !strings.Contains(rm.err.Error(), "second") {
		t.Errorf("error should name failing account 'second', got %v", rm.err)
	}
	if !strings.Contains(rm.err.Error(), "partially failed") {
		t.Errorf("error should say 'partially failed', got %v", rm.err)
	}
	// First and third deletions ran despite the second's failure.
	if _, err := mem.Get("openai", "first"); err == nil {
		t.Error("first should have been deleted")
	}
	if _, err := mem.Get("openai", "third"); err == nil {
		t.Error("third should have been deleted (loop didn't bail on second's failure)")
	}
	// Admin entry NOT deleted because cascade had errors.
	if !store.IsSet() {
		t.Error("admin key should be preserved when cascade fails")
	}
}

// End-to-end through the top-level model.
func TestModel_ProviderToProjectRevoke(t *testing.T) {
	v := memory.New()
	store := fakeAdminStore(t, "openai", true, "me@example.com")
	fake := providers.NewFake().WithName("openai")
	pj, _ := fake.CreateProject(context.Background(), "sk-test-admin", "p1")
	keyID, mat, _ := fake.MintKey(context.Background(), "sk-test-admin", pj.ID, "k")
	_ = v.Set(&vault.Credential{
		Type: vault.TypeAdminKey, Provider: "openai", Account: "work",
		AdminKey: &vault.AdminKeyData{
			OrgID: "org-test-001", ProjectID: pj.ID, KeyID: keyID, KeyMaterial: mat,
		},
	})

	m := model{
		vault:          v,
		adminProviders: map[string]providers.Provider{"openai": fake},
		adminStores:    map[string]*providers.AdminKeyStore{"openai": store},
	}
	pp, _ := newProviderPickerModel(v, m.adminStores, nil)
	m.providerPicker = pp
	m.current = screenProvider

	// → openai entity list
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	mm := updated.(model)
	updated, cmd := mm.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm = updated.(model)
	updated, _ = mm.Update(cmd())
	mm = updated.(model)

	// Move to the project row (idx 1: admin@0, project@1, addNew@2).
	updated, _ = mm.Update(tea.KeyMsg{Type: tea.KeyDown})
	mm = updated.(model)
	if mm.adminList.rows[mm.adminList.cursor].kind != rowProject {
		t.Fatalf("cursor not on project row, got %v", mm.adminList.rows[mm.adminList.cursor].kind)
	}

	// `r` opens revoke modal.
	updated, cmd = mm.Update(tea.KeyMsg{Runes: []rune{'r'}, Type: tea.KeyRunes})
	mm = updated.(model)
	if cmd == nil {
		t.Fatal("r on project row should emit revoke request")
	}
	updated, _ = mm.Update(cmd())
	mm = updated.(model)
	if mm.current != screenAdminRevoke {
		t.Fatalf("expected screenAdminRevoke, got %v", mm.current)
	}

	// Confirm with y, drive the result.
	updated, cmd = mm.Update(tea.KeyMsg{Runes: []rune{'y'}, Type: tea.KeyRunes})
	mm = updated.(model)
	rs := cmd().(adminRevokeResultMsg)
	updated, doneCmd := mm.Update(rs)
	mm = updated.(model)
	updated, _ = mm.Update(doneCmd())
	mm = updated.(model)

	if mm.current != screenAdminKeyList {
		t.Errorf("after revoke done: current = %v, want screenAdminKeyList", mm.current)
	}
	if _, err := v.Get("openai", "work"); err == nil {
		t.Error("vault entry should be revoked")
	}
}
