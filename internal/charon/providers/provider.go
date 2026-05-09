// Package providers defines the interface for upstream API-key
// providers (admin-key types: OpenAI, Anthropic, Google AI; catalog
// type: long-tail Tier 3 providers from issue #15).
//
// OAuth providers (Google for Gmail/Drive/etc.) do NOT implement
// Provider — they live in internal/oauth/ and the TUI dispatches by
// vault.Credential.Type. See workshop/plans/000013-…-plan.md for the
// cross-cutting design rationale.
package providers

import (
	"context"
	"errors"
)

// Provider is the abstraction over admin-key upstreams. Each
// implementation owns its own HTTP client, auth header convention, and
// endpoint routes. The TUI consumes this interface to drive the
// admin-key add-account / mint / revoke flows.
//
// Catalog (Tier 3) providers do not implement Provider — they're
// driven by catalog YAML and have no admin/mint operations. Catalog
// keys are pasted, not minted.
type Provider interface {
	// Name returns the provider's stable id (e.g. "openai",
	// "anthropic"). Used as the vault.Credential.Provider value.
	Name() string

	// Type returns the provider's vault.Credential.Type discriminator.
	// Always vault.TypeAdminKey for the implementations defined in
	// this package's subpackages; catalog providers don't implement
	// Provider so this is effectively a constant per implementation.
	Type() string

	// DiscoverOrg identifies the organization the admin key is scoped
	// to. Called once at admin-key paste time to capture OrgID and
	// OrgName for storage and for same-org-replace detection.
	//
	// Failure modes the TUI must handle: invalid admin key (401),
	// network failure, rate-limited. Implementations return a
	// recognizable error so the TUI can surface "key looks invalid"
	// vs "couldn't reach the provider, try again".
	DiscoverOrg(ctx context.Context, adminKey string) (orgID, orgName string, err error)

	// ListProjects returns the upstream's projects (OpenAI) or
	// workspaces (Anthropic). Naming is provider-local at the UI
	// layer; this interface uses "Project" as the abstract term. The
	// returned slice is unordered.
	ListProjects(ctx context.Context, adminKey string) ([]Project, error)

	// CreateProject creates a new project/workspace upstream. Returns
	// the freshly-created Project so the TUI can use its ID for the
	// subsequent MintKey call without a re-list round-trip.
	CreateProject(ctx context.Context, adminKey, name string) (Project, error)

	// MintKey creates an API key inside the given project/workspace.
	// keyName is the human label set on the upstream key (provider
	// dashboards show this); the X-Charon-Account value is the local
	// vault.Credential.Account, which may differ from keyName.
	//
	// keyMaterial is captured at mint time and never refetchable
	// upstream — the caller MUST persist it before treating MintKey as
	// complete.
	MintKey(ctx context.Context, adminKey, projectID, keyName string) (keyID, keyMaterial string, err error)

	// RevokeKey deletes the upstream API key. Idempotent at the
	// charon level: the caller proceeds with vault deletion even if
	// the upstream returns "already revoked" (implementations should
	// return a recognizable error so the TUI can label the outcome
	// honestly — see internal/oauth.ErrAlreadyRevoked for prior art).
	RevokeKey(ctx context.Context, adminKey, projectID, keyID string) error
}

// Project is a project (OpenAI) or workspace (Anthropic) the user
// minted an API key into. Naming differs per upstream but the shape is
// identical — ID + human name.
type Project struct {
	ID   string
	Name string
}

// ErrAlreadyRevoked signals that an upstream key was already gone when
// charon called RevokeKey. The TUI uses this to distinguish "we did
// the revoke" from "it was gone before we got there" in the exit
// message — same convention as internal/oauth.ErrAlreadyRevoked.
//
// Implementations wrap their upstream's specific 404/410-equivalent
// signal in this error so callers can errors.Is(err, ErrAlreadyRevoked)
// without knowing provider-specific details.
var ErrAlreadyRevoked = errors.New("upstream key already revoked")

// ErrInvalidAdminKey signals that DiscoverOrg or another admin-key-
// authed call rejected the key (401-equivalent). Distinct from a
// transient network/rate-limit failure so the TUI can show "this key
// looks invalid" vs "try again".
var ErrInvalidAdminKey = errors.New("admin key rejected by upstream")
