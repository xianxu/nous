package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xianxu/nous/lib/provider/vault"
	"github.com/xianxu/nous/lib/provider/vault/memory"
)

// vaultWithBase returns a memory vault with required (openid + userinfo.email)
// scopes already granted for the account, plus any extra scopes. Use this in
// tests that want "no pending changes on load" as the baseline.
func vaultWithBase(account string, extra ...string) *memory.Store {
	v := memory.New()
	scopes := append([]string{
		"openid",
		"https://www.googleapis.com/auth/userinfo.email",
	}, extra...)
	v.Set(&vault.Credential{Provider: "google", Account: account, Scopes: scopes})
	return v
}

func TestLoadRowsCatalogOnly(t *testing.T) {
	rows, err := loadScopeRows(memory.New(), "nobody@gmail.com", nil)
	if err != nil {
		t.Fatalf("loadScopeRows: %v", err)
	}
	if len(rows) < 5 {
		t.Fatalf("expected catalog rows, got %d", len(rows))
	}
	for _, r := range rows {
		if r.realized || r.requested || r.custom {
			t.Errorf("unauthenticated load: row %q has unexpected flags set: %+v", r.short, r)
		}
		// Required rows force target=true; non-required start as target=false.
		if r.required && !r.target {
			t.Errorf("required row %q should have target=true", r.short)
		}
		if !r.required && r.target {
			t.Errorf("non-required row %q should have target=false on fresh load", r.short)
		}
	}
}

func TestLoadRowsMarksRealizedAndTarget(t *testing.T) {
	v := memory.New()
	v.Set(&vault.Credential{
		Provider: "google",
		Account:  "a@gmail.com",
		Scopes: []string{
			"https://www.googleapis.com/auth/gmail.readonly",
			"https://www.googleapis.com/auth/calendar.readonly",
		},
	})
	rows, err := loadScopeRows(v, "a@gmail.com", nil)
	if err != nil {
		t.Fatalf("loadScopeRows: %v", err)
	}
	got := map[string]bool{}
	for _, r := range rows {
		if r.realized {
			got[r.short] = true
		}
		// Required rows can have target!=realized (forced); skip them here.
		if !r.required && r.realized != r.target {
			t.Errorf("row %q: realized=%v target=%v (should match)", r.short, r.realized, r.target)
		}
	}
	for _, want := range []string{"gmail.readonly", "calendar.readonly"} {
		if !got[want] {
			t.Errorf("expected %q realized, missing from %v", want, got)
		}
	}
}

func TestLoadRowsGrantedFirstPreservesCatalogOrder(t *testing.T) {
	// Grant calendar.readonly + drive.readonly. They live mid-catalog.
	// After sort: those two should appear before any non-granted catalog
	// rows (e.g. gmail.readonly which sits earlier in the catalog), and
	// among themselves preserve catalog order (calendar.readonly before
	// drive.readonly per GoogleScopeCatalog).
	v := vaultWithBase("a@gmail.com",
		"https://www.googleapis.com/auth/drive.readonly",
		"https://www.googleapis.com/auth/calendar.readonly",
	)
	rows, err := loadScopeRows(v, "a@gmail.com", nil)
	if err != nil {
		t.Fatalf("loadScopeRows: %v", err)
	}

	// Walk: collect realized rows and the first non-realized row index.
	var realizedShorts []string
	firstNonRealized := -1
	for i, r := range rows {
		if r.realized {
			realizedShorts = append(realizedShorts, r.short)
		} else if firstNonRealized < 0 {
			firstNonRealized = i
		}
	}

	// Every realized row must come before every non-realized row.
	for i, r := range rows {
		if !r.realized && firstNonRealized < 0 {
			firstNonRealized = i
		}
		if firstNonRealized >= 0 && i > firstNonRealized && r.realized {
			t.Fatalf("realized row %q at index %d appears after non-realized at %d",
				r.short, i, firstNonRealized)
		}
	}

	// Within the realized group, catalog order is preserved:
	// openid, email (required) precede calendar.readonly, drive.readonly.
	idx := func(short string) int {
		for i, s := range realizedShorts {
			if s == short {
				return i
			}
		}
		return -1
	}
	if idx("calendar.readonly") < 0 || idx("drive.readonly") < 0 {
		t.Fatalf("missing realized rows: %v", realizedShorts)
	}
	if idx("calendar.readonly") > idx("drive.readonly") {
		t.Errorf("catalog order broken among realized: calendar.readonly should precede drive.readonly, got %v",
			realizedShorts)
	}
}

