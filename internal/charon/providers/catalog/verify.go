package catalog

import (
	"context"
	"fmt"
	"io"
	"net/http"
)

// VerifyResult communicates what the verify call learned about the
// pasted key. Three outcomes:
//
//   VerifyOK             — 2xx response (or no verify_url declared)
//   VerifyRejected       — 401/403 — provider explicitly rejected the key
//   VerifyEndpointError  — 5xx, network, or unexpected status — inconclusive,
//                          caller should warn the user but not block
//                          (transient outages shouldn't trap users)
type VerifyResult int

const (
	VerifyOK VerifyResult = iota
	VerifyRejected
	VerifyEndpointError
)

// Verify probes the entry's verify_url with the pasted key applied
// per Auth. Entries without a verify_url skip verification entirely
// (returns VerifyOK with nil error) — the catalog YAML opts each
// provider in by declaring the URL.
//
// Two callers:
//   - the TUI paste flow runs Verify after Enter on the key step;
//     VerifyRejected sends the user back to retype the key,
//     VerifyEndpointError stores the key with a degraded status note.
//   - any future CLI subcommand that wants to revalidate a stored
//     credential without going through the TUI.
func (e Entry) Verify(ctx context.Context, pastedKey string) (VerifyResult, error) {
	if e.VerifyURL == "" {
		return VerifyOK, nil
	}
	req, err := http.NewRequestWithContext(ctx, "GET", e.VerifyURL, nil)
	if err != nil {
		return VerifyEndpointError, fmt.Errorf("build verify request: %w", err)
	}
	applyAuth(req, e.Auth, pastedKey)

	resp, err := httpClient.Do(req)
	if err != nil {
		return VerifyEndpointError, fmt.Errorf("verify request: %w", err)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return VerifyOK, nil
	case resp.StatusCode == 401 || resp.StatusCode == 403:
		body, _ := io.ReadAll(resp.Body)
		return VerifyRejected, fmt.Errorf("verify endpoint %d: %s", resp.StatusCode, snippet(body))
	default:
		body, _ := io.ReadAll(resp.Body)
		return VerifyEndpointError, fmt.Errorf("verify endpoint %d: %s", resp.StatusCode, snippet(body))
	}
}
