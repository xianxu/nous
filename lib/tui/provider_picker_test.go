package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xianxu/nous/lib/provider/providers"
	"github.com/xianxu/nous/lib/provider/providers/catalog"
	"github.com/xianxu/nous/lib/provider/vault"
	"github.com/xianxu/nous/lib/provider/vault/memory"
)

// fakeAdminStore is a minimal AdminKeyStore for picker tests. Real
// store would touch the keychain; here we drive it via injectable IO
// so the picker sees the configured-state we want.
func fakeAdminStore(t *testing.T, provider string, configured bool, label string) *providers.AdminKeyStore {
	t.Helper()
	entries := map[string]string{}
	get := func(service, account string) (string, error) {
		v, ok := entries[account]
		if !ok {
			return "", _testErr(account)
		}
		return v, nil
	}
	set := func(service, account, value string) error {
		entries[account] = value
		return nil
	}
	del := func(service, account string) error {
		delete(entries, account)
		return nil
	}
	s := providers.NewAdminKeyStoreWithIO(provider, "charon-test", get, set, del)
	if configured {
		if err := s.Set("sk-test-admin", providers.AdminMeta{
			OrgID:    "org-test-001",
			OrgLabel: label,
			OrgName:  "test-org",
		}); err != nil {
			t.Fatalf("seed admin store: %v", err)
		}
	}
	return s
}

type _testErrType string

func (e _testErrType) Error() string { return "not found: " + string(e) }
func _testErr(s string) error        { return _testErrType(s) }

func TestProviderPicker_EmptyVault_NoAdminKeys(t *testing.T) {
	v := memory.New()
	m, err := newProviderPickerModel(v, nil, nil)
	if err != nil {
		t.Fatalf("newProviderPickerModel: %v", err)
	}
	// Always-present rows: Google + "+ add provider" — minimum 2.
	if len(m.items) != 2 {
		t.Errorf("expected 2 items (google + add-provider), got %d: %+v", len(m.items), m.items)
	}
	if m.items[0].name != "google" {
		t.Errorf("first item should be google, got %+v", m.items[0])
	}
	if !m.items[len(m.items)-1].isAddProvider {
		t.Errorf("last item should be + add provider, got %+v", m.items[len(m.items)-1])
	}
}

func TestProviderPicker_GoogleAccountCount_SingularPlural(t *testing.T) {
	cases := []struct {
		name     string
		accounts []string
		want     string
	}{
		{"none", nil, "0 accounts"},
		{"one", []string{"a@gmail.com"}, "1 account"},
		{"two", []string{"a@gmail.com", "b@gmail.com"}, "2 accounts"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := memory.New()
			for _, a := range tc.accounts {
				_ = v.Set(&vault.Credential{Provider: "google", Account: a})
			}
			m, _ := newProviderPickerModel(v, nil, nil)
			if m.items[0].summary != tc.want {
				t.Errorf("google summary = %q, want %q", m.items[0].summary, tc.want)
			}
		})
	}
}

func TestProviderPicker_AdminKey_RedWhenUnconfigured(t *testing.T) {
	v := memory.New()
	stores := map[string]*providers.AdminKeyStore{
		"openai": fakeAdminStore(t, "openai", false, ""),
	}
	m, _ := newProviderPickerModel(v, stores, nil)

	// Items: google, openai, +add-provider
	if len(m.items) < 3 {
		t.Fatalf("expected ≥3 items, got %d", len(m.items))
	}
	openai := m.items[1]
	if openai.name != "openai" {
		t.Fatalf("expected openai at index 1, got %+v", openai)
	}
	if openai.glyph != "○" {
		t.Errorf("unconfigured openai glyph = %q, want ○", openai.glyph)
	}
	if openai.adminKeySet {
		t.Error("unconfigured openai should have adminKeySet=false")
	}
	if !strings.Contains(openai.summary, "not set") {
		t.Errorf("unconfigured openai summary should mention 'not set', got %q", openai.summary)
	}
}

