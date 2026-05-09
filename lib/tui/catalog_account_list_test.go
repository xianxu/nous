package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xianxu/nous/lib/provider/providers/catalog"
	"github.com/xianxu/nous/lib/provider/vault"
	"github.com/xianxu/nous/lib/provider/vault/memory"
)

func anthropicCatalogEntry() catalog.Entry {
	return catalog.Entry{
		ID:               "anthropic",
		Name:             "Anthropic",
		HostnamePatterns: []string{"api.anthropic.com"},
		Auth: catalog.Auth{
			Style:  "header",
			Header: "x-api-key",
		},
		ConsoleURL: "https://console.anthropic.com/settings/keys",
	}
}

func storeCatalogCred(t *testing.T, v vault.Store, providerID, account, key string) {
	t.Helper()
	if err := v.Set(&vault.Credential{
		Type: vault.TypeCatalog, Provider: providerID, Account: account,
		Catalog: &vault.CatalogData{KeyMaterial: key},
	}); err != nil {
		t.Fatalf("vault.Set: %v", err)
	}
}

func TestCatalogAccountList_RendersStoredAccountsAndAddRow(t *testing.T) {
	v := memory.New()
	storeCatalogCred(t, v, "anthropic", "personal", "sk-ant-key-AAAA")
	storeCatalogCred(t, v, "anthropic", "work", "sk-ant-key-ZZZZ")
	// Other-provider cred should not appear.
	storeCatalogCred(t, v, "groq", "shouldnt-appear", "gsk-irrelevant")

	m, err := newCatalogAccountListModel(anthropicCatalogEntry(), v)
	if err != nil {
		t.Fatalf("newCatalogAccountListModel: %v", err)
	}
	if got, want := len(m.rows), 3; got != want {
		t.Fatalf("rows = %d, want %d (2 accounts + add)", got, want)
	}
	if m.rows[0].account != "personal" || m.rows[1].account != "work" {
		t.Errorf("rows = %v, want [personal, work, +add] (sorted)", m.rows)
	}
	if !m.rows[2].isAddNew {
		t.Errorf("trailing row not + add account: %v", m.rows[2])
	}
	view := m.View()
	for _, want := range []string{"personal", "work", "+ add account", "Anthropic"} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q\n%s", want, view)
		}
	}
	// Hint should be redacted.
	if strings.Contains(view, "sk-ant-key-AAAA") {
		t.Errorf("view leaked full key:\n%s", view)
	}
}

func TestCatalogAccountList_REmitsRevokeRequest(t *testing.T) {
	v := memory.New()
	storeCatalogCred(t, v, "anthropic", "personal", "sk-ant-AAAA")
	m, _ := newCatalogAccountListModel(anthropicCatalogEntry(), v)
	// Cursor on personal (row 0).
	updated, cmd := m.Update(tea.KeyMsg{Runes: []rune{'r'}, Type: tea.KeyRunes})
	m = updated
	if cmd == nil {
		t.Fatal("r on account row produced no cmd")
	}
	got := cmd()
	rev, ok := got.(catalogRevokeRequestMsg)
	if !ok {
		t.Fatalf("cmd() = %T, want catalogRevokeRequestMsg", got)
	}
	if rev.entry.ID != "anthropic" || rev.account != "personal" {
		t.Errorf("rev = %+v, want anthropic/personal", rev)
	}
}

func TestCatalogAccountList_EnterOnAddNewEmitsAddMsg(t *testing.T) {
	v := memory.New()
	m, _ := newCatalogAccountListModel(anthropicCatalogEntry(), v)
	// Only the + add row exists; cursor is at 0.
	if !m.rows[0].isAddNew {
		t.Fatalf("expected only + add row when no creds; got %v", m.rows)
	}
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	_ = updated
	if cmd == nil {
		t.Fatal("enter on + add row produced no cmd")
	}
	got := cmd()
	add, ok := got.(catalogAccountAddMsg)
	if !ok {
		t.Fatalf("cmd() = %T, want catalogAccountAddMsg", got)
	}
	if add.entry.ID != "anthropic" {
		t.Errorf("add.entry = %+v, want anthropic", add.entry)
	}
}

func TestCatalogAccountList_EscEmitsBackMsg(t *testing.T) {
	v := memory.New()
	storeCatalogCred(t, v, "anthropic", "personal", "sk-ant-AAAA")
	m, _ := newCatalogAccountListModel(anthropicCatalogEntry(), v)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd == nil {
		t.Fatal("esc produced no cmd")
	}
	if _, ok := cmd().(catalogAccountListBackMsg); !ok {
		t.Fatalf("cmd() = %T, want catalogAccountListBackMsg", cmd())
	}
}

func TestCatalogAccountList_EnterOnAccountRow_IsNoOp(t *testing.T) {
	// Catalog credentials have no detail screen, so enter on an
	// account row is intentionally a no-op. Revoke is reachable
	// only via `r` — keeps the destructive action from sharing a
	// keybinding with a benign open-verb that doesn't exist here.
	v := memory.New()
	storeCatalogCred(t, v, "anthropic", "personal", "sk-ant-AAAA")
	m, _ := newCatalogAccountListModel(anthropicCatalogEntry(), v)
	// Cursor on personal (row 0).
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		// If there is a cmd, it must NOT be the revoke trigger.
		// (A nil cmd is the expected outcome.)
		if _, isRevoke := cmd().(catalogRevokeRequestMsg); isRevoke {
			t.Fatal("enter on account row erroneously triggered revoke")
		}
		if _, isAdd := cmd().(catalogAccountAddMsg); isAdd {
			t.Fatal("enter on account row erroneously triggered add")
		}
	}
	// Help line should not advertise enter for an open verb.
	if strings.Contains(m.View(), "enter open") {
		t.Errorf("help line still mentions 'enter open':\n%s", m.View())
	}
}

func TestCatalogAccountList_REmpty_OnAddRow_NoOp(t *testing.T) {
	v := memory.New()
	m, _ := newCatalogAccountListModel(anthropicCatalogEntry(), v)
	// Only + add row.
	_, cmd := m.Update(tea.KeyMsg{Runes: []rune{'r'}, Type: tea.KeyRunes})
	if cmd != nil {
		// Allow nil OR a no-op cmd; assert msg type is not the revoke
		// trigger.
		if _, isRevoke := cmd().(catalogRevokeRequestMsg); isRevoke {
			t.Fatal("r on + add row erroneously triggered revoke")
		}
	}
}