// Enter on a realized cloud-platform row must emit gcpSetupRequestMsg
// (carrying the account) instead of the default apply/quit. The user
// found the previous "quit silently" behavior confusing — granting
// the scope authorizes API calls but doesn't get them a usable
// project, so enter is repurposed here as the natural "manage
// project" affordance.
func TestEnterOnRealizedCloudPlatformLaunchesGCPSetup(t *testing.T) {
	v := vaultWithBase("alice@gmail.com",
		"https://www.googleapis.com/auth/cloud-platform",
	)
	rows, _ := loadScopeRows(v, "alice@gmail.com", nil)
	m := newScopesModel("alice@gmail.com", rows, nil)
	m.focus = focusList

	// Move cursor to the cloud-platform row.
	for i, idx := range m.filtered {
		if m.rows[idx].full == "https://www.googleapis.com/auth/cloud-platform" {
			m.cursor = i
			break
		}
	}
	if m.rows[m.filtered[m.cursor]].full != "https://www.googleapis.com/auth/cloud-platform" {
		t.Fatal("test setup: cursor not on cloud-platform row")
	}

	updated, cmd := m.updateList(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected a tea.Cmd; got nil")
	}
	msg := cmd()
	req, ok := msg.(gcpSetupRequestMsg)
	if !ok {
		t.Fatalf("expected gcpSetupRequestMsg, got %T (state=%d)", msg, updated.state)
	}
	if req.account != "alice@gmail.com" {
		t.Errorf("account = %q", req.account)
	}
}

// Enter on a non-realized cloud-platform row falls through to the
// default behavior — toggling cloud-platform off then back on must
// not accidentally trigger setup.
func TestEnterOnUngrantedCloudPlatformDoesNotLaunchSetup(t *testing.T) {
	v := vaultWithBase("alice@gmail.com")
	rows, _ := loadScopeRows(v, "alice@gmail.com", nil)
	m := newScopesModel("alice@gmail.com", rows, nil)
	m.focus = focusList

	for i, idx := range m.filtered {
		if m.rows[idx].full == "https://www.googleapis.com/auth/cloud-platform" {
			m.cursor = i
			break
		}
	}

	_, cmd := m.updateList(tea.KeyMsg{Type: tea.KeyEnter})
	// With no pending changes and no realized cloud-platform, enter
	// is the legacy "quit" path. Specifically NOT a gcpSetupRequest.
	if cmd != nil {
		if _, ok := cmd().(gcpSetupRequestMsg); ok {
			t.Error("did not expect gcpSetupRequestMsg for unrealized row")
		}
	}
}

func TestLoadRowsAppendsCustomScopes(t *testing.T) {
	v := memory.New()
	v.Set(&vault.Credential{
		Provider: "google",
		Account:  "a@gmail.com",
		Scopes:   []string{"https://www.googleapis.com/auth/some.unknown.scope"},
	})
	rows, err := loadScopeRows(v, "a@gmail.com", nil)
	if err != nil {
		t.Fatalf("loadScopeRows: %v", err)
	}
	var custom *scopeRow
	for i := range rows {
		if rows[i].custom {
			custom = &rows[i]
			break
		}
	}
	if custom == nil {
		t.Fatal("expected a custom row for the unknown scope")
	}
	if custom.short != "some.unknown.scope" {
		t.Errorf("custom short = %q, want some.unknown.scope", custom.short)
	}
	if !custom.realized || !custom.target {
		t.Errorf("custom row from keychain should be realized+target")
	}
}

func TestLoadRowsBadgesFromDenials(t *testing.T) {
	v := memory.New()
	fetcher := func(account string) []string {
		return []string{
			"https://www.googleapis.com/auth/calendar.readonly", // catalog
			"https://www.googleapis.com/auth/some.brand.new",    // not in catalog
		}
	}
	rows, err := loadScopeRows(v, "a@gmail.com", fetcher)
	if err != nil {
		t.Fatalf("loadScopeRows: %v", err)
	}
	var calReq, customReq *scopeRow
	for i := range rows {
		if rows[i].short == "calendar.readonly" {
			calReq = &rows[i]
		}
		if rows[i].full == "https://www.googleapis.com/auth/some.brand.new" {
			customReq = &rows[i]
		}
	}
	if calReq == nil || !calReq.requested {
		t.Errorf("catalog row not marked requested: %+v", calReq)
	}
	if customReq == nil {
		t.Fatal("brand-new denied scope not appended as custom row")
	}
	if !customReq.requested || !customReq.custom {
		t.Errorf("custom requested row flags wrong: %+v", customReq)
	}
}