func TestProviderPicker_AdminKey_GreenWhenConfigured_WithMintCount(t *testing.T) {
	v := memory.New()
	// Two minted projects under openai.
	_ = v.Set(&vault.Credential{
		Type: vault.TypeAdminKey, Provider: "openai", Account: "work",
		AdminKey: &vault.AdminKeyData{OrgID: "org-test-001", ProjectID: "proj_1", KeyMaterial: "sk-test-1"},
	})
	_ = v.Set(&vault.Credential{
		Type: vault.TypeAdminKey, Provider: "openai", Account: "personal",
		AdminKey: &vault.AdminKeyData{OrgID: "org-test-001", ProjectID: "proj_2", KeyMaterial: "sk-test-2"},
	})
	stores := map[string]*providers.AdminKeyStore{
		"openai": fakeAdminStore(t, "openai", true, "xianxu@gmail.com"),
	}
	m, _ := newProviderPickerModel(v, stores, nil)

	openai := m.items[1]
	if openai.glyph != "●" {
		t.Errorf("configured openai glyph = %q, want ●", openai.glyph)
	}
	if !openai.adminKeySet {
		t.Error("configured openai should have adminKeySet=true")
	}
	if openai.summary != "2 keys" {
		t.Errorf("configured openai summary = %q, want '2 keys'", openai.summary)
	}
}

func TestProviderPicker_View_RendersCleanly(t *testing.T) {
	v := memory.New()
	_ = v.Set(&vault.Credential{Provider: "google", Account: "a@gmail.com"})
	stores := map[string]*providers.AdminKeyStore{
		"anthropic": fakeAdminStore(t, "anthropic", true, "me@example.com"),
		"openai":    fakeAdminStore(t, "openai", false, ""),
	}
	m, _ := newProviderPickerModel(v, stores, nil)
	view := m.View()

	for _, want := range []string{"Charon", "Provider", "Google", "OpenAI", "Anthropic", "+ add provider"} {
		if !strings.Contains(view, want) {
			t.Errorf("View() missing %q\n%s", want, view)
		}
	}
	if !strings.Contains(view, "1 account") {
		t.Error("Google should show 1 account")
	}
}

func TestProviderPicker_NavigationKeys(t *testing.T) {
	v := memory.New()
	stores := map[string]*providers.AdminKeyStore{
		"openai": fakeAdminStore(t, "openai", false, ""),
	}
	m, _ := newProviderPickerModel(v, stores, nil)
	// Items: google (0), openai (1), + add provider (2).
	// M7 onboarding lands cursor on row 2 for empty vault; reset
	// to 0 so this test exercises navigation from the top, not the
	// onboarding default (covered separately).
	m.cursor = 0

	if m.cursor != 0 {
		t.Fatalf("initial cursor = %d, want 0", m.cursor)
	}

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if m.cursor != 1 {
		t.Errorf("after down: cursor = %d, want 1", m.cursor)
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if m.cursor != 2 {
		t.Errorf("after second down: cursor = %d, want 2", m.cursor)
	}
	// Past-the-end is clamped, not wrapped.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if m.cursor != 2 {
		t.Errorf("over-the-end: cursor = %d, want 2 (clamped)", m.cursor)
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if m.cursor != 1 {
		t.Errorf("after up: cursor = %d, want 1", m.cursor)
	}
}

func TestProviderPicker_EnterEmitsSelectedMsg(t *testing.T) {
	v := memory.New()
	stores := map[string]*providers.AdminKeyStore{
		"openai": fakeAdminStore(t, "openai", false, ""),
	}
	m, _ := newProviderPickerModel(v, stores, nil)
	// M7 puts cursor on +add when vault is empty; reset to row 0
	// (Google) so this test exercises the providerSelected path.
	m.cursor = 0

	// Enter on google (cursor 0).
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter should emit a command")
	}
	msg := cmd()
	sel, ok := msg.(providerSelectedMsg)
	if !ok {
		t.Fatalf("expected providerSelectedMsg, got %T", msg)
	}
	if sel.name != "google" || sel.provType != vault.TypeOAuth {
		t.Errorf("selection mismatch: %+v", sel)
	}

	// Move to openai and enter.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	msg = cmd()
	sel = msg.(providerSelectedMsg)
	if sel.name != "openai" || sel.provType != vault.TypeAdminKey {
		t.Errorf("openai selection mismatch: %+v", sel)
	}
}

func TestProviderPicker_EnterOnAddProvider_EmitsAddMsg(t *testing.T) {
	v := memory.New()
	m, _ := newProviderPickerModel(v, nil, nil)
	// Cursor at 0 (google). Move to last (+ add provider).
	for m.cursor < len(m.items)-1 {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	}
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter should emit a command")
	}
	if _, ok := cmd().(addProviderMsg); !ok {
		t.Errorf("expected addProviderMsg from + add provider row, got %T", cmd())
	}
	// Top-level model handles the transition to screenCatalogPicker;
	// the picker itself no longer carries a status message (that
	// behavior was the M2-pre stub).
}

