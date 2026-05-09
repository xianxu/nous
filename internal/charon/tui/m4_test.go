package tui

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xianxu/nous/internal/charon/providers"
	"github.com/xianxu/nous/internal/charon/vault"
	"github.com/xianxu/nous/internal/charon/vault/memory"
)

// untoggleGmailReadonly returns a model with cursor on the gmail.readonly
// row and that row toggled off (i.e. a pending reduction).
func untoggleGmailReadonly(t *testing.T, v vault.Store, auth Authenticator) scopesModel {
	t.Helper()
	m := newScopesForTest(t, v, "a@gmail.com", auth)
	m, _ = moveToFirstListRow(t, m)
	for i, r := range m.rows {
		if r.short == "gmail.readonly" {
			for visIdx, rowIdx := range m.filtered {
				if rowIdx == i {
					m.cursor = visIdx
				}
			}
			break
		}
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeySpace})
	return m
}

func TestReduceConfirmContinueDispatchesAuthForceFresh(t *testing.T) {
	v := vaultWithBase("a@gmail.com", "https://www.googleapis.com/auth/gmail.readonly")
	auth := &stubAuth{
		returnCred: &vault.Credential{
			Provider: "google", Account: "a@gmail.com",
			Scopes: []string{
				"openid",
				"https://www.googleapis.com/auth/userinfo.email",
			},
		},
	}
	m := untoggleGmailReadonly(t, v, auth)

	// Open confirmation modal.
	m, _ = m.Update(keyPress("enter"))
	if m.state != stateReduceConfirm {
		t.Fatalf("after enter on reduction: state=%v want stateReduceConfirm", m.state)
	}

	// User presses 'y' to continue.
	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	if m.state != stateApplying {
		t.Errorf("after y: state=%v want stateApplying", m.state)
	}
	if cmd == nil {
		t.Fatal("expected applyCmd")
	}
	msg := cmd()
	if _, ok := msg.(applyResultMsg); !ok {
		t.Fatalf("expected applyResultMsg, got %T", msg)
	}
	if auth.calls != 1 {
		t.Errorf("auth calls = %d, want 1", auth.calls)
	}
	if !auth.gotForceFresh {
		t.Errorf("expected forceFresh=true on reductive apply, got false")
	}
}

func TestReduceConfirmCancelReturnsNormal(t *testing.T) {
	v := vaultWithBase("a@gmail.com", "https://www.googleapis.com/auth/gmail.readonly")
	auth := &stubAuth{}
	m := untoggleGmailReadonly(t, v, auth)
	m, _ = m.Update(keyPress("enter"))
	if m.state != stateReduceConfirm {
		t.Fatalf("setup: not in stateReduceConfirm")
	}

	for _, key := range []string{"n", "c"} {
		mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
		if mm.state != stateNormal {
			t.Errorf("after %q: state=%v want stateNormal", key, mm.state)
		}
		if auth.calls != 0 {
			t.Errorf("auth should not be called on cancel, got %d", auth.calls)
		}
	}
	mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if mm.state != stateNormal {
		t.Errorf("after esc: state=%v want stateNormal", mm.state)
	}
}

func TestAdditiveApplyUsesForceFreshFalse(t *testing.T) {
	v := vaultWithBase("a@gmail.com")
	auth := &stubAuth{
		returnCred: &vault.Credential{
			Provider: "google", Account: "a@gmail.com",
			Scopes: []string{
				"openid",
				"https://www.googleapis.com/auth/userinfo.email",
				"https://www.googleapis.com/auth/gmail.readonly",
			},
		},
	}
	m := newScopesForTest(t, v, "a@gmail.com", auth)
	m, _ = moveToFirstListRow(t, m)
	// Toggle gmail.readonly on (additive).
	for i, r := range m.rows {
		if r.short == "gmail.readonly" {
			for visIdx, rowIdx := range m.filtered {
				if rowIdx == i {
					m.cursor = visIdx
				}
			}
			break
		}
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeySpace})

	// Enter on additive change goes straight to applying — no modal.
	m, cmd := m.Update(keyPress("enter"))
	if m.state != stateApplying {
		t.Errorf("additive enter: state=%v want stateApplying", m.state)
	}
	if cmd == nil {
		t.Fatal("expected applyCmd")
	}
	cmd()
	if auth.gotForceFresh {
		t.Errorf("additive apply: forceFresh should be false")
	}
}

