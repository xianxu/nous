package providers

import (
	"encoding/json"
	"fmt"

	"github.com/xianxu/nous/lib/provider/vault/keychain"
)

// AdminMeta is the metadata stored alongside an admin-key entry.
// Persisted as JSON under `_<provider>:meta` and joined with the admin
// key at `_<provider>:admin` to reconstruct the AdminKeyData payload
// for minted credentials.
//
// MVP storage uses fixed accounts (`_<provider>:admin` and
// `_<provider>:meta`) — exactly one admin key per provider. The
// per-provider per-OrgID keying that the multi-org UI will require is
// a future migration: read the existing `_<provider>:admin` /
// `_<provider>:meta`, write the new `_<provider>:admin:<OrgID>` /
// `_<provider>:meta:<OrgID>`, delete the old ones. AdminKeyData on
// minted credentials already carries OrgID so there's no schema gap on
// that side.
type AdminMeta struct {
	OrgID    string `json:"org_id"`
	OrgLabel string `json:"org_label,omitempty"`
	OrgName  string `json:"org_name,omitempty"`
}

// AdminKeyStore manages the admin-key + meta pair for a single
// admin-key provider (OpenAI, Anthropic, …). The IO operations are
// injected so tests can drive the store with in-memory stubs without
// touching the real keychain.
type AdminKeyStore struct {
	provider string
	service  string
	getRaw   func(service, account string) (string, error)
	setRaw   func(service, account, value string) error
	delRaw   func(service, account string) error
}

// NewAdminKeyStore wires the store to the real keychain. Production
// caller; tests should use NewAdminKeyStoreWithIO.
func NewAdminKeyStore(provider string) *AdminKeyStore {
	return &AdminKeyStore{
		provider: provider,
		service:  keychain.ResolveServiceName(),
		getRaw:   keychain.GetRaw,
		setRaw:   keychain.SetRaw,
		delRaw:   keychain.DeleteRaw,
	}
}

// NewAdminKeyStoreWithIO returns an AdminKeyStore that reads and writes
// via the supplied callbacks. Used in tests to avoid touching the real
// keychain.
func NewAdminKeyStoreWithIO(
	provider, service string,
	getRaw func(service, account string) (string, error),
	setRaw func(service, account, value string) error,
	delRaw func(service, account string) error,
) *AdminKeyStore {
	return &AdminKeyStore{
		provider: provider,
		service:  service,
		getRaw:   getRaw,
		setRaw:   setRaw,
		delRaw:   delRaw,
	}
}

func (s *AdminKeyStore) adminAccount() string { return "_" + s.provider + ":admin" }
func (s *AdminKeyStore) metaAccount() string  { return "_" + s.provider + ":meta" }

// Get returns the configured admin key + meta. Returns ErrAdminKeyNotSet
// if no admin key has been configured for this provider yet — callers
// branch on this to drive the "○ admin key not set" state in the TUI.
//
// A missing meta entry alongside a present admin key is treated as
// corruption and surfaced as an error: the meta blob is required for
// the OrgID-based same-org-replace check.
func (s *AdminKeyStore) Get() (adminKey string, meta AdminMeta, err error) {
	adminKey, err = s.getRaw(s.service, s.adminAccount())
	if err != nil {
		return "", AdminMeta{}, ErrAdminKeyNotSet
	}
	metaJSON, err := s.getRaw(s.service, s.metaAccount())
	if err != nil {
		return "", AdminMeta{}, fmt.Errorf("admin key present but meta missing for %q: %w", s.provider, err)
	}
	if err := json.Unmarshal([]byte(metaJSON), &meta); err != nil {
		return "", AdminMeta{}, fmt.Errorf("corrupt meta for %q: %w", s.provider, err)
	}
	return adminKey, meta, nil
}

// Set stores the admin key + meta. Two keychain writes under the hood;
// not atomic. Order is meta-first then admin-key so a half-failure
// (meta written, admin write fails) leaves the store with no admin
// entry — Get returns ErrAdminKeyNotSet cleanly and retrying Set
// overwrites both blobs. The inverse order would leave admin without
// meta, which Get treats as corruption (explicit error rather than
// ErrAdminKeyNotSet) and the user can't recover from without a manual
// keychain edit.
func (s *AdminKeyStore) Set(adminKey string, meta AdminMeta) error {
	if adminKey == "" {
		return fmt.Errorf("admin key must be non-empty")
	}
	if meta.OrgID == "" {
		return fmt.Errorf("meta.OrgID must be non-empty")
	}
	metaJSON, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("marshal meta: %w", err)
	}
	if err := s.setRaw(s.service, s.metaAccount(), string(metaJSON)); err != nil {
		return fmt.Errorf("write meta: %w", err)
	}
	if err := s.setRaw(s.service, s.adminAccount(), adminKey); err != nil {
		return fmt.Errorf("write admin key: %w", err)
	}
	return nil
}

// Delete removes both the admin key and the meta entries. Idempotent
// — missing entries are not an error. Used when the admin key is
// revoked from the provider screen, including the cascade-delete path
// (the caller must delete minted credentials separately before calling
// Delete here, or after; Delete itself only touches the admin/meta
// pair).
func (s *AdminKeyStore) Delete() error {
	if err := s.delRaw(s.service, s.adminAccount()); err != nil {
		return fmt.Errorf("delete admin key: %w", err)
	}
	if err := s.delRaw(s.service, s.metaAccount()); err != nil {
		return fmt.Errorf("delete meta: %w", err)
	}
	return nil
}

// IsSet reports whether an admin key is configured. Convenience for
// the TUI's red/green state without forcing a full Get + error
// pattern-match.
func (s *AdminKeyStore) IsSet() bool {
	_, err := s.getRaw(s.service, s.adminAccount())
	return err == nil
}

// ErrAdminKeyNotSet is returned by Get when no admin key has been
// configured for the provider. Distinct from a corrupt-meta error so
// callers can branch cleanly.
var ErrAdminKeyNotSet = fmt.Errorf("admin key not set")