func TestProviderPicker_QuitKey(t *testing.T) {
	v := memory.New()
	m, _ := newProviderPickerModel(v, nil, nil)
	for _, key := range []string{"q", "esc", "ctrl+c"} {
		_, cmd := m.Update(tea.KeyMsg{Runes: []rune(key), Type: tea.KeyRunes})
		// `esc` and `ctrl+c` arrive as different bubbletea types; just
		// build the runes form and verify a tea.Quit-shaped command.
		_ = cmd
		_ = key
	}
	// Direct quit via the standard q rune.
	_, cmd := m.Update(tea.KeyMsg{Runes: []rune{'q'}, Type: tea.KeyRunes})
	if cmd == nil {
		t.Fatal("q should emit tea.Quit")
	}
	// Quit returns a quitMsg; assert by running cmd and checking type.
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Errorf("q should produce tea.QuitMsg, got %T", cmd())
	}
}

func TestProviderPicker_CatalogRow_AppearsWhenCatalogCredsExist(t *testing.T) {
	v := memory.New()
	storeCatalogCred(t, v, "anthropic", "personal", "sk-ant-AAAA")
	storeCatalogCred(t, v, "anthropic", "work", "sk-ant-ZZZZ")
	cat := &catalog.Catalog{Entries: []catalog.Entry{anthropicCatalogEntry()}}

	m, err := newProviderPickerModel(v, nil, cat)
	if err != nil {
		t.Fatalf("newProviderPickerModel: %v", err)
	}
	// Items: [google, anthropic-catalog, +add]. No admin-key providers.
	if got, want := len(m.items), 3; got != want {
		t.Fatalf("items = %d, want %d", got, want)
	}
	cat0 := m.items[1]
	if cat0.name != "anthropic" || cat0.provType != vault.TypeCatalog {
		t.Errorf("item[1] = %+v, want anthropic catalog", cat0)
	}
	if cat0.typeLabel != "API key" {
		t.Errorf("typeLabel = %q, want 'API key'", cat0.typeLabel)
	}
	if cat0.summary != "2 accounts" {
		t.Errorf("summary = %q, want '2 accounts'", cat0.summary)
	}
	if cat0.glyph != "●" {
		t.Errorf("glyph = %q, want ●", cat0.glyph)
	}

	// Enter on the catalog row should emit a TypeCatalog providerSelectedMsg.
	m.cursor = 1
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter on catalog row produced no cmd")
	}
	sel, ok := cmd().(providerSelectedMsg)
	if !ok {
		t.Fatalf("cmd() = %T, want providerSelectedMsg", cmd())
	}
	if sel.name != "anthropic" || sel.provType != vault.TypeCatalog {
		t.Errorf("sel = %+v, want {anthropic, catalog}", sel)
	}
}

func TestProviderPicker_CatalogRow_OmittedWhenNoCreds(t *testing.T) {
	v := memory.New()
	cat := &catalog.Catalog{Entries: []catalog.Entry{anthropicCatalogEntry()}}
	m, _ := newProviderPickerModel(v, nil, cat)
	// Items: [google, +add]. Anthropic catalog row is omitted.
	if got, want := len(m.items), 2; got != want {
		t.Fatalf("items = %d, want %d (google + add only)", got, want)
	}
	for _, it := range m.items {
		if it.provType == vault.TypeCatalog {
			t.Errorf("unexpected TypeCatalog row when no creds stored: %+v", it)
		}
	}
}

