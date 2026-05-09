package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xianxu/nous/lib/provider/providers"
	"github.com/xianxu/nous/lib/provider/vault"
	"github.com/xianxu/nous/lib/provider/vault/memory"
)

func TestAdminKeyList_RendersUnconfigured(t *testing.T) {
	v := memory.New()
	store := fakeAdminStore(t, "openai", false, "")
	m, err := newAdminKeyListModel("openai", v, store)
	if err != nil {
		t.Fatalf("newAdminKeyListModel: %v", err)
	}
	if m.adminKeySet {
		t.Error("expected adminKeySet=false for unconfigured store")
	}
	// rows: [adminKey, addNew] when no projects exist.
	if len(m.rows) != 2 {
		t.Fatalf("rows = %d, want 2 (adminKey + addNew); got %+v", len(m.rows), m.rows)
	}
	if m.rows[0].kind != rowAdminKey {
		t.Errorf("first row kind = %v, want rowAdminKey", m.rows[0].kind)
	}
	if m.rows[1].kind != rowAddNew {
		t.Errorf("last row kind = %v, want rowAddNew", m.rows[1].kind)
	}

	view := m.View()
	for _, want := range []string{appName() + " › OpenAI", "Keys", "Admin key", "not set", "+ new key", "admin key required"} {
		if !strings.Contains(view, want) {
			t.Errorf("View missing %q\n%s", want, view)
		}
	}
}

func TestAdminKeyList_RendersConfigured_WithProjects(t *testing.T) {
	v := memory.New()
	_ = v.Set(&vault.Credential{
		Type: vault.TypeAdminKey, Provider: "openai", Account: "work",
		AdminKey: &vault.AdminKeyData{
			OrgID: "org-test-001", ProjectID: "proj_aB3", KeyMaterial: "sk-test-secret-xyz",
		},
	})
	_ = v.Set(&vault.Credential{
		Type: vault.TypeAdminKey, Provider: "openai", Account: "personal",
		AdminKey: &vault.AdminKeyData{
			OrgID: "org-test-001", ProjectID: "proj_X9z", KeyMaterial: "sk-test-secret-abc",
		},
	})
	store := fakeAdminStore(t, "openai", true, "xianxu@gmail.com")
	m, err := newAdminKeyListModel("openai", v, store)
	if err != nil {
		t.Fatalf("newAdminKeyListModel: %v", err)
	}
	if !m.adminKeySet {
		t.Error("expected adminKeySet=true")
	}
	// rows: [adminKey, project1, project2, addNew]
	if len(m.rows) != 4 {
		t.Fatalf("rows = %d, want 4; got %+v", len(m.rows), m.rows)
	}

	view := m.View()
	for _, want := range []string{"xianxu@gmail.com / test-org", "work", "personal", "proj_aB3", "proj_X9z"} {
		if !strings.Contains(view, want) {
			t.Errorf("View missing %q\n%s", want, view)
		}
	}
	// Key material redacted to a hint shape, not exposed verbatim.
	if strings.Contains(view, "sk-test-secret-xyz") {
		t.Error("View leaks full admin key material — must show only a hint")
	}
}

func TestAdminKeyList_AccountsSortedAlphabetically(t *testing.T) {
	v := memory.New()
	for _, name := range []string{"zebra", "alpha", "middle"} {
		_ = v.Set(&vault.Credential{
			Type: vault.TypeAdminKey, Provider: "openai", Account: name,
			AdminKey: &vault.AdminKeyData{OrgID: "org", ProjectID: "p_" + name, KeyMaterial: "sk"},
		})
	}
	store := fakeAdminStore(t, "openai", true, "me")
	m, _ := newAdminKeyListModel("openai", v, store)

	want := []string{"alpha", "middle", "zebra"}
	got := []string{m.rows[1].account, m.rows[2].account, m.rows[3].account}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("rows[%d].account = %q, want %q", i+1, got[i], want[i])
		}
	}
}

func TestAdminKeyList_FiltersOtherProviders(t *testing.T) {
	v := memory.New()
	_ = v.Set(&vault.Credential{
		Type: vault.TypeAdminKey, Provider: "openai", Account: "openai-acct",
		AdminKey: &vault.AdminKeyData{OrgID: "org-1", ProjectID: "p1", KeyMaterial: "sk-1"},
	})
	_ = v.Set(&vault.Credential{
		Type: vault.TypeAdminKey, Provider: "anthropic", Account: "anthropic-acct",
		AdminKey: &vault.AdminKeyData{OrgID: "org-2", ProjectID: "ws_2", KeyMaterial: "sk-ant-2"},
	})
	_ = v.Set(&vault.Credential{
		Type: vault.TypeOAuth, Provider: "google", Account: "x@gmail.com",
	})
	store := fakeAdminStore(t, "openai", true, "me")
	m, _ := newAdminKeyListModel("openai", v, store)

	for _, r := range m.rows {
		if r.kind == rowProject && r.account != "openai-acct" {
			t.Errorf("non-openai row leaked into openai list: %+v", r)
		}
	}
}

func TestAdminKeyList_AnthropicShowsWorkspaces(t *testing.T) {
	v := memory.New()
	store := fakeAdminStore(t, "anthropic", true, "me")
	m, _ := newAdminKeyListModel("anthropic", v, store)
	view := m.View()
	// Anthropic-specific verbiage shows up in the *mint flow Step 2*
	// (where the upstream-container distinction matters). The keys-list
	// screen itself is provider-agnostic now.
	for _, want := range []string{appName() + " › Anthropic", "Keys", "+ new key"} {
		if !strings.Contains(view, want) {
			t.Errorf("Anthropic view missing %q\n%s", want, view)
		}
	}
}

