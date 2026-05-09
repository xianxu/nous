package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xianxu/nous/internal/charon/providers"
	"github.com/xianxu/nous/internal/charon/vault"
	"github.com/xianxu/nous/internal/charon/vault/memory"
)

// typeText sends each rune in s to the model as a single key press.
// Returns the model after all updates. Used to drive textinput.Model
// without rebuilding it ourselves.
func typeText(t *testing.T, m adminKeyPasteModel, s string) adminKeyPasteModel {
	t.Helper()
	for _, r := range s {
		updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = updated
	}
	return m
}

func newPasteFixture(t *testing.T, isReplace bool, existingOrgID string) (adminKeyPasteModel, *providers.Fake, *memory.Store, *providers.AdminKeyStore) {
	t.Helper()
	v := memory.New()
	store := fakeAdminStore(t, "openai", isReplace, "old-label")
	fake := providers.NewFake().WithName("openai")
	fake.OrgID = "org-test-001"
	fake.OrgName = "test-org"
	m := newAdminKeyPasteModel("openai", fake, store, v, isReplace, existingOrgID)
	return m, fake, v, store
}

func TestPaste_FirstTime_HappyPath(t *testing.T) {
	m, _, _, store := newPasteFixture(t, false, "")

	if m.state != pasteStateEditingLabel {
		t.Fatalf("initial state = %d, want editingLabel", m.state)
	}

	m = typeText(t, m, "xianxu@gmail.com")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated
	if m.state != pasteStateEditingKey {
		t.Fatalf("after label-enter: state = %d, want editingKey", m.state)
	}

	m = typeText(t, m, "sk-admin-secret")
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated
	if m.state != pasteStateDiscovering {
		t.Fatalf("after key-enter: state = %d, want discovering", m.state)
	}
	if cmd == nil {
		t.Fatal("key-enter should fire the discovery cmd")
	}

	// Run the discovery command — Fake returns synchronously.
	dm := cmd().(adminKeyDiscoveredMsg)
	if dm.err != nil {
		t.Fatalf("Fake discovery returned err: %v", dm.err)
	}

	updated, doneCmd := m.Update(dm)
	m = updated
	if doneCmd == nil {
		t.Fatal("on success, paste model should emit done cmd")
	}
	if _, ok := doneCmd().(adminKeyPasteDoneMsg); !ok {
		t.Errorf("expected adminKeyPasteDoneMsg, got %T", doneCmd())
	}

	// Store now has the admin key.
	if !store.IsSet() {
		t.Error("admin key store should be set after happy-path paste")
	}
	storedKey, meta, err := store.Get()
	if err != nil {
		t.Fatalf("store.Get: %v", err)
	}
	if storedKey != "sk-admin-secret" {
		t.Errorf("stored admin key = %q, want sk-admin-secret", storedKey)
	}
	if meta.OrgID != "org-test-001" || meta.OrgLabel != "xianxu@gmail.com" || meta.OrgName != "test-org" {
		t.Errorf("stored meta = %+v", meta)
	}
}

func TestPaste_LabelStep_RequiresNonEmpty(t *testing.T) {
	m, _, _, _ := newPasteFixture(t, false, "")
	// Try enter on an empty label — should not advance.
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated
	if m.state != pasteStateEditingLabel {
		t.Errorf("empty label should not advance state, got %d", m.state)
	}
}

func TestPaste_LabelStep_EscCancels(t *testing.T) {
	m, _, _, _ := newPasteFixture(t, false, "")
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd == nil {
		t.Fatal("esc should emit cancel msg")
	}
	if _, ok := cmd().(adminKeyPasteCancelMsg); !ok {
		t.Errorf("esc on label step should emit adminKeyPasteCancelMsg, got %T", cmd())
	}
}

func TestPaste_KeyStep_EscReturnsToLabel(t *testing.T) {
	m, _, _, _ := newPasteFixture(t, false, "")
	m = typeText(t, m, "me@example.com")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated // now editingKey

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated
	if m.state != pasteStateEditingLabel {
		t.Errorf("esc on key step should return to editingLabel, got %d", m.state)
	}
}

func TestPaste_DiscoveryError_ShowsErrorAndDismisses(t *testing.T) {
	m, fake, _, store := newPasteFixture(t, false, "")
	// Reject a specific key — anything else fails DiscoverOrg.
	fake.ValidAdminKey = "sk-admin-only-this-works"

	m = typeText(t, m, "me")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated
	m = typeText(t, m, "sk-wrong")
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated

	dm := cmd().(adminKeyDiscoveredMsg)
	if dm.err == nil {
		t.Fatal("expected discovery error for wrong key")
	}
	updated, _ = m.Update(dm)
	m = updated
	if m.state != pasteStateError {
		t.Fatalf("expected pasteStateError, got %d", m.state)
	}

	// Any keypress dismisses error → back to editingKey, key cleared.
	updated, _ = m.Update(tea.KeyMsg{Runes: []rune{' '}, Type: tea.KeyRunes})
	m = updated
	if m.state != pasteStateEditingKey {
		t.Errorf("after dismiss: state = %d, want editingKey", m.state)
	}
	if m.keyInput.Value() != "" {
		t.Errorf("key input not cleared after error dismiss: %q", m.keyInput.Value())
	}

	// Store wasn't touched.
	if store.IsSet() {
		t.Error("store should not have been set after discovery error")
	}
}

