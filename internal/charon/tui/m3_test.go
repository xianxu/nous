package tui

import (
	"errors"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xianxu/nous/internal/charon/vault"
	"github.com/xianxu/nous/internal/charon/vault/memory"
)

// stubAuth records what was requested and returns a canned credential.
type stubAuth struct {
	calls         int
	gotAccount    string
	gotScopes     []string
	gotExisting   []string
	gotForceFresh bool
	revokeCalls   int
	gotRevokeTok  string
	revokeErr     error
	returnCred    *vault.Credential
	returnErr     error
}

func (s *stubAuth) Auth(account string, scopes, existingScopes []string, forceFresh bool) (*vault.Credential, error) {
	s.calls++
	s.gotAccount = account
	s.gotScopes = append([]string(nil), scopes...)
	s.gotExisting = append([]string(nil), existingScopes...)
	s.gotForceFresh = forceFresh
	return s.returnCred, s.returnErr
}

func (s *stubAuth) Revoke(refreshToken string) error {
	s.revokeCalls++
	s.gotRevokeTok = refreshToken
	return s.revokeErr
}

// helper: load rows and build a scopes model with the given auth.
func newScopesForTest(t *testing.T, v vault.Store, account string, auth Authenticator) scopesModel {
	t.Helper()
	rows, err := loadScopeRows(v, account, nil)
	if err != nil {
		t.Fatalf("loadScopeRows: %v", err)
	}
	return newScopesModel(account, rows, auth)
}

// moveToFirstListRow drops focus to the list. Returns the row index in m.rows
// that the cursor now points at.
func moveToFirstListRow(t *testing.T, m scopesModel) (scopesModel, int) {
	t.Helper()
	m, _ = m.Update(keyPress("down"))
	if m.focus != focusList {
		t.Fatalf("focus not list after down")
	}
	if len(m.filtered) == 0 {
		t.Fatalf("no filtered rows")
	}
	return m, m.filtered[m.cursor]
}

// moveToFirstTogglableRow drops focus to the list and advances the cursor
// past any required (non-togglable) rows. Returns the row index that the
// cursor now points at.
func moveToFirstTogglableRow(t *testing.T, m scopesModel) (scopesModel, int) {
	t.Helper()
	m, _ = m.Update(keyPress("down"))
	if m.focus != focusList {
		t.Fatalf("focus not list after down")
	}
	for m.cursor < len(m.filtered) && m.rows[m.filtered[m.cursor]].required {
		m, _ = m.Update(keyPress("down"))
	}
	if m.cursor >= len(m.filtered) {
		t.Fatalf("no togglable rows in list")
	}
	return m, m.filtered[m.cursor]
}

func TestSpaceTogglesTarget(t *testing.T) {
	v := vaultWithBase("a@gmail.com")
	m := newScopesForTest(t, v, "a@gmail.com", nil)
	m, idx := moveToFirstTogglableRow(t, m)

	prior := m.rows[idx].target
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeySpace})
	if m.rows[idx].target == prior {
		t.Errorf("space did not toggle row %d", idx)
	}
	if !m.pendingChanges() {
		t.Errorf("pendingChanges should be true after toggle")
	}
	// Toggle back: should clear pending.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeySpace})
	if m.rows[idx].target != prior {
		t.Errorf("second space did not toggle back")
	}
	if m.pendingChanges() {
		t.Errorf("pendingChanges should be false after toggle-back")
	}
}

func TestEnterNoChangeQuits(t *testing.T) {
	v := vaultWithBase("a@gmail.com")
	m := newScopesForTest(t, v, "a@gmail.com", nil)
	m, _ = moveToFirstListRow(t, m)

	if m.pendingChanges() {
		t.Fatalf("setup: vault with required scopes should produce no pending changes")
	}
	_, cmd := m.Update(keyPress("enter"))
	if cmd == nil {
		t.Fatal("enter with no pending changes: expected quit cmd")
	}
	if _, ok := cmd().(scopesQuitMsg); !ok {
		t.Fatalf("expected scopesQuitMsg, got %T", cmd())
	}
}

