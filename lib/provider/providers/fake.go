package providers

import (
	"context"
	"fmt"
	"sync"

	"github.com/xianxu/nous/lib/provider/vault"
)

// Fake is an in-memory Provider for tests. It models a single
// organization with a configurable OrgID/OrgName, a project list, and
// minted keys. Concurrency-safe; callers can use it as a stand-in for
// the OpenAI/Anthropic providers in TUI tests, end-to-end flows, and
// integration tests that don't want to touch the network.
//
// Defaults:
//   - Name="fake-admin", Type=vault.TypeAdminKey ("admin-key")
//   - OrgID="org-fake-0001", OrgName="fake-org"
//   - Empty project list
//   - Any non-empty admin key is accepted; set ValidAdminKey to gate
//     DiscoverOrg behind a specific value.
type Fake struct {
	mu sync.Mutex

	// Identity returned by DiscoverOrg. Fixed for the lifetime of the
	// Fake unless tests set them explicitly.
	OrgID   string
	OrgName string

	// ValidAdminKey, when non-empty, is the only admin key DiscoverOrg
	// accepts. Anything else returns ErrInvalidAdminKey. Empty means
	// "accept any non-empty key" — but `checkAdminKey` still rejects
	// the literally-empty admin key so the empty-key-as-error invariant
	// is preserved across all paths.
	ValidAdminKey string

	// nameOverride optionally replaces the default "fake-admin" Name.
	// Lets tests stand the Fake in for "openai" or "anthropic" without
	// instantiating the real backends.
	nameOverride string

	projects map[string]Project // keyed by Project.ID
	keys     map[string]fakeKey // keyed by KeyID
	nextID   int
}

type fakeKey struct {
	ProjectID string
	KeyID     string
	KeyName   string
	Material  string
	Revoked   bool
}

// NewFake builds a Fake with sensible defaults.
func NewFake() *Fake {
	return &Fake{
		OrgID:    "org-fake-0001",
		OrgName:  "fake-org",
		projects: make(map[string]Project),
		keys:     make(map[string]fakeKey),
	}
}

// WithName sets the Provider's Name. Useful when a test wants the Fake
// to identify as "openai" or "anthropic".
func (f *Fake) WithName(name string) *Fake {
	f.nameOverride = name
	return f
}

// SeedProject pre-populates the Fake with a project so tests can
// exercise ListProjects / MintKey without going through CreateProject
// first.
func (f *Fake) SeedProject(id, name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.projects[id] = Project{ID: id, Name: name}
}

// Snapshot returns the current minted-key state for assertions. Copies
// internal state so callers can mutate the result without racing the
// Fake.
func (f *Fake) Snapshot() (projects []Project, keys []fakeKeyView) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, p := range f.projects {
		projects = append(projects, p)
	}
	for _, k := range f.keys {
		keys = append(keys, fakeKeyView(k))
	}
	return projects, keys
}

// fakeKeyView is the public projection of a minted key for test
// assertions. Mirrors fakeKey but lives outside the unexported
// boundary so tests in other packages can read fields.
type fakeKeyView struct {
	ProjectID string
	KeyID     string
	KeyName   string
	Material  string
	Revoked   bool
}

func (f *Fake) Name() string {
	if f.nameOverride != "" {
		return f.nameOverride
	}
	return "fake-admin"
}

// Type returns vault.TypeAdminKey — the Fake only models the
// admin-key Provider shape. Catalog providers don't implement
// Provider.
func (f *Fake) Type() string { return vault.TypeAdminKey }

func (f *Fake) DiscoverOrg(_ context.Context, adminKey string) (orgID, orgName string, err error) {
	if adminKey == "" {
		return "", "", ErrInvalidAdminKey
	}
	if f.ValidAdminKey != "" && adminKey != f.ValidAdminKey {
		return "", "", ErrInvalidAdminKey
	}
	return f.OrgID, f.OrgName, nil
}

func (f *Fake) ListProjects(_ context.Context, adminKey string) ([]Project, error) {
	if err := f.checkAdminKey(adminKey); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Project, 0, len(f.projects))
	for _, p := range f.projects {
		out = append(out, p)
	}
	return out, nil
}

func (f *Fake) CreateProject(_ context.Context, adminKey, name string) (Project, error) {
	if err := f.checkAdminKey(adminKey); err != nil {
		return Project{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	p := Project{
		ID:   fmt.Sprintf("proj_fake_%04d", f.nextID),
		Name: name,
	}
	f.projects[p.ID] = p
	return p, nil
}

func (f *Fake) MintKey(_ context.Context, adminKey, projectID, keyName string) (keyID, keyMaterial string, err error) {
	if err := f.checkAdminKey(adminKey); err != nil {
		return "", "", err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.projects[projectID]; !ok {
		return "", "", fmt.Errorf("project %q not found", projectID)
	}
	f.nextID++
	keyID = fmt.Sprintf("key_fake_%04d", f.nextID)
	keyMaterial = fmt.Sprintf("sk-fake-%s", keyID)
	f.keys[keyID] = fakeKey{
		ProjectID: projectID,
		KeyID:     keyID,
		KeyName:   keyName,
		Material:  keyMaterial,
	}
	return keyID, keyMaterial, nil
}

func (f *Fake) RevokeKey(_ context.Context, adminKey, projectID, keyID string) error {
	if err := f.checkAdminKey(adminKey); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	k, ok := f.keys[keyID]
	if !ok {
		return ErrAlreadyRevoked
	}
	if k.ProjectID != projectID {
		return fmt.Errorf("key %q does not belong to project %q", keyID, projectID)
	}
	if k.Revoked {
		return ErrAlreadyRevoked
	}
	k.Revoked = true
	f.keys[keyID] = k
	return nil
}

func (f *Fake) checkAdminKey(adminKey string) error {
	if adminKey == "" {
		return ErrInvalidAdminKey
	}
	if f.ValidAdminKey != "" && adminKey != f.ValidAdminKey {
		return ErrInvalidAdminKey
	}
	return nil
}
