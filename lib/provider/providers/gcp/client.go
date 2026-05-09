// Package gcp implements thin clients for the Google Cloud APIs charon
// uses to manage Gemini access: Cloud Resource Manager (project list /
// create), Service Usage (API enablement), and Cloud Billing (billing
// info detection). See workshop/issues/000014-google-ai-providers.md
// M3 for the design.
//
// Auth: every call carries `Authorization: Bearer <token>` where the
// token is supplied per-call via TokenSupplier. The supplier's job is
// to return a fresh access token (refreshing the underlying OAuth
// credential when needed) — this package does not know about refresh
// flows; it just calls the supplier and uses what comes back.
package gcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Default base URLs for each Google Cloud API charon talks to. Tests
// override per-API by setting the matching field on Client.
const (
	DefaultResourceManager = "https://cloudresourcemanager.googleapis.com"
	DefaultServiceUsage    = "https://serviceusage.googleapis.com"
	DefaultCloudBilling    = "https://cloudbilling.googleapis.com"
)

// TokenSupplier returns a fresh OAuth access token for the account
// charon is acting on behalf of. Implementations are expected to
// transparently refresh the token via the user's stored refresh
// token; this client does not retry on 401.
type TokenSupplier func(ctx context.Context) (string, error)

// Client is the shared HTTP transport for all three GCP APIs. The
// per-API base URLs are split so tests can mount different httptest
// servers per endpoint family if needed; in production they all
// resolve to googleapis.com subdomains under the same TLS root.
type Client struct {
	HTTP            *http.Client
	Tokens          TokenSupplier
	ResourceManager string
	ServiceUsage    string
	CloudBilling    string
	APIKeys         string
	// PollInterval governs how often long-running operations are
	// re-queried while waiting for completion. Zero means default
	// (2s) — tuned for Resource Manager / Service Usage's typical
	// 5-30s latencies; tests override to milliseconds.
	PollInterval time.Duration
}

// New returns a Client wired to the production GCP endpoints.
func New(tokens TokenSupplier) *Client {
	return &Client{
		HTTP:            &http.Client{Timeout: 30 * time.Second},
		Tokens:          tokens,
		ResourceManager: DefaultResourceManager,
		ServiceUsage:    DefaultServiceUsage,
		CloudBilling:    DefaultCloudBilling,
		APIKeys:         DefaultAPIKeys,
	}
}

// apiError is returned by every GCP HTTP call when the response code
// is non-2xx. Body is the raw response body so callers can match on
// specific error patterns (e.g. BILLING_DISABLED, API not enabled)
// without imposing a single error-code taxonomy on this package.
type apiError struct {
	Status int
	Body   string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("gcp api: status %d: %s", e.Status, e.Body)
}

// IsHTTPStatus reports whether err (or anything in its wrap chain)
// is a GCP API error with the given status code. Handy for callers
// that branch on 403 (permission / API not enabled) vs 404 (not
// found) vs 409 (already exists).
func IsHTTPStatus(err error, status int) bool {
	var ae *apiError
	return errors.As(err, &ae) && ae.Status == status
}

// HTTPBody returns the raw response body string for a GCP API error
// anywhere in the wrap chain, or "" if no GCP API error is present.
// Callers use this to grep for Google's documented error reasons
// (e.g. "BILLING_DISABLED").
func HTTPBody(err error) string {
	var ae *apiError
	if errors.As(err, &ae) {
		return ae.Body
	}
	return ""
}

// do executes an authenticated request against url, JSON-encoding body
// (nil for GET) and decoding the JSON response into out (nil to
// discard). Returns *apiError for non-2xx responses; other errors
// indicate transport / encoding failures.
func (c *Client) do(ctx context.Context, method, url string, body, out any) error {
	token, err := c.Tokens(ctx)
	if err != nil {
		return fmt.Errorf("token supplier: %w", err)
	}

	var reqBody io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode request: %w", err)
		}
		reqBody = bytes.NewReader(buf)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, reqBody)
	if err != nil {
		return fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &apiError{Status: resp.StatusCode, Body: string(respBody)}
	}

	if out != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}
