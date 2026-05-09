package providers

import (
	"errors"
	"strings"
	"testing"
)

// fakeKeychain is an in-memory stand-in for the keychain helpers; the
// store accepts callback functions, so tests inject these directly.
type fakeKeychain struct {
	entries map[string]string // keyed by "service\x00account"
	missing error             // returned by getRaw on missing keys
}

func newFakeKeychain() *fakeKeychain {
	return &fakeKeychain{
		entries: make(map[string]string),
		missing: errors.New("not found"),
	}
}

func (f *fakeKeychain) key(service, account string) string {
	return service + "\x00" + account
}

func (f *fakeKeychain) get(service, account string) (string, error) {
	v, ok := f.entries[f.key(service, account)]
	if !ok {
		return "", f.missing
	}
	return v, nil
}

func (f *fakeKeychain) set(service, account, value string) error {
	f.entries[f.key(service, account)] = value
	return nil
}

func (f *fakeKeychain) del(service, account string) error {
	delete(f.entries, f.key(service, account))
	return nil
}

func newTestStore(provider string) (*AdminKeyStore, *fakeKeychain) {
	fk := newFakeKeychain()
	s := NewAdminKeyStoreWithIO(provider, "charon-test", fk.get, fk.set, fk.del)
	return s, fk
}

func TestAdminKeyStore_GetUnsetReturnsSentinel(t *testing.T) {
	s, _ := newTestStore("openai")
	_, _, err := s.Get()
	if !errors.Is(err, ErrAdminKeyNotSet) {
		t.Errorf("expected ErrAdminKeyNotSet, got %v", err)
	}
}

func TestAdminKeyStore_IsSet(t *testing.T) {
	s, _ := newTestStore("openai")
	if s.IsSet() {
		t.Error("IsSet should be false on fresh store")
	}
	if err := s.Set("sk-admin-secret", AdminMeta{OrgID: "org-aB3", OrgLabel: "me", OrgName: "acme"}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if !s.IsSet() {
		t.Error("IsSet should be true after Set")
	}
}

func TestAdminKeyStore_SetGet_RoundTrip(t *testing.T) {
	s, _ := newTestStore("openai")

	want := AdminMeta{
		OrgID:    "org-aB3cD4",
		OrgLabel: "xianxu@gmail.com",
		OrgName:  "acme-inc",
	}
	if err := s.Set("sk-admin-test", want); err != nil {
		t.Fatalf("Set: %v", err)
	}

	gotKey, gotMeta, err := s.Get()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if gotKey != "sk-admin-test" {
		t.Errorf("admin key: got %q, want %q", gotKey, "sk-admin-test")
	}
	if gotMeta != want {
		t.Errorf("meta: got %+v, want %+v", gotMeta, want)
	}
}

func TestAdminKeyStore_Delete_Idempotent(t *testing.T) {
	s, _ := newTestStore("openai")

	// First call against an empty store: not an error.
	if err := s.Delete(); err != nil {
		t.Errorf("Delete on empty store: %v", err)
	}

	// Set, delete, verify gone.
	_ = s.Set("sk-admin", AdminMeta{OrgID: "org-x"})
	if err := s.Delete(); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, _, err := s.Get(); !errors.Is(err, ErrAdminKeyNotSet) {
		t.Errorf("after Delete, Get should return ErrAdminKeyNotSet, got %v", err)
	}
}

func TestAdminKeyStore_AdminPresentMetaMissing_IsError(t *testing.T) {
	s, fk := newTestStore("openai")
	// Manually plant only the admin key (simulate keychain corruption /
	// half-failed write).
	fk.entries[fk.key("charon-test", "_openai:admin")] = "sk-admin"

	_, _, err := s.Get()
	if err == nil || errors.Is(err, ErrAdminKeyNotSet) {
		t.Errorf("expected explicit corruption error, got %v", err)
	}
	if !strings.Contains(err.Error(), "meta missing") {
		t.Errorf("error should mention meta missing, got %v", err)
	}
}

func TestAdminKeyStore_PerProviderNamespacing(t *testing.T) {
	openai, fk := newTestStore("openai")
	// Reuse the same fake by building a second store.
	anthropic := NewAdminKeyStoreWithIO("anthropic", "charon-test", fk.get, fk.set, fk.del)

	if err := openai.Set("sk-openai", AdminMeta{OrgID: "org-1"}); err != nil {
		t.Fatalf("openai Set: %v", err)
	}
	if err := anthropic.Set("sk-ant", AdminMeta{OrgID: "uuid-2"}); err != nil {
		t.Fatalf("anthropic Set: %v", err)
	}

	gotOpenAI, _, err := openai.Get()
	if err != nil || gotOpenAI != "sk-openai" {
		t.Errorf("openai Get drift: %q / %v", gotOpenAI, err)
	}
	gotAnthropic, _, err := anthropic.Get()
	if err != nil || gotAnthropic != "sk-ant" {
		t.Errorf("anthropic Get drift: %q / %v", gotAnthropic, err)
	}
}

func TestAdminKeyStore_SetValidation(t *testing.T) {
	s, _ := newTestStore("openai")

	if err := s.Set("", AdminMeta{OrgID: "org-x"}); err == nil {
		t.Error("empty admin key should be rejected")
	}
	if err := s.Set("sk-admin", AdminMeta{}); err == nil {
		t.Error("empty OrgID should be rejected")
	}
}

// Set writes meta first, then admin key. A half-failure where the
// admin write fails after meta succeeds must leave the store in a
// recoverable state: Get returns ErrAdminKeyNotSet (not a corruption
// error), so the user can retry Set without manual keychain editing.
func TestAdminKeyStore_Set_HalfFailureLeavesRecoverable(t *testing.T) {
	fk := newFakeKeychain()
	failingSet := func(service, account, value string) error {
		// First write (meta) succeeds; second write (admin) fails.
		if account == "_openai:admin" {
			return errors.New("simulated keychain unavailable")
		}
		return fk.set(service, account, value)
	}
	s := NewAdminKeyStoreWithIO("openai", "charon-test", fk.get, failingSet, fk.del)

	err := s.Set("sk-admin", AdminMeta{OrgID: "org-x", OrgLabel: "me"})
	if err == nil {
		t.Fatal("Set should have failed when admin write errors")
	}

	// After half-failure, Get must report ErrAdminKeyNotSet — clean
	// retry path. Anything else (corruption error, succeeded read) means
	// the caller is stuck.
	_, _, getErr := s.Get()
	if !errors.Is(getErr, ErrAdminKeyNotSet) {
		t.Errorf("after half-failed Set, Get should report ErrAdminKeyNotSet; got %v", getErr)
	}

	// Retry with a working setRaw should succeed (overwrites both).
	working := NewAdminKeyStoreWithIO("openai", "charon-test", fk.get, fk.set, fk.del)
	if err := working.Set("sk-admin", AdminMeta{OrgID: "org-x", OrgLabel: "me"}); err != nil {
		t.Fatalf("Set retry: %v", err)
	}
	if !working.IsSet() {
		t.Error("after retry, IsSet should be true")
	}
}