func TestPaste_Replace_SameOrg_SilentRotate(t *testing.T) {
	m, fake, _, store := newPasteFixture(t, true, "org-test-001") // existing OrgID matches Fake's OrgID
	fake.OrgID = "org-test-001"

	m = typeText(t, m, "new-label")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated
	m = typeText(t, m, "sk-admin-rotated")
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated

	dm := cmd().(adminKeyDiscoveredMsg)
	updated, doneCmd := m.Update(dm)
	m = updated
	if doneCmd == nil {
		t.Fatal("same-org rotate should emit done immediately (no confirm modal)")
	}
	if _, ok := doneCmd().(adminKeyPasteDoneMsg); !ok {
		t.Errorf("expected adminKeyPasteDoneMsg, got %T", doneCmd())
	}
	if m.state == pasteStateReplaceConfirm {
		t.Error("same-org rotate should NOT show replace confirm modal")
	}

	// Store has new key.
	storedKey, _, _ := store.Get()
	if storedKey != "sk-admin-rotated" {
		t.Errorf("rotated admin key = %q, want sk-admin-rotated", storedKey)
	}
}

func TestPaste_Replace_DifferentOrg_ShowsConfirmAndCascades(t *testing.T) {
	v := memory.New()
	// Two existing minted credentials under the OLD OrgID.
	_ = v.Set(&vault.Credential{
		Type: vault.TypeAdminKey, Provider: "openai", Account: "work",
		AdminKey: &vault.AdminKeyData{OrgID: "org-OLD", ProjectID: "proj_1", KeyMaterial: "sk-1"},
	})
	_ = v.Set(&vault.Credential{
		Type: vault.TypeAdminKey, Provider: "openai", Account: "personal",
		AdminKey: &vault.AdminKeyData{OrgID: "org-OLD", ProjectID: "proj_2", KeyMaterial: "sk-2"},
	})
	// And one under a different provider — must NOT be cascaded.
	_ = v.Set(&vault.Credential{
		Type: vault.TypeAdminKey, Provider: "anthropic", Account: "ant",
		AdminKey: &vault.AdminKeyData{OrgID: "org-OLD", ProjectID: "ws_1", KeyMaterial: "sk-ant-1"},
	})

	store := fakeAdminStore(t, "openai", true, "old-label")
	fake := providers.NewFake().WithName("openai")
	fake.OrgID = "org-NEW"
	fake.OrgName = "new-test-org"
	m := newAdminKeyPasteModel("openai", fake, store, v, true, "org-OLD")

	m = typeText(t, m, "new-label")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated
	m = typeText(t, m, "sk-admin-new")
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated

	dm := cmd().(adminKeyDiscoveredMsg)
	updated, _ = m.Update(dm)
	m = updated
	if m.state != pasteStateReplaceConfirm {
		t.Fatalf("different-org discovery should land in replaceConfirm, got %d", m.state)
	}
	if len(m.cascadeAccounts) != 2 {
		t.Errorf("expected 2 cascade accounts (openai-only), got %d: %v", len(m.cascadeAccounts), m.cascadeAccounts)
	}
	for _, a := range m.cascadeAccounts {
		if a == "ant" {
			t.Error("cross-provider account leaked into cascade list")
		}
	}

	view := m.View()
	for _, want := range []string{"different organization", "work", "personal", "[y/enter]", "[n/esc]"} {
		if !strings.Contains(view, want) {
			t.Errorf("replace confirm view missing %q\n%s", want, view)
		}
	}

	// Confirm with y. Should cascade-delete + commit + emit done.
	updated, cmd = m.Update(tea.KeyMsg{Runes: []rune{'y'}, Type: tea.KeyRunes})
	m = updated
	if cmd == nil {
		t.Fatal("y on replace confirm should emit done cmd")
	}
	if _, ok := cmd().(adminKeyPasteDoneMsg); !ok {
		t.Errorf("expected adminKeyPasteDoneMsg, got %T", cmd())
	}

	// OpenAI minted creds gone; anthropic cred preserved.
	if _, err := v.Get("openai", "work"); err == nil {
		t.Error("openai/work should be cascade-deleted")
	}
	if _, err := v.Get("openai", "personal"); err == nil {
		t.Error("openai/personal should be cascade-deleted")
	}
	if _, err := v.Get("anthropic", "ant"); err != nil {
		t.Error("anthropic/ant should NOT have been cascade-deleted (different provider)")
	}

	// Store has new key + new meta.
	storedKey, meta, _ := store.Get()
	if storedKey != "sk-admin-new" || meta.OrgID != "org-NEW" || meta.OrgName != "new-test-org" {
		t.Errorf("post-replace store state wrong: key=%q meta=%+v", storedKey, meta)
	}
}