func TestEnterAdditiveCallsAuth(t *testing.T) {
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
	// Find a known catalog row and toggle it on.
	var targetIdx int
	for i, r := range m.rows {
		if r.short == "gmail.readonly" {
			targetIdx = i
			break
		}
	}
	for visIdx, rowIdx := range m.filtered {
		if rowIdx == targetIdx {
			m.cursor = visIdx
			break
		}
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeySpace})
	if !m.pendingChanges() {
		t.Fatalf("expected pending changes after toggle on gmail.readonly")
	}

	// Enter dispatches apply; state goes to applying, cmd runs auth.
	m, cmd := m.Update(keyPress("enter"))
	if m.state != stateApplying {
		t.Errorf("after enter: state = %v, want stateApplying", m.state)
	}
	if cmd == nil {
		t.Fatal("expected applyCmd")
	}
	msg := cmd()
	result, ok := msg.(applyResultMsg)
	if !ok {
		t.Fatalf("apply cmd returned %T, want applyResultMsg", msg)
	}
	if auth.calls != 1 {
		t.Errorf("auth.Auth called %d times, want 1", auth.calls)
	}
	if auth.gotAccount != "a@gmail.com" {
		t.Errorf("auth got account %q, want a@gmail.com", auth.gotAccount)
	}
	if !contains(auth.gotScopes, "https://www.googleapis.com/auth/gmail.readonly") {
		t.Errorf("auth scopes missing gmail.readonly: %v", auth.gotScopes)
	}

	// Forward result to the model: should clear pending and update realized.
	m = m.handleApplyResult(result)
	if m.state != stateNormal {
		t.Errorf("after apply success: state = %v, want stateNormal", m.state)
	}
	for _, r := range m.rows {
		if r.short == "gmail.readonly" {
			if !r.realized || !r.target {
				t.Errorf("gmail.readonly should be realized+target after apply: %+v", r)
			}
		}
	}
	if m.pendingChanges() {
		t.Error("apply success should clear pending changes")
	}
}

func TestEnterReductionRoutesToConfirmModal(t *testing.T) {
	v := vaultWithBase("a@gmail.com", "https://www.googleapis.com/auth/gmail.readonly")
	auth := &stubAuth{}
	m := newScopesForTest(t, v, "a@gmail.com", auth)
	m, _ = moveToFirstListRow(t, m)

	// Find the granted row and toggle it off.
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

	// Enter on a reductive change opens the confirmation modal — does not
	// dispatch Auth yet.
	m, cmd := m.Update(keyPress("enter"))
	if m.state != stateReduceConfirm {
		t.Errorf("reduction: state = %v, want stateReduceConfirm", m.state)
	}
	if cmd != nil {
		t.Errorf("expected no cmd at modal open, got %T", cmd())
	}
	if auth.calls != 0 {
		t.Errorf("auth should not be called yet, got %d calls", auth.calls)
	}
}

func TestApplyErrorDismissedByAnyKey(t *testing.T) {
	v := memory.New()
	m := newScopesForTest(t, v, "a@gmail.com", nil)
	m.state = stateApplyError
	m.applyErr = errors.New("boom")

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	if m.state != stateNormal {
		t.Errorf("after key in apply error: state = %v, want stateNormal", m.state)
	}
	if m.applyErr != nil {
		t.Errorf("applyErr should be cleared after dismissal")
	}
}

