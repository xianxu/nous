package tui

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xianxu/nous/internal/charon/providers/catalog"
	"github.com/xianxu/nous/internal/charon/vault/memory"
)

// anthropicLikeRevokeEntry mirrors the catalog package's test fixture
// — local copy so this TUI test doesn't reach across packages for an
// unexported helper.
func anthropicLikeRevokeEntry(listURL, revokeURL, consoleURL string) catalog.Entry {
	return catalog.Entry{
		ID:               "anthropic",
		Name:             "Anthropic",
		HostnamePatterns: []string{"api.anthropic.com"},
		Auth: catalog.Auth{
			Style:        "header",
			Header:       "x-api-key",
			ExtraHeaders: map[string]string{"anthropic-version": "2023-06-01"},
		},
		Revoke: &catalog.Revoke{
			ListEndpoint: &catalog.ListEndpoint{
				URL:        listURL,
				KeyMatch:   "partial_key_hint",
				ResultPath: "data[].id",
			},
			Method: "POST",
			URL:    revokeURL,
			Body:   `{"status":"inactive"}`,
		},
		ConsoleURL: consoleURL,
	}
}

const fakeAnthropicListBody = `{
  "data": [
    {"id": "apikey_001", "partial_key_hint": "sk-ant-…AAAA", "status": "active"}
  ]
}`

const matchingPastedKey = "sk-ant-api03-zzzzzzzzzzzzzzzzzAAAA"