func TestPaste_Replace_DifferentOrg_CancelKeepsEverything(t *testing.T) {
	v := memory.New()
	_ = v.Set(&vault.Credential{
		Type: vault.TypeAdminKey, Provider: "openai", Account: "work",
		AdminKey: &vault.AdminKeyData{OrgID: "org-OLD", ProjectID: "proj_1", KeyMaterial: "sk-1"},
	})
	store := fakeAdminStore(t, "openai", true, "old-label")
	fake := providers.NewFake().WithName("openai")
	fake.OrgID = "org-NEW"
	m := newAdminKeyPasteModel("openai", fake, store, v, true, "org-OLD")

	m = typeText(t, m, "new-label")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated
	m = typeText(t, m, "sk-admin-new")
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated
	dm := cmd().(adminKeyDiscoveredMsg)
	updated, _ = m.Update(dm)
	m = updated

	// Cancel.
	updated, cancelCmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cancelCmd == nil {
		t.Fatal("esc on replace confirm should emit cancel msg")
	}
	if _, ok := cancelCmd().(adminKeyPasteCancelMsg); !ok {
		t.Errorf("expected adminKeyPasteCancelMsg, got %T", cancelCmd())
	}
	_ = updated

	// Old admin key + minted creds untouched.
	storedKey, _, _ := store.Get()
	if storedKey != "sk-test-admin" {
		t.Errorf("after cancel, admin key should be unchanged, got %q", storedKey)
	}
	if _, err := v.Get("openai", "work"); err != nil {
		t.Error("openai/work should NOT have been cascade-deleted after cancel")
	}
}

func TestPaste_View_ShowsLabelStepURL(t *testing.T) {
	m, _, _, _ := newPasteFixture(t, false, "")
	view := m.View()
	if !strings.Contains(view, "https://platform.openai.com/settings/organization/admin-keys") {
		t.Errorf("label step view should show admin-key URL\n%s", view)
	}
}

func TestPaste_KeyEcho_HiddenAsBullets(t *testing.T) {
	m, _, _, _ := newPasteFixture(t, false, "")
	m = typeText(t, m, "label")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated
	m = typeText(t, m, "sk-secret")

	view := m.View()
	if strings.Contains(view, "sk-secret") {
		t.Error("admin key view should NOT echo plaintext key bytes")
	}
	if !strings.Contains(view, "•") {
		t.Error("admin key view should show bullet echo characters")
	}
}

// End-to-end through the top-level model: provider picker → openai →
// admin-key list → enter on admin row → paste flow opens.
func TestModel_ProviderToPasteFlow(t *testing.T) {
	v := memory.New()
	store := fakeAdminStore(t, "openai", false, "")
	fake := providers.NewFake().WithName("openai")

	m := model{
		vault:          v,
		adminProviders: map[string]providers.Provider{"openai": fake},
		adminStores:    map[string]*providers.AdminKeyStore{"openai": store},
	}
	pp, _ := newProviderPickerModel(v, m.adminStores, nil)
	m.providerPicker = pp
	// M7 onboarding cursor lands on +add for empty vault; reset to
	// row 0 so the down-arrow below moves to openai.
	m.providerPicker.cursor = 0
	m.current = screenProvider

	// → openai entry
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	mm := updated.(model)
	updated, cmd := mm.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm = updated.(model)
	updated, _ = mm.Update(cmd())
	mm = updated.(model)
	if mm.current != screenAdminKeyList {
		t.Fatalf("expected screenAdminKeyList, got %v", mm.current)
	}

	// Cursor on admin-key row (default at index 0). Enter opens paste.
	updated, cmd = mm.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm = updated.(model)
	if cmd == nil {
		t.Fatal("enter on admin-key row should emit a cmd")
	}
	updated, _ = mm.Update(cmd())
	mm = updated.(model)
	if mm.current != screenAdminKeyPaste {
		t.Fatalf("expected screenAdminKeyPaste, got %v", mm.current)
	}
	if mm.adminPaste.providerName != "openai" {
		t.Errorf("paste model providerName = %q, want openai", mm.adminPaste.providerName)
	}
	if mm.adminPaste.isReplace {
		t.Error("first-time setup should NOT be in replace mode")
	}
}
