package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xianxu/nous/lib/provider/providers/catalog"
	"github.com/xianxu/nous/lib/provider/vault"
	"github.com/xianxu/nous/lib/provider/vault/memory"
)

func newCatalogPasteFixture(t *testing.T) (catalogPasteModel, vault.Store) {
	t.Helper()
	v := memory.New()
	entry := catalog.Entry{
		ID:               "anthropic",
		Name:             "Anthropic",
		SignupURL:        "https://console.anthropic.com",
		KeyURL:           "https://console.anthropic.com/settings/keys",
		HostnamePatterns: []string{"api.anthropic.com"},
		Auth: catalog.Auth{
			Style:  "header",
			Header: "x-api-key",
		},
	}
	return newCatalogPasteModel(entry, v), v
}

// typeRunes drives a textinput by feeding each rune as a tea.KeyMsg.
// The bubbles/list / textinput Update path expects one rune-key per
// character.
func typeRunes(m catalogPasteModel, s string) catalogPasteModel {
	for _, r := range s {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	return m
}

func TestCatalogPaste_HappyPath_StoresCredAndEmitsDone(t *testing.T) {
	m, v := newCatalogPasteFixture(t)

	// Step 1: type account name
	m = typeRunes(m, "personal")
	if got := strings.TrimSpace(m.accountInput.Value()); got != "personal" {
		t.Fatalf("account input = %q, want %q", got, "personal")
	}

	// Enter advances to step 2
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.state != catalogPasteStateKey {
		t.Fatalf("state = %d after enter on account, want catalogPasteStateKey", m.state)
	}

	// Step 2: type key, enter to verify-then-store. Fixture entry has
	// no VerifyURL so Verify returns VerifyOK immediately as a no-op
	// — the verifying state still gets driven for state-machine
	// uniformity.
	m = typeRunes(m, "sk-ant-FAKE")
	m, verifyCmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.state != catalogPasteStateVerifying {
		t.Fatalf("state after enter on key = %d, want catalogPasteStateVerifying", m.state)
	}
	if verifyCmd == nil {
		t.Fatal("expected verify cmd from enter on key")
	}
	verifyMsg := verifyCmd()
	if _, ok := verifyMsg.(catalogVerifyResultMsg); !ok {
		t.Fatalf("verify cmd produced %T, want catalogVerifyResultMsg", verifyMsg)
	}

	// Drive the verify result back into the model — store should
	// land + done msg should fire.
	_, doneCmd := m.Update(verifyMsg)
	if doneCmd == nil {
		t.Fatal("expected done cmd after verify result")
	}

	// Vault should have the credential stored under anthropic/personal
	cred, err := v.Get("anthropic", "personal")
	if err != nil {
		t.Fatalf("vault.Get: %v", err)
	}
	if cred.CredType() != vault.TypeCatalog {
		t.Errorf("CredType = %q, want %q", cred.CredType(), vault.TypeCatalog)
	}
	if cred.Catalog == nil || cred.Catalog.KeyMaterial != "sk-ant-FAKE" {
		t.Errorf("Catalog.KeyMaterial = %+v, want sk-ant-FAKE", cred.Catalog)
	}

	// Cmd should emit catalogPasteDoneMsg with provider+account, no
	// verify note (entry has no VerifyURL → no claim).
	doneMsg := doneCmd()
	done, ok := doneMsg.(catalogPasteDoneMsg)
	if !ok {
		t.Fatalf("expected catalogPasteDoneMsg, got %T", doneMsg)
	}
	if done.provider != "anthropic" || done.account != "personal" {
		t.Errorf("done = %+v, want anthropic/personal", done)
	}
	if done.verifyNote != "" {
		t.Errorf("verifyNote = %q, want empty (no VerifyURL → no claim)", done.verifyNote)
	}
}

func TestCatalogPaste_EscFromAccount_EmitsCancel(t *testing.T) {
	m, v := newCatalogPasteFixture(t)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd == nil {
		t.Fatal("expected cmd from esc on account")
	}
	if _, ok := cmd().(catalogPasteCancelMsg); !ok {
		t.Errorf("expected catalogPasteCancelMsg, got %T", cmd())
	}
	creds, _ := v.List()
	if len(creds) != 0 {
		t.Errorf("vault should be empty after cancel, got %d creds", len(creds))
	}
}

func TestCatalogPaste_EnterOnEmptyAccount_DoesNotAdvance(t *testing.T) {
	m, _ := newCatalogPasteFixture(t)
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // no input yet
	if m.state != catalogPasteStateAccount {
		t.Errorf("state = %d, want catalogPasteStateAccount (empty account should not advance)", m.state)
	}
}

func TestCatalogPaste_EscFromKey_GoesBackToAccount(t *testing.T) {
	m, _ := newCatalogPasteFixture(t)
	m = typeRunes(m, "personal")
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // → key state
	m = typeRunes(m, "partial-key")

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.state != catalogPasteStateAccount {
		t.Fatalf("state = %d after esc on key, want catalogPasteStateAccount", m.state)
	}
	// Account name should be preserved
	if got := strings.TrimSpace(m.accountInput.Value()); got != "personal" {
		t.Errorf("account input lost on back-nav: %q", got)
	}
	// Key input should be cleared
	if got := m.keyInput.Value(); got != "" {
		t.Errorf("key input not cleared on back-nav: %q", got)
	}
}

func TestCatalogPaste_EnterOnEmptyKey_DoesNotStore(t *testing.T) {
	m, v := newCatalogPasteFixture(t)
	m = typeRunes(m, "personal")
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // → key state
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // empty key — no advance
	if m.state != catalogPasteStateKey {
		t.Errorf("state = %d, want catalogPasteStateKey", m.state)
	}
	creds, _ := v.List()
	if len(creds) != 0 {
		t.Errorf("vault should be empty after empty-key enter, got %d", len(creds))
	}
}

func TestCatalogPaste_ViewIncludesUrlsAndHost(t *testing.T) {
	m, _ := newCatalogPasteFixture(t)
	out := m.View()
	if !strings.Contains(out, "console.anthropic.com") {
		t.Errorf("view missing signup URL, got:\n%s", out)
	}
	if !strings.Contains(out, "/settings/keys") {
		t.Errorf("view missing key URL, got:\n%s", out)
	}
	if !strings.Contains(out, "api.anthropic.com") {
		t.Errorf("view missing hostname, got:\n%s", out)
	}
	if !strings.Contains(out, "ctrl+o") {
		t.Errorf("view missing ctrl+o hint, got:\n%s", out)
	}
}

func TestCatalogPaste_KeyViewIsMasked(t *testing.T) {
	m, _ := newCatalogPasteFixture(t)
	m = typeRunes(m, "personal")
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // → key state
	m = typeRunes(m, "sk-ant-FAKE-VISIBLE")
	out := m.View()
	if strings.Contains(out, "sk-ant-FAKE-VISIBLE") {
		t.Errorf("key view leaked plaintext key:\n%s", out)
	}
	if !strings.Contains(out, "Account: personal") {
		t.Errorf("key view should echo confirmed account name, got:\n%s", out)
	}
}

// fixtureWithVerify returns a paste model whose entry has the given
// verify_url so the M5 verify path is exercised.
func fixtureWithVerify(t *testing.T, verifyURL string) (catalogPasteModel, vault.Store) {
	t.Helper()
	v := memory.New()
	entry := catalog.Entry{
		ID:               "anthropic",
		Name:             "Anthropic",
		HostnamePatterns: []string{"api.anthropic.com"},
		Auth:             catalog.Auth{Style: "header", Header: "x-api-key"},
		VerifyURL:        verifyURL,
	}
	return newCatalogPasteModel(entry, v), v
}

// driveToVerifying runs the paste flow up to the verifying state for
// the given account+key, returning the model and the verify-result
// command. Lets the per-test code drive the verify result directly
// (bypassing the actual HTTP call) since httptest server addrs
// aren't yet wired into the entry at fixture-build time.
func driveToVerifying(t *testing.T, m catalogPasteModel, account, key string) (catalogPasteModel, tea.Cmd) {
	t.Helper()
	m = typeRunes(m, account)
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = typeRunes(m, key)
	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.state != catalogPasteStateVerifying {
		t.Fatalf("state = %d, want catalogPasteStateVerifying", m.state)
	}
	return m, cmd
}

func TestCatalogPaste_VerifyOK_StoresWithVerifiedNote(t *testing.T) {
	m, v := fixtureWithVerify(t, "https://example.test/verify")
	m, _ = driveToVerifying(t, m, "personal", "sk-ant-key")

	// Inject a successful verify result directly (avoids hitting the
	// example.test URL — entry.Verify exercised separately in
	// catalog/verify_test.go).
	_, doneCmd := m.Update(catalogVerifyResultMsg{result: catalog.VerifyOK})
	if doneCmd == nil {
		t.Fatal("VerifyOK should produce done cmd")
	}
	done, ok := doneCmd().(catalogPasteDoneMsg)
	if !ok {
		t.Fatalf("doneCmd() = %T, want catalogPasteDoneMsg", doneCmd())
	}
	if done.verifyNote != "verified" {
		t.Errorf("verifyNote = %q, want %q", done.verifyNote, "verified")
	}
	if _, err := v.Get("anthropic", "personal"); err != nil {
		t.Errorf("vault should have stored credential: %v", err)
	}
}

func TestCatalogPaste_VerifyRejected_DoesNotStore_GoesToError(t *testing.T) {
	m, v := fixtureWithVerify(t, "https://example.test/verify")
	m, _ = driveToVerifying(t, m, "personal", "bad-key")

	m, _ = m.Update(catalogVerifyResultMsg{
		result: catalog.VerifyRejected,
		err:    fmtErr("verify endpoint 401: invalid_api_key"),
	})
	if m.state != catalogPasteStateError {
		t.Fatalf("state after VerifyRejected = %d, want catalogPasteStateError", m.state)
	}
	if m.err == nil || !strings.Contains(m.err.Error(), "rejected the pasted key") {
		t.Errorf("err = %v, want 'rejected the pasted key' phrasing", m.err)
	}
	// Vault must NOT have the credential.
	if _, err := v.Get("anthropic", "personal"); err == nil {
		t.Error("vault should NOT have credential when verify rejects key")
	}

	// Any key returns to the key step so user can re-paste.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.state != catalogPasteStateKey {
		t.Errorf("after error-overlay key, state = %d, want catalogPasteStateKey", m.state)
	}
	if m.keyInput.Value() != "" {
		t.Errorf("key input should be cleared on retry, got %q", m.keyInput.Value())
	}
}