func TestAdminKeyList_KeyHintRedaction(t *testing.T) {
	cases := []struct {
		material string
		want     string // expected hint
	}{
		{"sk-test-secret", "sk-…ret"},
		{"sk-1234567890abcdef", "sk-…def"},
		{"short", "short"}, // too short to redact
		{"", ""},
	}
	for _, tc := range cases {
		got := hintFromKeyMaterial(tc.material)
		if got != tc.want {
			t.Errorf("hintFromKeyMaterial(%q) = %q, want %q", tc.material, got, tc.want)
		}
	}
}

func TestAdminKeyList_AddNewMutedWhenAdminKeyUnset(t *testing.T) {
	v := memory.New()
	store := fakeAdminStore(t, "openai", false, "")
	m, _ := newAdminKeyListModel("openai", v, store)

	// Cursor on +new project (last row).
	for m.cursor < len(m.rows)-1 {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	}
	if m.rows[m.cursor].kind != rowAddNew {
		t.Fatalf("expected cursor on rowAddNew, got %v", m.rows[m.cursor].kind)
	}

	// Enter on add-new should flash a "set admin key first" status,
	// not advance the flow.
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Errorf("enter on disabled add-new should not emit a command, got %v", cmd)
	}
	if !strings.Contains(updated.statusMsg, "admin key first") {
		t.Errorf("status should mention admin key first, got %q", updated.statusMsg)
	}
}

func TestAdminKeyList_EscEmitsBackMsg(t *testing.T) {
	v := memory.New()
	store := fakeAdminStore(t, "openai", false, "")
	m, _ := newAdminKeyListModel("openai", v, store)

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd == nil {
		t.Fatal("esc should emit a command")
	}
	if _, ok := cmd().(adminKeyListBackMsg); !ok {
		t.Errorf("expected adminKeyListBackMsg, got %T", cmd())
	}
}

func TestAdminKeyList_QuitEmitsQuitMsg(t *testing.T) {
	v := memory.New()
	store := fakeAdminStore(t, "openai", false, "")
	m, _ := newAdminKeyListModel("openai", v, store)

	_, cmd := m.Update(tea.KeyMsg{Runes: []rune{'q'}, Type: tea.KeyRunes})
	if cmd == nil {
		t.Fatal("q should emit tea.Quit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Errorf("q should produce tea.QuitMsg, got %T", cmd())
	}
}

func TestFormatAdminLabel_Fallbacks(t *testing.T) {
	cases := []struct {
		meta providers.AdminMeta
		want string
	}{
		{providers.AdminMeta{OrgLabel: "me", OrgName: "acme"}, "me / acme"},
		{providers.AdminMeta{OrgLabel: "me"}, "me"},
		{providers.AdminMeta{OrgName: "acme"}, "acme"},
		{providers.AdminMeta{OrgID: "org-aB3cD4eF5gH6iJ7kL8mN9oP0xx"}, "org-aB3cD4eF5gH6iJ7k…"},
		{providers.AdminMeta{}, "(unlabeled)"},
	}
	for _, tc := range cases {
		got := formatAdminLabel(tc.meta)
		if got != tc.want {
			t.Errorf("formatAdminLabel(%+v) = %q, want %q", tc.meta, got, tc.want)
		}
	}
}

// End-to-end through the top-level model: provider picker → openai
// row → admin-key list → esc → back to provider picker.
func TestModel_FullProviderToAdminKeyAndBack(t *testing.T) {
	v := memory.New()
	store := fakeAdminStore(t, "openai", false, "")

	m := model{
		vault:          v,
		adminProviders: map[string]providers.Provider{"openai": providers.NewFake().WithName("openai")},
		adminStores:    map[string]*providers.AdminKeyStore{"openai": store},
	}
	pp, _ := newProviderPickerModel(v, m.adminStores, nil)
	m.providerPicker = pp
	// M7 lands cursor on +add when vault is empty; reset to row 0
	// so the navigation below tests row 1 (openai).
	m.providerPicker.cursor = 0
	m.current = screenProvider

	// Move to openai (cursor 1) and enter.
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	mm := updated.(model)
	updated, cmd := mm.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm = updated.(model)
	if cmd == nil {
		t.Fatal("enter on openai should emit providerSelectedMsg cmd")
	}
	// Drive the message back into the model.
	updated, _ = mm.Update(cmd())
	mm = updated.(model)
	if mm.current != screenAdminKeyList {
		t.Fatalf("expected screenAdminKeyList, got %v", mm.current)
	}
	if mm.activeAdminProvider != "openai" {
		t.Errorf("activeAdminProvider = %q, want openai", mm.activeAdminProvider)
	}

	// Esc to navigate back.
	updated, cmd = mm.Update(tea.KeyMsg{Type: tea.KeyEsc})
	mm = updated.(model)
	if cmd == nil {
		t.Fatal("esc should emit adminKeyListBackMsg")
	}
	updated, _ = mm.Update(cmd())
	mm = updated.(model)
	if mm.current != screenProvider {
		t.Errorf("after esc/back: current = %v, want screenProvider", mm.current)
	}
}
