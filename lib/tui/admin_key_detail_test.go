package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xianxu/nous/lib/provider/providers"
	"github.com/xianxu/nous/lib/provider/vault"
	"github.com/xianxu/nous/lib/provider/vault/memory"
)

func seedDetailCred(t *testing.T) *memory.Store {
	t.Helper()
	v := memory.New()
	_ = v.Set(&vault.Credential{
		Type: vault.TypeAdminKey, Provider: "openai", Account: "work-key",
		AdminKey: &vault.AdminKeyData{
			OrgID:       "org-test-001",
			OrgLabel:    "xianxu@gmail.com",
			OrgName:     "acme-inc",
			ProjectID:   "proj_aB3xY9zKL",
			ProjectName: "another",
			KeyID:       "svc_acct_123",
			KeyMaterial: "sk-test-secret-woA",
			CreatedAt:   time.Date(2026, 4, 30, 14, 21, 0, 0, time.UTC),
		},
	})
	return v
}

func TestDetail_RendersAllFields(t *testing.T) {
	v := seedDetailCred(t)
	m, err := newAdminKeyDetailModel("openai", v, "work-key")
	if err != nil {
		t.Fatalf("newAdminKeyDetailModel: %v", err)
	}

	view := m.View()
	for _, want := range []string{
		appName() + " › OpenAI › work-key",
		"Key info",
		"Name:",
		"work-key",
		"Project:",
		"another",
		"proj_aB3xY9zKL",
		"Key ID:",
		"svc_acct_123",
		"Key prefix:",
		"sk-…woA",
		"Created:",
		"2026-04-30 14:21",
		"Org:",
		"xianxu@gmail.com / acme-inc",
		"Org ID:",
		"org-test-001",
		"never shown after mint",
		"r revoke",
		"esc back",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("detail view missing %q\n%s", want, view)
		}
	}
	// Full key material must NOT appear — only the redacted hint.
	if strings.Contains(view, "sk-test-secret-woA") {
		t.Error("detail view leaks full key material")
	}
}

func TestDetail_RevokeKeyEmitsRequest(t *testing.T) {
	v := seedDetailCred(t)
	m, _ := newAdminKeyDetailModel("openai", v, "work-key")

	_, cmd := m.Update(tea.KeyMsg{Runes: []rune{'r'}, Type: tea.KeyRunes})
	if cmd == nil {
		t.Fatal("r should emit a command")
	}
	msg := cmd()
	rr, ok := msg.(adminRevokeRequestMsg)
	if !ok {
		t.Fatalf("expected adminRevokeRequestMsg, got %T", msg)
	}
	if rr.target != revokeProject {
		t.Errorf("target = %v, want revokeProject", rr.target)
	}
	if rr.provider != "openai" || rr.account != "work-key" {
		t.Errorf("revoke target wrong: %+v", rr)
	}
}

func TestDetail_EscEmitsBack(t *testing.T) {
	v := seedDetailCred(t)
	m, _ := newAdminKeyDetailModel("openai", v, "work-key")

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd == nil {
		t.Fatal("esc should emit a command")
	}
	if _, ok := cmd().(adminKeyDetailBackMsg); !ok {
		t.Errorf("expected adminKeyDetailBackMsg, got %T", cmd())
	}
}

func TestDetail_QuitKey(t *testing.T) {
	v := seedDetailCred(t)
	m, _ := newAdminKeyDetailModel("openai", v, "work-key")

	_, cmd := m.Update(tea.KeyMsg{Runes: []rune{'q'}, Type: tea.KeyRunes})
	if cmd == nil {
		t.Fatal("q should emit tea.Quit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Errorf("q should produce tea.QuitMsg, got %T", cmd())
	}
}

func TestDetail_MissingCredentialErrors(t *testing.T) {
	v := memory.New()
	_, err := newAdminKeyDetailModel("openai", v, "ghost")
	if err == nil {
		t.Fatal("missing credential should fail")
	}
}

func TestDetail_NoAdminKeyPayloadErrors(t *testing.T) {
	v := memory.New()
	_ = v.Set(&vault.Credential{
		Type:     vault.TypeOAuth,
		Provider: "openai",
		Account:  "weird",
		// No AdminKey — entity-list dispatch shouldn't reach this state,
		// but the guard exists in case storage is corrupted.
	})
	_, err := newAdminKeyDetailModel("openai", v, "weird")
	if err == nil {
		t.Fatal("credential without AdminKey payload should fail")
	}
}

func TestDetail_FieldFallbacks(t *testing.T) {
	v := memory.New()
	_ = v.Set(&vault.Credential{
		Type: vault.TypeAdminKey, Provider: "openai", Account: "minimal",
		AdminKey: &vault.AdminKeyData{
			// Sparse: only required fields populated.
			ProjectID:   "proj_X",
			KeyMaterial: "sk-min-abc",
		},
	})
	m, err := newAdminKeyDetailModel("openai", v, "minimal")
	if err != nil {
		t.Fatalf("newAdminKeyDetailModel: %v", err)
	}
	view := m.View()
	if !strings.Contains(view, "Project:") || !strings.Contains(view, "proj_X") {
		t.Errorf("project fallback not rendered: %s", view)
	}
	if !strings.Contains(view, "(unlabeled)") {
		t.Errorf("org fallback should show (unlabeled), got\n%s", view)
	}
	if strings.Contains(view, "Created:") {
		t.Error("zero CreatedAt should not render the row")
	}
}

// End-to-end: provider picker → entity list → enter on key row →
// detail screen → esc → back to entity list.
func TestModel_KeyDetailRoundTrip(t *testing.T) {
	v := seedDetailCred(t)
	store := fakeAdminStore(t, "openai", true, "xianxu@gmail.com")
	fake := providers.NewFake().WithName("openai")

	m := model{
		vault:          v,
		adminProviders: map[string]providers.Provider{"openai": fake},
		adminStores:    map[string]*providers.AdminKeyStore{"openai": store},
	}
	pp, _ := newProviderPickerModel(v, m.adminStores, nil)
	m.providerPicker = pp
	m.current = screenProvider

	// Provider picker → OpenAI entity list.
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	mm := updated.(model)
	updated, cmd := mm.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm = updated.(model)
	updated, _ = mm.Update(cmd())
	mm = updated.(model)
	if mm.current != screenAdminKeyList {
		t.Fatalf("expected screenAdminKeyList, got %v", mm.current)
	}

	// Move cursor to the project row (admin row at 0, project at 1).
	updated, _ = mm.Update(tea.KeyMsg{Type: tea.KeyDown})
	mm = updated.(model)
	if mm.adminList.rows[mm.adminList.cursor].kind != rowProject {
		t.Fatalf("cursor not on project row, got %v", mm.adminList.rows[mm.adminList.cursor].kind)
	}

	// Enter opens detail.
	updated, cmd = mm.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm = updated.(model)
	if cmd == nil {
		t.Fatal("enter on key row should emit detail-request cmd")
	}
	updated, _ = mm.Update(cmd())
	mm = updated.(model)
	if mm.current != screenAdminKeyDetail {
		t.Fatalf("expected screenAdminKeyDetail, got %v", mm.current)
	}

	// Esc returns to entity list.
	updated, cmd = mm.Update(tea.KeyMsg{Type: tea.KeyEsc})
	mm = updated.(model)
	if cmd == nil {
		t.Fatal("esc should emit detail-back cmd")
	}
	updated, _ = mm.Update(cmd())
	mm = updated.(model)
	if mm.current != screenAdminKeyList {
		t.Errorf("after esc/back: current = %v, want screenAdminKeyList", mm.current)
	}
}