// Esc-back from a sub-screen should land the cursor where it was
// before drill-in (the row the user entered from). This is the
// general TUI principle "back-nav preserves state" applied to the
// provider picker — same convention as refreshAdminKeyList /
// refreshCatalogAccountList already follow for their entity lists.
func TestModel_BackNavToProviderPicker_PreservesCursor(t *testing.T) {
	v := memory.New()
	storeCatalogCred(t, v, "anthropic", "personal", "sk-ant-AAAA")
	cat := &catalog.Catalog{Entries: []catalog.Entry{anthropicCatalogEntry()}}

	m := model{vault: v, catalog: cat}
	pp, _ := newProviderPickerModel(v, nil, cat)
	m.providerPicker = pp
	m.current = screenProvider
	// Items: [Google, Anthropic, + add provider]. Move cursor to Anthropic.
	m.providerPicker.cursor = 1
	if m.providerPicker.items[1].name != "anthropic" {
		t.Fatalf("test fixture wrong: items[1] = %q, expected anthropic", m.providerPicker.items[1].name)
	}

	// Drill into Anthropic.
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm := updated.(model)
	if cmd == nil {
		t.Fatal("enter on Anthropic produced no cmd")
	}
	updated, _ = mm.Update(cmd())
	mm = updated.(model)
	if mm.current != screenCatalogAccountList {
		t.Fatalf("expected screenCatalogAccountList, got %v", mm.current)
	}

	// Esc back.
	updated, cmd = mm.Update(tea.KeyMsg{Type: tea.KeyEsc})
	mm = updated.(model)
	if cmd == nil {
		t.Fatal("esc should emit catalogAccountListBackMsg")
	}
	updated, _ = mm.Update(cmd())
	mm = updated.(model)
	if mm.current != screenProvider {
		t.Fatalf("after esc-back: current = %v, want screenProvider", mm.current)
	}

	// Cursor should still be on Anthropic row, not reset to 0 (Google).
	if mm.providerPicker.cursor != 1 {
		t.Errorf("cursor = %d after back-nav, want 1 (preserved on Anthropic)",
			mm.providerPicker.cursor)
	}
	if mm.providerPicker.items[mm.providerPicker.cursor].name != "anthropic" {
		t.Errorf("cursor row = %q, want anthropic",
			mm.providerPicker.items[mm.providerPicker.cursor].name)
	}
}

// Re-entering the same sub-screen restores the cursor where the
// user left it last time. In-session memory only — fresh model
// starts cursor at 0. Tests the catalog account list path; same
// logic applies to admin entity list and OAuth account picker.
// First-run UX (#15 M7): when no credentials are configured
// anywhere, cursor lands on the "+ add provider" row so the user's
// first Enter takes them somewhere actionable rather than into a
// detail screen for an empty provider.
func TestProviderPicker_EmptyVault_CursorOnAddProvider(t *testing.T) {
	v := memory.New()
	m, err := newProviderPickerModel(v, nil, nil)
	if err != nil {
		t.Fatalf("newProviderPickerModel: %v", err)
	}
	addIdx := len(m.items) - 1
	if !m.items[addIdx].isAddProvider {
		t.Fatalf("last row %+v not isAddProvider", m.items[addIdx])
	}
	if m.cursor != addIdx {
		t.Errorf("cursor = %d, want %d (+ add provider) on empty vault", m.cursor, addIdx)
	}
}

// Once any credential exists (vault non-empty), the M7 auto-cursor
// no longer fires — cursor lands at row 0 (Google) per pre-M7
// behavior so the picker doesn't aggressively redirect away from a
// configured provider.
func TestProviderPicker_NonEmptyVault_CursorAtZero(t *testing.T) {
	v := memory.New()
	_ = v.Set(&vault.Credential{Provider: "google", Account: "user@gmail.com"})
	m, _ := newProviderPickerModel(v, nil, nil)
	if m.cursor != 0 {
		t.Errorf("cursor = %d, want 0 (Google) when vault has creds", m.cursor)
	}
}

func TestModel_ReEntryToCatalogAccountList_RestoresCursor(t *testing.T) {
	v := memory.New()
	storeCatalogCred(t, v, "anthropic", "personal", "sk-ant-AAAA")
	storeCatalogCred(t, v, "anthropic", "work", "sk-ant-ZZZZ")
	cat := &catalog.Catalog{Entries: []catalog.Entry{anthropicCatalogEntry()}}

	m := model{
		vault:                 v,
		catalog:               cat,
		adminCursors:          map[string]int{},
		catalogAccountCursors: map[string]int{},
	}
	pp, _ := newProviderPickerModel(v, nil, cat)
	m.providerPicker = pp
	m.providerPicker.cursor = 1 // anthropic
	m.current = screenProvider

	// Drill into Anthropic.
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm := updated.(model)
	updated, _ = mm.Update(cmd())
	mm = updated.(model)
	if mm.current != screenCatalogAccountList {
		t.Fatalf("expected screenCatalogAccountList, got %v", mm.current)
	}
	// Move cursor down to "work" row (index 1).
	updated, _ = mm.Update(tea.KeyMsg{Type: tea.KeyDown})
	mm = updated.(model)
	if mm.catalogAccountList.cursor != 1 {
		t.Fatalf("after down: cursor = %d, want 1", mm.catalogAccountList.cursor)
	}

	// Esc back. Should save the cursor.
	updated, cmd = mm.Update(tea.KeyMsg{Type: tea.KeyEsc})
	mm = updated.(model)
	updated, _ = mm.Update(cmd())
	mm = updated.(model)
	if got := mm.catalogAccountCursors["anthropic"]; got != 1 {
		t.Errorf("catalogAccountCursors[anthropic] = %d, want 1 (saved on esc-back)", got)
	}

	// Re-enter Anthropic. Cursor should be restored.
	updated, cmd = mm.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm = updated.(model)
	updated, _ = mm.Update(cmd())
	mm = updated.(model)
	if mm.current != screenCatalogAccountList {
		t.Fatalf("after re-entry: current = %v, want screenCatalogAccountList", mm.current)
	}
	if mm.catalogAccountList.cursor != 1 {
		t.Errorf("after re-entry: cursor = %d, want 1 (restored from per-screen memory)",
			mm.catalogAccountList.cursor)
	}
}