func TestCatalogRevoke_ConfirmHappyPath_DeactivatesAndDeletes(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/organizations/api_keys", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, fakeAnthropicListBody)
	})
	revoked := false
	mux.HandleFunc("/v1/organizations/api_keys/apikey_001", func(w http.ResponseWriter, r *http.Request) {
		revoked = true
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	entry := anthropicLikeRevokeEntry(
		srv.URL+"/v1/organizations/api_keys",
		srv.URL+"/v1/organizations/api_keys/{key_id}",
		"https://console.anthropic.com/settings/keys",
	)
	v := memory.New()
	storeCatalogCred(t, v, "anthropic", "personal", matchingPastedKey)

	m, err := newCatalogRevokeModel(entry, "personal", v)
	if err != nil {
		t.Fatalf("newCatalogRevokeModel: %v", err)
	}

	// y → in-progress with cmd that does the upstream call.
	updated, cmd := m.Update(tea.KeyMsg{Runes: []rune{'y'}, Type: tea.KeyRunes})
	m = updated
	if m.state != catalogRevokeStateInProgress {
		t.Fatalf("after y: state = %d, want inProgress", m.state)
	}
	if cmd == nil {
		t.Fatal("y produced no cmd")
	}
	resultMsg := cmd()
	if _, ok := resultMsg.(catalogRevokeUpstreamResultMsg); !ok {
		t.Fatalf("cmd() = %T, want catalogRevokeUpstreamResultMsg", resultMsg)
	}
	updated, doneCmd := m.Update(resultMsg)
	m = updated
	if doneCmd == nil {
		t.Fatal("expected catalogRevokeDoneMsg cmd")
	}
	done, ok := doneCmd().(catalogRevokeDoneMsg)
	if !ok {
		t.Fatalf("doneCmd() = %T, want catalogRevokeDoneMsg", doneCmd())
	}
	if !strings.Contains(done.statusNote, "Revoked and removed") {
		t.Errorf("statusNote = %q, want 'Revoked and removed' substring", done.statusNote)
	}
	if !revoked {
		t.Error("upstream revoke endpoint was not called")
	}
	// vault entry is gone.
	if _, err := v.Get("anthropic", "personal"); err == nil {
		t.Error("vault still has anthropic/personal after successful revoke")
	}
}

func TestCatalogRevoke_NoEndpoint_LocalDeleteWithConsoleHint(t *testing.T) {
	entry := catalog.Entry{
		ID:         "groq",
		Name:       "Groq",
		Auth:       catalog.Auth{Style: "bearer"},
		ConsoleURL: "https://console.groq.com/keys",
	}
	v := memory.New()
	storeCatalogCred(t, v, "groq", "default", "gsk-key-AAAA")

	m, err := newCatalogRevokeModel(entry, "default", v)
	if err != nil {
		t.Fatalf("newCatalogRevokeModel: %v", err)
	}
	// Confirm view should mention manual cleanup.
	view := m.View()
	if !strings.Contains(view, "manually") {
		t.Errorf("confirm view missing manual-cleanup language:\n%s", view)
	}

	updated, cmd := m.Update(tea.KeyMsg{Runes: []rune{'y'}, Type: tea.KeyRunes})
	m = updated
	resultMsg := cmd()
	r, ok := resultMsg.(catalogRevokeUpstreamResultMsg)
	if !ok {
		t.Fatalf("cmd() = %T, want catalogRevokeUpstreamResultMsg", resultMsg)
	}
	if !errors.Is(r.err, catalog.ErrNoRevokeEndpoint) {
		t.Errorf("upstream err = %v, want ErrNoRevokeEndpoint", r.err)
	}
	updated, doneCmd := m.Update(resultMsg)
	m = updated
	done, ok := doneCmd().(catalogRevokeDoneMsg)
	if !ok {
		t.Fatalf("doneCmd() = %T, want catalogRevokeDoneMsg", doneCmd())
	}
	if !strings.Contains(done.statusNote, "console.groq.com/keys") {
		t.Errorf("statusNote = %q, want console.groq.com URL", done.statusNote)
	}
	if _, err := v.Get("groq", "default"); err == nil {
		t.Error("vault still has groq/default after no-endpoint revoke")
	}
}

func TestCatalogRevoke_UpstreamFailure_DefaultPreservesCredential(t *testing.T) {
	// Default safe action on upstream-fail: any key that isn't `d`
	// (esc/n/enter included) cancels and preserves the credential
	// so the user can retry revoke later. Catalog credentials are
	// only useful as charon's handle on the upstream key; throwing
	// the handle away on transient failure forces a re-paste.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, `{"error":"insufficient_scope"}`)
	}))
	defer srv.Close()
	entry := anthropicLikeRevokeEntry(srv.URL, srv.URL+"/{key_id}", "https://console.anthropic.com/settings/keys")
	v := memory.New()
	storeCatalogCred(t, v, "anthropic", "personal", matchingPastedKey)

	m, _ := newCatalogRevokeModel(entry, "personal", v)
	updated, cmd := m.Update(tea.KeyMsg{Runes: []rune{'y'}, Type: tea.KeyRunes})
	m = updated
	resultMsg := cmd()
	updated, _ = m.Update(resultMsg)
	m = updated
	if m.state != catalogRevokeStateUpstreamFailed {
		t.Fatalf("expected upstream-failed state, got %d", m.state)
	}
	view := m.View()
	if !strings.Contains(view, "401") {
		t.Errorf("error view doesn't mention 401:\n%s", view)
	}
	if !strings.Contains(view, "console.anthropic.com") {
		t.Errorf("error view missing console URL:\n%s", view)
	}
	// View should advertise both options.
	if !strings.Contains(view, "keep credential") || !strings.Contains(view, "[d]") {
		t.Errorf("error view help line missing keep/d affordances:\n%s", view)
	}

	// The three explicit cancel keys all preserve the credential.
	for _, k := range []tea.KeyMsg{
		{Type: tea.KeyEnter},
		{Type: tea.KeyEsc},
		{Runes: []rune{'n'}, Type: tea.KeyRunes},
	} {
		// Each iteration uses a fresh model so we don't drain state.
		mc, _ := newCatalogRevokeModel(entry, "personal", v)
		mc, cmd := mc.Update(tea.KeyMsg{Runes: []rune{'y'}, Type: tea.KeyRunes})
		mc, _ = mc.Update(cmd())
		_, cmd2 := mc.Update(k)
		if cmd2 == nil {
			t.Fatalf("key %v produced no cmd", k)
		}
		if _, ok := cmd2().(catalogRevokeCancelMsg); !ok {
			t.Errorf("key %v: cmd = %T, want catalogRevokeCancelMsg", k, cmd2())
		}
		if _, err := v.Get("anthropic", "personal"); err != nil {
			t.Errorf("vault entry missing after key %v on upstream-fail; expected preserved", k)
		}
	}

	// Stray keys (not in the explicit cancel/force-delete set) are a
	// no-op — they don't silently abandon the flow.
	mc, _ := newCatalogRevokeModel(entry, "personal", v)
	mc, cmdRaw := mc.Update(tea.KeyMsg{Runes: []rune{'y'}, Type: tea.KeyRunes})
	mc, _ = mc.Update(cmdRaw())
	if mc.state != catalogRevokeStateUpstreamFailed {
		t.Fatalf("setup: state = %d, want upstreamFailed", mc.state)
	}
	_, strayCmd := mc.Update(tea.KeyMsg{Runes: []rune{'x'}, Type: tea.KeyRunes})
	if strayCmd != nil {
		if _, ok := strayCmd().(catalogRevokeCancelMsg); ok {
			t.Error("stray key 'x' on upstream-fail emitted cancel — expected no-op")
		}
	}
	if _, err := v.Get("anthropic", "personal"); err != nil {
		t.Errorf("stray key shouldn't have removed credential")
	}
}