func TestLoadRowsBadgesShortNameDenials(t *testing.T) {
	// Agents declare scopes via short name; the proxy stores denials in
	// whatever form was declared. We must still match those to the catalog
	// row whose `full` is the resolved URL — otherwise the catalog row
	// stays unbadged and the denial appears as a duplicate "custom" row.
	v := memory.New()
	fetcher := func(account string) []string {
		return []string{"gmail.readonly"} // short form
	}
	rows, err := loadScopeRows(v, "a@gmail.com", fetcher)
	if err != nil {
		t.Fatalf("loadScopeRows: %v", err)
	}

	// The catalog row should be marked requested.
	var catalogRow *scopeRow
	customCount := 0
	for i := range rows {
		if rows[i].short == "gmail.readonly" && !rows[i].custom {
			catalogRow = &rows[i]
		}
		if rows[i].custom {
			customCount++
		}
	}
	if catalogRow == nil {
		t.Fatal("catalog row gmail.readonly not found")
	}
	if !catalogRow.requested {
		t.Errorf("catalog row gmail.readonly should be requested=true after short-name denial")
	}
	if customCount != 0 {
		t.Errorf("expected 0 custom rows after canonical match, got %d", customCount)
	}
}

func TestLoadRowsTolerantOfNilFetcher(t *testing.T) {
	rows, err := loadScopeRows(memory.New(), "a@gmail.com", nil)
	if err != nil || len(rows) == 0 {
		t.Fatalf("nil fetcher should still load catalog: %v %d", err, len(rows))
	}
	for _, r := range rows {
		if r.requested {
			t.Errorf("nil fetcher: row %q marked requested", r.short)
		}
	}
}

func TestRowMatchesFilter(t *testing.T) {
	r := scopeRow{short: "gmail.readonly", description: "Read Gmail messages"}
	cases := []struct {
		filter string
		want   bool
	}{
		{"", true},
		{"gmail", true},
		{"GMAIL", true},
		{"readonly", true},
		{"messages", true},
		{"calendar", false},
	}
	for _, tc := range cases {
		if got := r.matches(tc.filter); got != tc.want {
			t.Errorf("matches(%q) = %v, want %v", tc.filter, got, tc.want)
		}
	}
}

func TestScopesFocusToggle(t *testing.T) {
	rows, _ := loadScopeRows(vaultWithBase("a@gmail.com"), "a@gmail.com", nil)
	m := newScopesModel("a@gmail.com", rows, nil)

	// Initial focus is search.
	if m.focus != focusSearch {
		t.Fatalf("initial focus = %v, want search", m.focus)
	}

	// Down moves focus to list (when there are filtered rows).
	m, _ = m.Update(keyPress("down"))
	if m.focus != focusList {
		t.Errorf("after down: focus = %v, want list", m.focus)
	}
	if m.cursor != 0 {
		t.Errorf("after down: cursor = %d, want 0", m.cursor)
	}

	// Up at cursor=0 returns to search.
	m, _ = m.Update(keyPress("up"))
	if m.focus != focusSearch {
		t.Errorf("up at cursor 0: focus = %v, want search", m.focus)
	}

	// Down → list, then `/` returns to search.
	m, _ = m.Update(keyPress("down"))
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	if m.focus != focusSearch {
		t.Errorf("after / from list: focus = %v, want search", m.focus)
	}

	// From list, esc returns to search (does NOT quit when in list focus).
	m, _ = m.Update(keyPress("down"))
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.focus != focusSearch {
		t.Errorf("after esc from list: focus = %v, want search", m.focus)
	}
}

func TestScopesEscFromSearchSignalsQuit(t *testing.T) {
	rows, _ := loadScopeRows(vaultWithBase("a@gmail.com"), "a@gmail.com", nil)
	m := newScopesModel("a@gmail.com", rows, nil)

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd == nil {
		t.Fatal("esc from search: expected quit command")
	}
	if _, ok := cmd().(scopesQuitMsg); !ok {
		t.Fatalf("expected scopesQuitMsg, got %T", cmd())
	}
}

func TestScopesQFromListSignalsQuit(t *testing.T) {
	rows, _ := loadScopeRows(vaultWithBase("a@gmail.com"), "a@gmail.com", nil)
	m := newScopesModel("a@gmail.com", rows, nil)

	// Move to list focus first.
	m, _ = m.Update(keyPress("down"))
	if m.focus != focusList {
		t.Fatalf("setup: focus not list")
	}
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if cmd == nil {
		t.Fatal("q from list: expected quit command")
	}
	if _, ok := cmd().(scopesQuitMsg); !ok {
		t.Fatalf("expected scopesQuitMsg, got %T", cmd())
	}
}