// Catalog picker re-entry preserves cursor: the picker model is
// kept across `+ add provider` re-invocations rather than rebuilt,
// so cursor naturally persists.
func TestModel_ReEntryToCatalogPicker_PreservesCursor(t *testing.T) {
	v := memory.New()
	cat := &catalog.Catalog{Entries: []catalog.Entry{
		anthropicCatalogEntry(),
		{ID: "groq", Name: "Groq", HostnamePatterns: []string{"api.groq.com"}, Auth: catalog.Auth{Style: "bearer"}},
	}}
	m := model{
		vault:                 v,
		catalog:               cat,
		catalogPicker:         newCatalogPickerModel(cat),
		adminCursors:          map[string]int{},
		catalogAccountCursors: map[string]int{},
	}
	pp, _ := newProviderPickerModel(v, nil, cat)
	m.providerPicker = pp
	m.current = screenProvider

	// Trigger addProviderMsg manually (the bound + add provider row
	// emits this).
	updated, _ := m.Update(addProviderMsg{})
	mm := updated.(model)
	if mm.current != screenCatalogPicker {
		t.Fatalf("expected screenCatalogPicker, got %v", mm.current)
	}
	// Move cursor down to row 1 (groq).
	updated, _ = mm.Update(tea.KeyMsg{Type: tea.KeyDown})
	mm = updated.(model)
	if mm.catalogPicker.cursor != 1 {
		t.Fatalf("catalogPicker cursor = %d, want 1", mm.catalogPicker.cursor)
	}

	// Esc back to provider picker.
	updated, cmd := mm.Update(tea.KeyMsg{Type: tea.KeyEsc})
	mm = updated.(model)
	updated, _ = mm.Update(cmd())
	mm = updated.(model)
	if mm.current != screenProvider {
		t.Fatalf("after esc-back: current = %v, want screenProvider", mm.current)
	}

	// Re-enter catalog picker. Cursor should still be on row 1.
	updated, _ = mm.Update(addProviderMsg{})
	mm = updated.(model)
	if mm.current != screenCatalogPicker {
		t.Fatalf("re-entry: current = %v, want screenCatalogPicker", mm.current)
	}
	if mm.catalogPicker.cursor != 1 {
		t.Errorf("re-entry: catalogPicker cursor = %d, want 1 (preserved)",
			mm.catalogPicker.cursor)
	}
}

func TestProviderLabel_KnownAndUnknown(t *testing.T) {
	cases := map[string]string{
		"google":    "Google",
		"openai":    "OpenAI",
		"anthropic": "Anthropic",
		"groq":      "Groq", // Title-cased fallback
		"":          "",
	}
	for in, want := range cases {
		if got := providerLabel(in); got != want {
			t.Errorf("providerLabel(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestEntityTerm_PerProvider(t *testing.T) {
	if entityTerm("openai") != "project" || entityTermPlural("openai") != "projects" {
		t.Error("openai entity term wrong")
	}
	if entityTerm("anthropic") != "workspace" || entityTermPlural("anthropic") != "workspaces" {
		t.Error("anthropic entity term wrong")
	}
	if entityTerm("groq") != "account" || entityTermPlural("groq") != "accounts" {
		t.Error("default entity term should be account")
	}
}

// Smoke-test that newModel routes through newProviderPickerModel
// successfully when admin stores are wired.
func TestModel_StartsAtProviderPicker(t *testing.T) {
	v := memory.New()
	m, err := newModel(v, "")
	if err != nil {
		t.Fatalf("newModel: %v", err)
	}
	if m.current != screenProvider {
		t.Errorf("current = %v, want screenProvider", m.current)
	}
	// Renders without panic; not a snapshot test (chrome may evolve).
	view := m.View()
	if view == "" {
		t.Error("View on screenProvider rendered empty")
	}
	_ = time.Now() // silence unused-import if other helpers are removed
}
