package gcp

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// DefaultAPIKeys is the production Google API Keys (v2) endpoint.
// Tests override Client.APIKeys to point at httptest.
const DefaultAPIKeys = "https://apikeys.googleapis.com"

// APIKey models the subset of the Google API Keys v2 Key resource
// charon cares about. The full resource has restrictions, annotations,
// timestamps, etc.; we only persist what's needed to attach the key
// to outbound requests and to revoke it later.
type APIKey struct {
	// Name is the full resource name —
	// "projects/{project}/locations/global/keys/{uid}". Used by
	// DELETE for revoke.
	Name string `json:"name"`
	// UID is the short opaque key id (the part after .../keys/).
	// Stable identifier even if the displayName changes.
	UID string `json:"uid"`
	// DisplayName is the human-readable label set at create time.
	DisplayName string `json:"displayName,omitempty"`
	// KeyString is the actual API key value (the AIzaSy... string
	// agents will eventually paste into URLs). Returned in the
	// Create operation response on first creation; subsequent reads
	// must fetch via GetKeyString.
	KeyString string `json:"keyString,omitempty"`
	// CreateTime is when Google created the key. Best-effort.
	CreateTime string `json:"createTime,omitempty"`
}

// APIKeyRestrictions is the subset of restrictions charon sets at
// mint time. apiTargets pins the key to a specific service so that
// even if it leaks, it can only call that service. For Gemini AI
// Studio: service = "generativelanguage.googleapis.com".
type APIKeyRestrictions struct {
	APITargets []APITarget `json:"apiTargets,omitempty"`
}

// APITarget restricts a key to a single Google API.
type APITarget struct {
	Service string `json:"service"`
}

// createAPIKeyRequest models the body for v2/keys.create.
type createAPIKeyRequest struct {
	DisplayName  string              `json:"displayName"`
	Restrictions *APIKeyRestrictions `json:"restrictions,omitempty"`
}

// CreateAPIKey mints a new API key under the project, restricted to
// the named services (defense-in-depth: a leaked key can only call
// those upstreams). Returns the long-running operation; once Done,
// the key resource is in op.Response.
//
// displayName is freeform but should mention charon so the user
// can identify it in Cloud Console (e.g. "charon-aistudio").
// restrictedTo is the list of services to allow (e.g.
// ["generativelanguage.googleapis.com"]).
func (c *Client) CreateAPIKey(ctx context.Context, projectID, displayName string, restrictedTo []string) (*Operation, error) {
	req := &createAPIKeyRequest{
		DisplayName: displayName,
	}
	if len(restrictedTo) > 0 {
		req.Restrictions = &APIKeyRestrictions{}
		for _, svc := range restrictedTo {
			req.Restrictions.APITargets = append(req.Restrictions.APITargets, APITarget{Service: svc})
		}
	}
	url := fmt.Sprintf("%s/v2/projects/%s/locations/global/keys", c.apiKeysBase(), projectID)
	var op Operation
	if err := c.do(ctx, "POST", url, req, &op); err != nil {
		return nil, fmt.Errorf("create api key: %w", err)
	}
	return &op, nil
}

// WaitAPIKeyOperation polls an apikeys long-running operation until
// done. apikeys ops live under apikeys.googleapis.com (not the
// resourcemanager host), so this is a separate poll loop.
//
// Returns the operation on success so callers can inspect Response
// (which carries the new APIKey including the secret keyString —
// only available on Create response).
func (c *Client) WaitAPIKeyOperation(ctx context.Context, opName string) (*Operation, error) {
	interval := c.PollInterval
	if interval <= 0 {
		interval = 2 * time.Second
	}
	url := fmt.Sprintf("%s/v2/%s", c.apiKeysBase(), opName)
	for {
		var op Operation
		if err := c.do(ctx, "GET", url, nil, &op); err != nil {
			return nil, fmt.Errorf("poll api keys op: %w", err)
		}
		if op.Done {
			if op.Error != nil {
				return &op, op.Error
			}
			return &op, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(interval):
		}
	}
}

// ExtractAPIKey unmarshals an APIKey from an operation's Response
// field (the Operation generic shape carries response as
// map[string]any). Returns the key including KeyString, which is
// only populated on Create-operation responses.
func ExtractAPIKey(op *Operation) (*APIKey, error) {
	if op == nil || op.Response == nil {
		return nil, fmt.Errorf("operation response is empty")
	}
	// Round-trip via JSON to avoid hand-rolling field-by-field
	// extraction from map[string]any.
	raw, err := json.Marshal(op.Response)
	if err != nil {
		return nil, fmt.Errorf("re-marshal response: %w", err)
	}
	var key APIKey
	if err := json.Unmarshal(raw, &key); err != nil {
		return nil, fmt.Errorf("decode key: %w", err)
	}
	return &key, nil
}

// DeleteAPIKey revokes a key by full resource name (the form
// "projects/{p}/locations/global/keys/{uid}"). Returns an Operation
// that the caller may poll; for charon's revoke flow, fire-and-
// forget is acceptable since the local credential gets removed
// regardless of upstream success.
func (c *Client) DeleteAPIKey(ctx context.Context, name string) (*Operation, error) {
	url := fmt.Sprintf("%s/v2/%s", c.apiKeysBase(), name)
	var op Operation
	if err := c.do(ctx, "DELETE", url, nil, &op); err != nil {
		return nil, fmt.Errorf("delete api key: %w", err)
	}
	return &op, nil
}

// apiKeysBase returns the base URL for the API Keys API, defaulting
// to the production endpoint when Client.APIKeys is unset.
func (c *Client) apiKeysBase() string {
	if c.APIKeys != "" {
		return c.APIKeys
	}
	return DefaultAPIKeys
}