func TestAddCustomAppendsRow(t *testing.T) {
	v := memory.New()
	m := newScopesForTest(t, v, "a@gmail.com", nil)
	m, _ = moveToFirstListRow(t, m)
	priorCount := len(m.rows)

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	if m.state != stateAddCustom {
		t.Fatalf("after 'a': state = %v, want stateAddCustom", m.state)
	}

	// Type a URL into the custom input.
	url := "https://www.googleapis.com/auth/some.new"
	for _, r := range url {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m, _ = m.Update(keyPress("enter"))
	if m.state != stateNormal {
		t.Errorf("after enter: state = %v, want stateNormal", m.state)
	}
	if len(m.rows) != priorCount+1 {
		t.Errorf("rows = %d, want %d", len(m.rows), priorCount+1)
	}
	last := m.rows[len(m.rows)-1]
	if last.full != url || !last.target || !last.custom {
		t.Errorf("appended row wrong: %+v", last)
	}
}

func TestAddCustomEscCancels(t *testing.T) {
	v := memory.New()
	m := newScopesForTest(t, v, "a@gmail.com", nil)
	m, _ = moveToFirstListRow(t, m)
	priorCount := len(m.rows)

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	if m.state != stateAddCustom {
		t.Fatalf("setup: not in stateAddCustom")
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.state != stateNormal {
		t.Errorf("after esc: state = %v, want stateNormal", m.state)
	}
	if len(m.rows) != priorCount {
		t.Errorf("rows mutated on cancel: %d → %d", priorCount, len(m.rows))
	}
}

func TestAddCustomDuplicateRejected(t *testing.T) {
	v := memory.New()
	v.Set(&vault.Credential{
		Provider: "google", Account: "a@gmail.com",
		Scopes: []string{"https://www.googleapis.com/auth/gmail.readonly"},
	})
	m := newScopesForTest(t, v, "a@gmail.com", nil)
	m, _ = moveToFirstListRow(t, m)

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	dup := "https://www.googleapis.com/auth/gmail.readonly"
	for _, r := range dup {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m, _ = m.Update(keyPress("enter"))
	if m.state != stateApplyError {
		t.Errorf("duplicate add: state = %v, want stateApplyError", m.state)
	}
}

func TestQuitConfirmAppearsForPendingChanges(t *testing.T) {
	v := memory.New()
	m := newScopesForTest(t, v, "a@gmail.com", nil)
	m, _ = moveToFirstListRow(t, m)

	// Toggle to create a pending change.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeySpace})
	if !m.pendingChanges() {
		t.Fatalf("setup: no pending changes")
	}

	// q from list with pending → stateQuitConfirm, no quit cmd.
	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if m.state != stateQuitConfirm {
		t.Errorf("q with pending: state = %v, want stateQuitConfirm", m.state)
	}
	if cmd != nil {
		t.Errorf("expected no cmd; got one — quit prompt should not exit yet")
	}
}

func TestQuitConfirmDiscardSignalsQuit(t *testing.T) {
	v := memory.New()
	m := newScopesForTest(t, v, "a@gmail.com", nil)
	m.state = stateQuitConfirm

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	if cmd == nil {
		t.Fatal("d in confirm: expected quit cmd")
	}
	if _, ok := cmd().(scopesQuitMsg); !ok {
		t.Fatalf("expected scopesQuitMsg, got %T", cmd())
	}
}

func TestQuitConfirmCancelReturnsNormal(t *testing.T) {
	v := memory.New()
	m := newScopesForTest(t, v, "a@gmail.com", nil)
	m.state = stateQuitConfirm

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	if m.state != stateNormal {
		t.Errorf("c in confirm: state = %v, want stateNormal", m.state)
	}
}

func TestQuitConfirmApplyDispatchesAuth(t *testing.T) {
	v := memory.New()
	auth := &stubAuth{
		returnCred: &vault.Credential{Provider: "google", Account: "a@gmail.com", Scopes: []string{}},
	}
	m := newScopesForTest(t, v, "a@gmail.com", auth)
	m, _ = moveToFirstListRow(t, m)

	// Toggle a row on, then enter quit confirm.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeySpace})
	m.state = stateQuitConfirm

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	if cmd == nil {
		t.Fatal("a in confirm with additive change: expected applyCmd")
	}
	msg := cmd()
	if _, ok := msg.(applyResultMsg); !ok {
		t.Fatalf("expected applyResultMsg, got %T", msg)
	}
	if auth.calls != 1 {
		t.Errorf("auth.Auth calls = %d, want 1", auth.calls)
	}
}

func TestModelForwardsApplyResultAndPersists(t *testing.T) {
	v := memory.New()
	v.Set(&vault.Credential{Provider: "google", Account: "a@gmail.com"})
	auth := &stubAuth{}
	m, err := newModel(v, "a@gmail.com", WithAuthenticator(auth))
	if err != nil {
		t.Fatalf("newModel: %v", err)
	}
	newCred := &vault.Credential{
		Provider: "google", Account: "a@gmail.com",
		Scopes: []string{"https://www.googleapis.com/auth/gmail.readonly"},
	}
	updated, _ := m.Update(applyResultMsg{cred: newCred})
	mm := updated.(model)
	stored, err := v.Get("google", "a@gmail.com")
	if err != nil {
		t.Fatalf("vault.Get after apply: %v", err)
	}
	if !contains(stored.Scopes, "https://www.googleapis.com/auth/gmail.readonly") {
		t.Errorf("vault not updated after apply, scopes = %v", stored.Scopes)
	}
	if mm.scopes.state != stateNormal {
		t.Errorf("scope state after apply forward = %v, want stateNormal", mm.scopes.state)
	}
}

func TestRequiredRowsForceTargetTrue(t *testing.T) {
	rows, err := loadScopeRows(memory.New(), "a@gmail.com", nil)
	if err != nil {
		t.Fatalf("loadScopeRows: %v", err)
	}
	for _, r := range rows {
		if r.required && !r.target {
			t.Errorf("required row %q should have target=true on fresh load", r.short)
		}
	}
}

func TestSpaceOnRequiredRowIsNoOp(t *testing.T) {
	v := vaultWithBase("a@gmail.com")
	m := newScopesForTest(t, v, "a@gmail.com", nil)
	m, _ = m.Update(keyPress("down"))
	// Cursor should be on the first required row (openid).
	idx := m.filtered[m.cursor]
	if !m.rows[idx].required {
		t.Fatalf("setup: first row not required, got %+v", m.rows[idx])
	}

	priorTarget := m.rows[idx].target
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeySpace})
	if m.rows[idx].target != priorTarget {
		t.Errorf("space on required row toggled target")
	}
	if m.applyStatus == "" {
		t.Errorf("expected status message when toggling required row")
	}
	if m.pendingChanges() {
		t.Errorf("required row no-op should not introduce pending changes")
	}
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