func TestRevokeChordOpensConfirmModalFromList(t *testing.T) {
	v := vaultWithBase("a@gmail.com")
	auth := &stubAuth{}
	m := newScopesForTest(t, v, "a@gmail.com", auth)
	m, _ = moveToFirstListRow(t, m)

	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlR})
	if m.state != stateRevokeConfirm {
		t.Errorf("after ctrl+r in list: state=%v want stateRevokeConfirm", m.state)
	}
	if cmd != nil {
		t.Errorf("ctrl+r alone should not dispatch a command, got %T", cmd())
	}
}

func TestRevokeChordOpensConfirmModalFromSearch(t *testing.T) {
	v := vaultWithBase("a@gmail.com")
	auth := &stubAuth{}
	m := newScopesForTest(t, v, "a@gmail.com", auth)
	// Default focus is search.

	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlR})
	if m.state != stateRevokeConfirm {
		t.Errorf("after ctrl+r in search: state=%v want stateRevokeConfirm", m.state)
	}
	if cmd != nil {
		t.Errorf("ctrl+r alone should not dispatch a command, got %T", cmd())
	}
}

func TestRevokeConfirmEmitsRevokeAccountMsg(t *testing.T) {
	v := vaultWithBase("a@gmail.com")
	auth := &stubAuth{}
	m := newScopesForTest(t, v, "a@gmail.com", auth)
	m.state = stateRevokeConfirm

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	if cmd == nil {
		t.Fatal("expected revokeAccountMsg cmd")
	}
	msg := cmd()
	r, ok := msg.(revokeAccountMsg)
	if !ok {
		t.Fatalf("expected revokeAccountMsg, got %T", msg)
	}
	if r.account != "a@gmail.com" {
		t.Errorf("account = %q, want a@gmail.com", r.account)
	}
}

func TestRevokeConfirmCancelReturnsNormal(t *testing.T) {
	v := vaultWithBase("a@gmail.com")
	m := newScopesForTest(t, v, "a@gmail.com", &stubAuth{})
	m.state = stateRevokeConfirm

	for _, key := range []string{"n", "c"} {
		mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
		if mm.state != stateNormal {
			t.Errorf("after %q: state=%v want stateNormal", key, mm.state)
		}
	}
	mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if mm.state != stateNormal {
		t.Errorf("after esc: state=%v want stateNormal", mm.state)
	}
}

func TestSessionMarkersPersistAfterApply(t *testing.T) {
	// Start with openid+email+gmail.readonly granted; user grants gmail.send
	// and revokes gmail.readonly; we expect after apply that send shows +
	// and readonly shows -, with initialRealized snapshot intact.
	v := vaultWithBase("a@gmail.com", "https://www.googleapis.com/auth/gmail.readonly")
	auth := &stubAuth{
		returnCred: &vault.Credential{
			Provider: "google", Account: "a@gmail.com",
			Scopes: []string{
				"openid",
				"https://www.googleapis.com/auth/userinfo.email",
				"https://www.googleapis.com/auth/gmail.send",
			},
		},
	}
	m := newScopesForTest(t, v, "a@gmail.com", auth)

	// Verify initialRealized was captured.
	for _, r := range m.rows {
		if r.short == "gmail.readonly" && !r.initialRealized {
			t.Errorf("gmail.readonly should have initialRealized=true at load")
		}
		if r.short == "gmail.send" && r.initialRealized {
			t.Errorf("gmail.send should have initialRealized=false at load")
		}
	}

	// Simulate apply result delivering the new credential.
	result := applyResultMsg{cred: auth.returnCred}
	m = m.handleApplyResult(result)

	// gmail.send: was off, now realized → marker should be "+"
	// gmail.readonly: was realized, now off → marker should be "-"
	for _, r := range m.rows {
		if r.short == "gmail.send" {
			if r.initialRealized {
				t.Errorf("gmail.send: initialRealized was clobbered by apply (want false)")
			}
			if !r.realized {
				t.Errorf("gmail.send: should be realized after apply")
			}
		}
		if r.short == "gmail.readonly" {
			if !r.initialRealized {
				t.Errorf("gmail.readonly: initialRealized was clobbered by apply (want true)")
			}
			if r.realized {
				t.Errorf("gmail.readonly: should not be realized after apply")
			}
		}
	}

	// Render and check the marker column shows + and -.
	view := m.View()
	if !strings.Contains(view, "+ gmail.send") {
		t.Errorf("expected '+ gmail.send' marker in rendered view")
	}
	if !strings.Contains(view, "- gmail.readonly") {
		t.Errorf("expected '- gmail.readonly' marker in rendered view")
	}
}

