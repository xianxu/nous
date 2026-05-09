// Package memory provides an in-memory vault.Store for testing.
package memory

import (
	"fmt"
	"sync"

	"github.com/xianxu/nous/internal/charon/vault"
)

// Store is an in-memory credential store for testing.
type Store struct {
	mu    sync.RWMutex
	creds map[string]*vault.Credential // keyed by "provider:account"
}

func New() *Store {
	return &Store{creds: make(map[string]*vault.Credential)}
}

func key(provider, account string) string { return provider + ":" + account }

func (s *Store) Get(provider, account string) (*vault.Credential, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.creds[key(provider, account)]
	if !ok {
		return nil, fmt.Errorf("not found: %s/%s", provider, account)
	}
	cp := *c
	return &cp, nil
}

func (s *Store) Set(cred *vault.Credential) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *cred
	s.creds[key(cred.Provider, cred.Account)] = &cp
	return nil
}

func (s *Store) Delete(provider, account string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.creds, key(provider, account))
	return nil
}

func (s *Store) List() ([]*vault.Credential, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []*vault.Credential
	for _, c := range s.creds {
		cp := *c
		cp.AccessToken = ""
		result = append(result, &cp)
	}
	return result, nil
}
