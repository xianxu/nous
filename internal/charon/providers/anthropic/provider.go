// Package anthropic implements providers.Provider for Anthropic using
// the Admin API endpoints under /v1/organizations/{org_id}/. Mirrors
// internal/providers/openai with two material differences:
//
//   - Auth header: x-api-key (not Authorization: Bearer). Anthropic's
//     long-standing convention for both data-plane and admin calls.
//   - org_id is part of the URL path, not implicit in the admin key.
//     DiscoverOrg returns it via GET /v1/organizations/me; subsequent
//     calls cache the lookup so a single ListProjects/MintKey/RevokeKey
//     remains a single HTTP round-trip.
//
// Required Anthropic headers:
//   - x-api-key: <admin_key>
//   - anthropic-version: 2023-06-01
//   - content-type: application/json (on POST)
//
// See workshop/issues/000013 § "Anthropic" for the endpoint shapes.
package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/xianxu/nous/internal/charon/providers"
	"github.com/xianxu/nous/internal/charon/vault"
)

const (
	Name = "anthropic"

	DefaultBaseURL = "https://api.anthropic.com"
	// AnthropicVersion is the long-standing API version string;
	// required on every request. Bumping requires a deliberate
	// migration since response shapes can change.
	AnthropicVersion = "2023-06-01"
)

// Provider implements providers.Provider for Anthropic.
type Provider struct {
	BaseURL string
	HTTP    *http.Client

	// orgIDCache memoizes adminKey → orgID. Anthropic's admin API
	// requires the org id in the URL path, but DiscoverOrg returns it
	// from /v1/organizations/me. Caching avoids re-discovering on every
	// ListProjects/MintKey/RevokeKey call. Keyed by adminKey value;
	// safe because a single admin key uniquely identifies an org by
	// design.
	orgIDCache sync.Map
}

func New() *Provider {
	return &Provider{
		BaseURL: DefaultBaseURL,
		HTTP:    &http.Client{Timeout: 30 * time.Second},
	}
}

func (p *Provider) Name() string { return Name }
func (p *Provider) Type() string { return vault.TypeAdminKey }

// orgResponse is the shape of GET /v1/organizations/me.
type orgResponse struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// workspaceResponse is the shape of a single workspace from the list /
// create endpoints.
type workspaceResponse struct {
	ID         string `json:"id"`
	Type       string `json:"type"`
	Name       string `json:"name"`
	ArchivedAt string `json:"archived_at"`
}

type workspacesListResponse struct {
	Data    []workspaceResponse `json:"data"`
	HasMore bool                `json:"has_more"`
}

// apiKeyMintResponse is the shape returned from POST
// /v1/organizations/{org_id}/workspaces/{ws_id}/api_keys.
//
// Anthropic typically names the secret material `key` (not OpenAI's
// `value`); the partial-hint field used in subsequent reads is
// `partial_key_hint`. Captured at mint time, never refetchable.
type apiKeyMintResponse struct {
	ID              string `json:"id"`
	Type            string `json:"type"`
	Name            string `json:"name"`
	Key             string `json:"key"`               // full sk-ant-… on creation only
	PartialKeyHint  string `json:"partial_key_hint"`  // sk-ant-…xyz, on retrieve
	CreatedAt       string `json:"created_at"`
	WorkspaceID     string `json:"workspace_id"`
}