func TestCatalogRevoke_UpstreamFailure_DKeyForceLocalDelete(t *testing.T) {
	// `d` is the explicit-force-delete affordance for cases where
	// upstream-revoke can't ever succeed (key already revoked at
	// provider, provider deprecated). Non-default key on purpose.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	entry := anthropicLikeRevokeEntry(srv.URL, srv.URL+"/{key_id}", "https://console.anthropic.com/settings/keys")
	v := memory.New()
	storeCatalogCred(t, v, "anthropic", "personal", matchingPastedKey)

	m, _ := newCatalogRevokeModel(entry, "personal", v)
	m, cmd := m.Update(tea.KeyMsg{Runes: []rune{'y'}, Type: tea.KeyRunes})
	m, _ = m.Update(cmd())
	if m.state != catalogRevokeStateUpstreamFailed {
		t.Fatalf("state = %d, want upstreamFailed", m.state)
	}
	_, dCmd := m.Update(tea.KeyMsg{Runes: []rune{'d'}, Type: tea.KeyRunes})
	if dCmd == nil {
		t.Fatal("d produced no cmd")
	}
	done, ok := dCmd().(catalogRevokeDoneMsg)
	if !ok {
		t.Fatalf("dCmd() = %T, want catalogRevokeDoneMsg", dCmd())
	}
	if !strings.Contains(done.statusNote, "Removed") ||
		!strings.Contains(done.statusNote, "upstream revoke failed") ||
		!strings.Contains(done.statusNote, "console.anthropic.com") {
		t.Errorf("statusNote = %q, want 'Removed … upstream revoke failed … console URL' phrasing", done.statusNote)
	}
	if _, err := v.Get("anthropic", "personal"); err == nil {
		t.Error("vault entry should have been deleted on `d` (force local delete)")
	}
}

func TestCatalogRevoke_DKey_LocalDeleteFailure_ExitsCleanlyNotStuck(t *testing.T) {
	// Important #1 from chunk-2 review: when vault.Delete fails on
	// the `[d]` force-delete path, the user must not be left stuck
	// on the upstream-failed overlay re-pressing `d` against the
	// same failure. Cancel cleanly with a status note for the
	// parent screen so the user sees what went wrong; they can
	// retry from the account list once the underlying issue
	// (keychain lock, ACL denial) is fixed.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	entry := anthropicLikeRevokeEntry(srv.URL, srv.URL+"/{key_id}", "https://console.anthropic.com/settings/keys")

	mem := memory.New()
	storeCatalogCred(t, mem, "anthropic", "personal", matchingPastedKey)
	// failingDeleteVault is shared with admin_revoke_test.go: fails
	// Delete for any account in failOn.
	v := &failingDeleteVault{Store: mem, failOn: map[string]bool{"personal": true}}

	m, _ := newCatalogRevokeModel(entry, "personal", v)
	m, cmd := m.Update(tea.KeyMsg{Runes: []rune{'y'}, Type: tea.KeyRunes})
	m, _ = m.Update(cmd())
	if m.state != catalogRevokeStateUpstreamFailed {
		t.Fatalf("setup: state = %d, want upstreamFailed", m.state)
	}

	_, dCmd := m.Update(tea.KeyMsg{Runes: []rune{'d'}, Type: tea.KeyRunes})
	if dCmd == nil {
		t.Fatal("d on upstream-fail with delete-error produced no cmd — user would be stuck")
	}
	done, ok := dCmd().(catalogRevokeDoneMsg)
	if !ok {
		t.Fatalf("dCmd() = %T, want catalogRevokeDoneMsg (clean exit, not stuck)", dCmd())
	}
	if !strings.Contains(done.statusNote, "Could not remove") {
		t.Errorf("statusNote = %q, want 'Could not remove' phrasing", done.statusNote)
	}
	if !strings.Contains(done.statusNote, "simulated delete failure") {
		t.Errorf("statusNote = %q, want underlying delete error mentioned", done.statusNote)
	}
	if !strings.Contains(done.statusNote, "401") {
		t.Errorf("statusNote = %q, want upstream cause (401) mentioned", done.statusNote)
	}
}

func TestCatalogRevoke_ConfirmCancelEmitsCancelMsg(t *testing.T) {
	v := memory.New()
	storeCatalogCred(t, v, "anthropic", "personal", "sk-ant-key-AAAA")
	entry := catalog.Entry{ID: "anthropic", Name: "Anthropic", Auth: catalog.Auth{Style: "header", Header: "x-api-key"}}
	m, _ := newCatalogRevokeModel(entry, "personal", v)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd == nil {
		t.Fatal("esc on confirm produced no cmd")
	}
	if _, ok := cmd().(catalogRevokeCancelMsg); !ok {
		t.Fatalf("cmd() = %T, want catalogRevokeCancelMsg", cmd())
	}
}