func TestScopesFilterReducesVisibleRows(t *testing.T) {
	rows, _ := loadScopeRows(memory.New(), "a@gmail.com", nil)
	m := newScopesModel("a@gmail.com", rows, nil)
	totalCatalogRows := len(m.filtered)

	// Type "gmail" — should reduce to gmail.* rows.
	for _, r := range "gmail" {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if len(m.filtered) == 0 || len(m.filtered) >= totalCatalogRows {
		t.Errorf("filter 'gmail': %d rows, want a strict subset of %d", len(m.filtered), totalCatalogRows)
	}
	for _, idx := range m.filtered {
		row := m.rows[idx]
		if !row.matches("gmail") {
			t.Errorf("filtered row %q doesn't match 'gmail'", row.short)
		}
	}
}

func TestScopesViewRendersExpectedContent(t *testing.T) {
	v := memory.New()
	v.Set(&vault.Credential{
		Provider: "google",
		Account:  "a@gmail.com",
		Scopes:   []string{"https://www.googleapis.com/auth/gmail.readonly"},
	})
	fetcher := func(string) []string {
		return []string{"https://www.googleapis.com/auth/calendar.readonly"}
	}
	rows, _ := loadScopeRows(v, "a@gmail.com", fetcher)
	m := newScopesModel("a@gmail.com", rows, nil)
	out := m.View()

	for _, want := range []string{
		"google / a@gmail.com",
		"granted",
		"gmail.readonly",
		"calendar.readonly",
		"Read Gmail messages",
		// proxy-requested rows are conveyed via muted-yellow color now,
		// no '!' character marker
	} {
		if !strings.Contains(out, want) {
			t.Errorf("View() missing %q\nout:\n%s", want, out)
		}
	}
}

func TestScopesNoPendingChangesInM2(t *testing.T) {
	v := vaultWithBase("a@gmail.com", "https://www.googleapis.com/auth/gmail.readonly")
	rows, _ := loadScopeRows(v, "a@gmail.com", nil)
	m := newScopesModel("a@gmail.com", rows, nil)
	if m.pendingChanges() {
		t.Error("M2 view-only: pendingChanges() must be false on load")
	}
}

func TestPickerToScopesTransition(t *testing.T) {
	v := memory.New()
	v.Set(&vault.Credential{Provider: "google", Account: "a@gmail.com"})

	m, err := newModel(v, "")
	if err != nil {
		t.Fatalf("newModel: %v", err)
	}
	// Post-#13: the entry point is the provider picker, not the OAuth
	// account picker. Drilling in: provider → OAuth picker → scopes.
	if m.current != screenProvider {
		t.Fatalf("initial screen = %v, want screenProvider", m.current)
	}

	updated, _ := m.Update(providerSelectedMsg{name: "google", provType: vault.TypeOAuth})
	m = updated.(model)
	if m.current != screenPicker {
		t.Fatalf("after google selection: screen = %v, want screenPicker", m.current)
	}

	updated, _ = m.Update(accountSelectedMsg{email: "a@gmail.com"})
	m = updated.(model)
	if m.current != screenScopes {
		t.Errorf("after account selection: screen = %v, want scopes", m.current)
	}
	if m.scopes.account != "a@gmail.com" {
		t.Errorf("scope view account = %q, want a@gmail.com", m.scopes.account)
	}
}

func TestNewAccountWithoutAuthErrors(t *testing.T) {
	// + new account without an Authenticator can't proceed; should exit
	// cleanly with an error.
	m, _ := newModel(memory.New(), "")
	updated, cmd := m.Update(newAccountMsg{})
	mm := updated.(model)
	if mm.err == nil {
		t.Error("expected err to be set when no authenticator is wired")
	}
	if cmd == nil {
		t.Error("expected tea.Quit command")
	}
}

func TestNewAccountAuthedTransitionsToScopes(t *testing.T) {
	v := memory.New()
	authed := &vault.Credential{
		Provider: "google",
		Account:  "newuser@gmail.com",
		Scopes:   []string{"openid", "https://www.googleapis.com/auth/userinfo.email"},
	}
	m, _ := newModel(v, "")
	updated, _ := m.Update(newAccountAuthedMsg{cred: authed})
	mm := updated.(model)
	if mm.current != screenScopes {
		t.Errorf("after newAccountAuthedMsg: current=%v, want screenScopes", mm.current)
	}
	if mm.scopes.account != "newuser@gmail.com" {
		t.Errorf("scopes.account = %q, want newuser@gmail.com", mm.scopes.account)
	}
	stored, err := v.Get("google", "newuser@gmail.com")
	if err != nil {
		t.Fatalf("vault.Get after newAccountAuthedMsg: %v", err)
	}
	if len(stored.Scopes) != 2 {
		t.Errorf("vault stored scopes count = %d, want 2", len(stored.Scopes))
	}
}

func TestInitialAccountSkipsPicker(t *testing.T) {
	v := memory.New()
	v.Set(&vault.Credential{Provider: "google", Account: "a@gmail.com"})
	m, err := newModel(v, "a@gmail.com")
	if err != nil {
		t.Fatalf("newModel: %v", err)
	}
	if m.current != screenScopes {
		t.Errorf("with initialAccount: screen = %v, want scopes", m.current)
	}
}