// errorResponse is Anthropic's standard error JSON wrapper. The
// shape is `{"type":"error","error":{"type":"...","message":"..."}}`,
// distinct from OpenAI's flat-error envelope.
type errorResponse struct {
	Type string `json:"type"`
	Err  struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// DiscoverOrg returns (orgID, orgName) by calling
// GET /v1/organizations/me. Result is cached under adminKey so
// subsequent path-construction calls don't re-fetch.
func (p *Provider) DiscoverOrg(ctx context.Context, adminKey string) (orgID, orgName string, err error) {
	if adminKey == "" {
		return "", "", providers.ErrInvalidAdminKey
	}
	var out orgResponse
	if err := p.do(ctx, adminKey, http.MethodGet, "/v1/organizations/me", nil, &out); err != nil {
		return "", "", err
	}
	if out.ID == "" {
		return "", "", fmt.Errorf("anthropic: /v1/organizations/me returned empty id")
	}
	p.orgIDCache.Store(adminKey, out.ID)
	return out.ID, out.Name, nil
}

// ListProjects returns all non-archived workspaces. Single-page
// (mirrors the OpenAI MVP scope decision).
func (p *Provider) ListProjects(ctx context.Context, adminKey string) ([]providers.Project, error) {
	orgID, err := p.resolveOrgID(ctx, adminKey)
	if err != nil {
		return nil, err
	}
	path := fmt.Sprintf("/v1/organizations/%s/workspaces", orgID)
	var out workspacesListResponse
	if err := p.do(ctx, adminKey, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	projects := make([]providers.Project, 0, len(out.Data))
	for _, ws := range out.Data {
		// Skip archived workspaces — they reject new key mints, so
		// surfacing them in the TUI's "select existing" picker would
		// just lead to downstream errors.
		if ws.ArchivedAt != "" {
			continue
		}
		projects = append(projects, providers.Project{ID: ws.ID, Name: ws.Name})
	}
	return projects, nil
}

func (p *Provider) CreateProject(ctx context.Context, adminKey, name string) (providers.Project, error) {
	orgID, err := p.resolveOrgID(ctx, adminKey)
	if err != nil {
		return providers.Project{}, err
	}
	body := map[string]string{"name": name}
	path := fmt.Sprintf("/v1/organizations/%s/workspaces", orgID)
	var out workspaceResponse
	if err := p.do(ctx, adminKey, http.MethodPost, path, body, &out); err != nil {
		return providers.Project{}, err
	}
	if out.ID == "" {
		return providers.Project{}, fmt.Errorf("anthropic: create workspace returned empty id")
	}
	return providers.Project{ID: out.ID, Name: out.Name}, nil
}

func (p *Provider) MintKey(ctx context.Context, adminKey, projectID, keyName string) (keyID, keyMaterial string, err error) {
	orgID, err := p.resolveOrgID(ctx, adminKey)
	if err != nil {
		return "", "", err
	}
	body := map[string]string{"name": keyName}
	path := fmt.Sprintf("/v1/organizations/%s/workspaces/%s/api_keys", orgID, projectID)
	var out apiKeyMintResponse
	if err := p.do(ctx, adminKey, http.MethodPost, path, body, &out); err != nil {
		return "", "", err
	}
	if out.ID == "" || out.Key == "" {
		return "", "", fmt.Errorf("anthropic: mint returned id=%q key-empty=%v — API may have changed", out.ID, out.Key == "")
	}
	return out.ID, out.Key, nil
}

func (p *Provider) RevokeKey(ctx context.Context, adminKey, projectID, keyID string) error {
	orgID, err := p.resolveOrgID(ctx, adminKey)
	if err != nil {
		return err
	}
	path := fmt.Sprintf("/v1/organizations/%s/workspaces/%s/api_keys/%s", orgID, projectID, keyID)
	return p.do(ctx, adminKey, http.MethodDelete, path, nil, nil)
}

// resolveOrgID returns the cached org id for adminKey, or fetches it
// via DiscoverOrg. Tests can pre-seed the cache to avoid the discovery
// hop in unit-scope tests.
func (p *Provider) resolveOrgID(ctx context.Context, adminKey string) (string, error) {
	if v, ok := p.orgIDCache.Load(adminKey); ok {
		return v.(string), nil
	}
	orgID, _, err := p.DiscoverOrg(ctx, adminKey)
	if err != nil {
		return "", err
	}
	return orgID, nil
}

// InvalidateAdminKey drops the cached org-id entry for the given admin
// key. The TUI MUST call this on admin-key rotation/deletion so that
// (a) stale admin-key bytes aren't kept alive in `sync.Map` longer
// than necessary, and (b) re-Set with a new key can't accidentally
// short-circuit to a stale OrgID if the same key value is ever seen
// again. No-op if the key isn't cached.
//
// Note: M3 still has the cache living on the Provider for simplicity.
// A future cleanup may move OrgID resolution out to the TUI/AdminMeta
// layer (the TUI already stores OrgID in `_<provider>:meta`),
// eliminating the cache entirely. Until then, this is the explicit
// invalidation seam.
func (p *Provider) InvalidateAdminKey(adminKey string) {
	p.orgIDCache.Delete(adminKey)
}

func (p *Provider) do(ctx context.Context, adminKey, method, path string, body any, out any) error {
	url := p.baseURL() + path

	var bodyReader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal body: %w", err)
		}
		bodyReader = bytes.NewReader(buf)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("x-api-key", adminKey)
	req.Header.Set("anthropic-version", AnthropicVersion)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")

	resp, err := p.client().Do(req)
	if err != nil {
		return fmt.Errorf("anthropic %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		if out == nil || len(respBody) == 0 {
			return nil
		}
		if err := json.Unmarshal(respBody, out); err != nil {
			return fmt.Errorf("decode response: %w (body=%q)", err, truncate(respBody, 200))
		}
		return nil
	case resp.StatusCode == http.StatusUnauthorized:
		return providers.ErrInvalidAdminKey
	case resp.StatusCode == http.StatusNotFound && method == http.MethodDelete:
		return providers.ErrAlreadyRevoked
	default:
		return parseUpstreamError(resp.StatusCode, respBody)
	}
}

func (p *Provider) baseURL() string {
	if p.BaseURL == "" {
		return DefaultBaseURL
	}
	return p.BaseURL
}

func (p *Provider) client() *http.Client {
	if p.HTTP == nil {
		return &http.Client{Timeout: 30 * time.Second}
	}
	return p.HTTP
}

// parseUpstreamError unwraps Anthropic's `{"type":"error","error":{...}}`
// envelope. Falls back to a generic "status N" if the body isn't
// recognizable.
func parseUpstreamError(status int, body []byte) error {
	var er errorResponse
	if err := json.Unmarshal(body, &er); err == nil && er.Err.Message != "" {
		return fmt.Errorf("anthropic: %d %s: %s", status, er.Err.Type, er.Err.Message)
	}
	return fmt.Errorf("anthropic: %d (body=%q)", status, truncate(body, 200))
}

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "…"
}

// Compile-time guard.
var _ providers.Provider = (*Provider)(nil)