func TestModelHandlesRevokeAccountMsg(t *testing.T) {
	v := vaultWithBase("a@gmail.com")
	// Add a refresh token so Revoke gets a non-empty argument.
	cred, _ := v.Get("google", "a@gmail.com")
	cred.RefreshToken = "fake-refresh-token"
	v.Set(cred)

	auth := &stubAuth{}
	m, err := newModel(v, "a@gmail.com", WithAuthenticator(auth))
	if err != nil {
		t.Fatalf("newModel: %v", err)
	}

	updated, cmd := m.Update(revokeAccountMsg{account: "a@gmail.com"})
	mm := updated.(model)

	if auth.revokeCalls != 1 {
		t.Errorf("Revoke calls = %d, want 1", auth.revokeCalls)
	}
	if auth.gotRevokeTok != "fake-refresh-token" {
		t.Errorf("Revoke got %q, want fake-refresh-token", auth.gotRevokeTok)
	}
	if _, err := v.Get("google", "a@gmail.com"); err == nil {
		t.Error("vault should have deleted the credential after revoke")
	}
	if cmd == nil {
		t.Error("expected tea.Quit cmd after revoke")
	}
	if mm.exitNote == "" {
		t.Error("expected exitNote describing the revoke")
	}
}

// #14 M6: revoke must DELETE the account's AI Studio key upstream
// before invalidating the OAuth token. Order matters because the
// DELETE call uses the OAuth bearer for auth — revoking the OAuth
// token first would 401 the upstream cleanup.
func TestRevokeAccount_DeletesAIStudioKey_BeforeRevoke(t *testing.T) {
	v := vaultWithBase("a@gmail.com")
	cred, _ := v.Get("google", "a@gmail.com")
	cred.RefreshToken = "rt"
	cred.AIStudio = &vault.AIStudioData{
		Name:        "projects/p/locations/global/keys/uid-1",
		UID:         "uid-1",
		KeyMaterial: "AIzaSy_FAKE",
		ProjectID:   "p",
	}
	v.Set(cred)

	auth := &stubAuth{}
	fake := &fakeGCPClient{}
	factory := func(account string) (GCPSetupClient, error) { return fake, nil }
	m, err := newModel(v, "a@gmail.com",
		WithAuthenticator(auth),
		WithGCPClientFactory(factory),
	)
	if err != nil {
		t.Fatalf("newModel: %v", err)
	}

	_, _ = m.Update(revokeAccountMsg{account: "a@gmail.com"})

	if fake.deleteAPIKeyCalls != 1 {
		t.Errorf("DeleteAPIKey calls = %d, want 1", fake.deleteAPIKeyCalls)
	}
	if fake.lastDeletedAPIKey != "projects/p/locations/global/keys/uid-1" {
		t.Errorf("DeleteAPIKey arg = %q", fake.lastDeletedAPIKey)
	}
	if auth.revokeCalls != 1 {
		t.Errorf("Revoke calls = %d, want 1", auth.revokeCalls)
	}
	if _, err := v.Get("google", "a@gmail.com"); err == nil {
		t.Error("vault entry should be gone after revoke")
	}
}