func TestCatalogPaste_VerifyEndpointError_StoresWithDegradedNote(t *testing.T) {
	m, v := fixtureWithVerify(t, "https://example.test/verify")
	m, _ = driveToVerifying(t, m, "personal", "sk-ant-key")

	_, doneCmd := m.Update(catalogVerifyResultMsg{
		result: catalog.VerifyEndpointError,
		err:    fmtErr("verify endpoint 503: service unavailable"),
	})
	if doneCmd == nil {
		t.Fatal("VerifyEndpointError should still emit done (store-anyway)")
	}
	done := doneCmd().(catalogPasteDoneMsg)
	if !strings.Contains(done.verifyNote, "verify inconclusive") {
		t.Errorf("verifyNote = %q, want 'verify inconclusive' phrasing", done.verifyNote)
	}
	if !strings.Contains(done.verifyNote, "503") {
		t.Errorf("verifyNote = %q, want underlying status (503) mentioned", done.verifyNote)
	}
	if _, err := v.Get("anthropic", "personal"); err != nil {
		t.Errorf("vault should still have stored credential despite verify failure: %v", err)
	}
}

func TestCatalogPaste_NoVerifyURL_SkipsVerifiedNote(t *testing.T) {
	// Existing newCatalogPasteFixture has no VerifyURL; happy-path
	// test already covers the empty-note case. This locks the
	// behavior explicitly: VerifyOK from a no-URL entry should not
	// claim "verified" since no verification actually happened.
	m, _ := newCatalogPasteFixture(t)
	m, _ = driveToVerifying(t, m, "personal", "sk-ant-key")

	_, doneCmd := m.Update(catalogVerifyResultMsg{result: catalog.VerifyOK})
	done := doneCmd().(catalogPasteDoneMsg)
	if done.verifyNote != "" {
		t.Errorf("verifyNote = %q, want empty (no VerifyURL → no claim)", done.verifyNote)
	}
}

// fmtErr is a tiny helper so test code can build errors inline
// without an extra import dance for fmt.Errorf.
func fmtErr(s string) error { return errFromString(s) }

type errFromString string

func (e errFromString) Error() string { return string(e) }