// AI Studio DELETE failure must NOT block the local revoke. User
// asked for the account gone; charon respects that and surfaces
// a partial-failure status note so the user can clean up manually.
func TestRevokeAccount_AIStudioDeleteFailureNonFatal(t *testing.T) {
	v := vaultWithBase("a@gmail.com")
	cred, _ := v.Get("google", "a@gmail.com")
	cred.RefreshToken = "rt"
	cred.AIStudio = &vault.AIStudioData{
		Name:        "projects/p/locations/global/keys/uid-1",
		KeyMaterial: "AIzaSy_FAKE",
	}
	v.Set(cred)

	auth := &stubAuth{}
	fake := &fakeGCPClient{deleteAPIKeyErr: errors.New("403 forbidden")}
	factory := func(account string) (GCPSetupClient, error) { return fake, nil }
	m, _ := newModel(v, "a@gmail.com",
		WithAuthenticator(auth),
		WithGCPClientFactory(factory),
	)
	_, _ = m.Update(revokeAccountMsg{account: "a@gmail.com"})

	// Local revoke must still have happened.
	if _, err := v.Get("google", "a@gmail.com"); err == nil {
		t.Error("local revoke must proceed despite upstream DELETE failure")
	}
	if auth.revokeCalls != 1 {
		t.Error("OAuth Revoke must still happen even when AIStudio DELETE fails")
	}
}

// Skipping AIStudio cleanup is correct when the credential has no
// AIStudio sidecar (typical for accounts pre-M4 or accounts that
// never granted cloud-platform).
func TestRevokeAccount_NoAIStudio_SkipsDeleteAPIKey(t *testing.T) {
	v := vaultWithBase("a@gmail.com")
	cred, _ := v.Get("google", "a@gmail.com")
	cred.RefreshToken = "rt"
	v.Set(cred) // no AIStudio sidecar

	auth := &stubAuth{}
	fake := &fakeGCPClient{}
	factory := func(account string) (GCPSetupClient, error) { return fake, nil }
	m, _ := newModel(v, "a@gmail.com",
		WithAuthenticator(auth),
		WithGCPClientFactory(factory),
	)
	_, _ = m.Update(revokeAccountMsg{account: "a@gmail.com"})

	if fake.deleteAPIKeyCalls != 0 {
		t.Errorf("DeleteAPIKey should not be called when no AIStudio sidecar; got %d calls", fake.deleteAPIKeyCalls)
	}
}

// M6: revoke through the OAuth picker (not initial-account mode)
// returns to the picker (rebuilt) rather than exiting. The deleted
// account is gone from the picker's items; statusMsg names what
// happened. Mirrors the admin-key revoke flow's return-to-list shape.
func TestRevokeAccount_FromPicker_ReturnsToList(t *testing.T) {
	v := vaultWithBase("a@gmail.com")
	v.Set(&vault.Credential{Provider: "google", Account: "b@gmail.com", RefreshToken: "rt-b"})

	auth := &stubAuth{}
	// No initialAccount → newModel routes through the provider picker.
	m, err := newModel(v, "", WithAuthenticator(auth))
	if err != nil {
		t.Fatalf("newModel: %v", err)
	}
	// Bring up the OAuth picker as if user had already selected Google.
	updated, _ := m.Update(providerSelectedMsg{name: "google", provType: vault.TypeOAuth})
	mm := updated.(model)
	if mm.current != screenPicker {
		t.Fatalf("expected screenPicker after google selection, got %v", mm.current)
	}
	if len(mm.picker.items) != 3 { // a, b, +new
		t.Fatalf("picker.items = %d, want 3 (a + b + new), got: %+v", len(mm.picker.items), mm.picker.items)
	}

	// Drive the revoke message directly (the picker confirm modal is
	// covered by picker_test.go).
	updated, cmd := mm.Update(revokeAccountMsg{account: "a@gmail.com"})
	mm = updated.(model)

	// Should NOT exit. cmd is nil (no tea.Quit).
	if cmd != nil {
		t.Errorf("expected no tea.Quit command after picker-revoke, got %v", cmd)
	}
	if mm.current != screenPicker {
		t.Errorf("expected to return to screenPicker, got %v", mm.current)
	}
	if mm.exitNote != "" {
		t.Errorf("exitNote should be empty when staying in picker, got %q", mm.exitNote)
	}

	// Vault entry for a@gmail.com is gone.
	if _, err := v.Get("google", "a@gmail.com"); err == nil {
		t.Error("vault should have deleted a@gmail.com after revoke")
	}

	// Rebuilt picker no longer lists a@gmail.com; b@gmail.com survives.
	if len(mm.picker.items) != 2 { // b + new
		t.Errorf("picker.items after revoke = %d, want 2 (b + new): %+v", len(mm.picker.items), mm.picker.items)
	}
	for _, it := range mm.picker.items {
		if it.email == "a@gmail.com" {
			t.Errorf("revoked account a@gmail.com should not appear in rebuilt picker: %+v", it)
		}
	}

	// Status note communicates the outcome.
	if !strings.Contains(mm.picker.statusMsg, "a@gmail.com") {
		t.Errorf("picker statusMsg should name the revoked account, got %q", mm.picker.statusMsg)
	}
}

// Chunk-2 review #5: cursor stays at the same row index after a
// picker rebuild rather than jumping back to 0. Removing an item
// before the cursor's row clamps; removing the cursored row leaves
// the cursor on the next account at the same index.
func TestRevokeAccount_FromPicker_PreservesCursor(t *testing.T) {
	v := vaultWithBase("a@gmail.com")
	v.Set(&vault.Credential{Provider: "google", Account: "b@gmail.com", RefreshToken: "rt-b"})
	v.Set(&vault.Credential{Provider: "google", Account: "c@gmail.com", RefreshToken: "rt-c"})

	auth := &stubAuth{}
	m, _ := newModel(v, "", WithAuthenticator(auth))
	updated, _ := m.Update(providerSelectedMsg{name: "google", provType: vault.TypeOAuth})
	mm := updated.(model)
	// items: a, b, c, +new — cursor starts at 0.
	mm.picker.cursor = 1 // park on b@gmail.com

	updated, _ = mm.Update(revokeAccountMsg{account: "a@gmail.com"})
	mm = updated.(model)
	// New items: b, c, +new — the cursor stayed at index 1, which now
	// points at c@gmail.com (was b@gmail.com before).
	if mm.picker.cursor != 1 {
		t.Errorf("after revoke: cursor = %d, want 1 (preserved)", mm.picker.cursor)
	}

	// Now revoke c via cursor-on-tail edge: cursor at 2 (+new) when
	// items shrink — clamp back to len-1.
	mm.picker.cursor = 2 // currently +new (with 3 items: b, c, +new)
	updated, _ = mm.Update(revokeAccountMsg{account: "b@gmail.com"})
	mm = updated.(model)
	// Items: c, +new — cursor 2 clamps to 1 (+new now at index 1).
	if mm.picker.cursor >= len(mm.picker.items) {
		t.Errorf("after revoke: cursor = %d, len = %d (should be clamped)",
			mm.picker.cursor, len(mm.picker.items))
	}
}

// Chunk-2 review #1: refreshAdminKeyList must flush the proxy
// token+account cache so a recently-revoked admin-key cred isn't
// served stale by the proxy on the next request.
func TestRefreshAdminKeyList_FlushesProxyCache(t *testing.T) {
	// Set up a fake proxy that records cache-clear hits.
	clearHits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/cache/clear" && r.Method == http.MethodPost {
			clearHits++
		}
	}))
	defer srv.Close()

	v := memory.New()
	store := fakeAdminStore(t, "openai", true, "me@example.com")
	fake := providers.NewFake().WithName("openai")

	m := model{
		vault:               v,
		adminProviders:      map[string]providers.Provider{"openai": fake},
		adminStores:         map[string]*providers.AdminKeyStore{"openai": store},
		activeAdminProvider: "openai",
		proxyAddr:           strings.TrimPrefix(srv.URL, "http://"),
	}

	// Trigger a refresh — same path mint/revoke/paste-done all use.
	updated, _ := m.refreshAdminKeyList()
	mm := updated.(model)
	if mm.current != screenAdminKeyList {
		t.Errorf("expected screenAdminKeyList, got %v", mm.current)
	}
	if clearHits != 1 {
		t.Errorf("expected /cache/clear to be hit once, got %d", clearHits)
	}
}
