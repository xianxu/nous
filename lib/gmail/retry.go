// Retry classification and backoff shared between apiGet and apiBatch.
//
// Gmail enforces three rate-limit surfaces relevant to this library:
//
//   1. Per-user *concurrent requests* (~10–20). Returns 429 "Too many concurrent
//      requests for user". Recovery is sub-second once load eases. We engineer
//      around this with the SearchThreads semaphore (8 batches in flight).
//
//   2. Per-user *quota-units per second* (250 u/s ≈ 50 threads.get/s). Mostly
//      avoided by batching.
//
//   3. Per-user *quota-units per minute*. Returns 403 with reason
//      "rateLimitExceeded" or "userRateLimitExceeded". Recovery requires waiting
//      out the 60-second window. This is what we hit during back-to-back 1k+
//      backfills. Default backoff to ~30s if no Retry-After header is present.
//
// Daily-quota errors (403 dailyLimitExceeded) are NOT retried — the window is
// 24 hours and recovery requires user intervention.
package gmail

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strconv"
	"time"
)

// retryOpts controls retry behavior for both apiGet (whole-call) and apiBatch
// (per-sub-request and whole-batch). Defaults are calibrated against the
// rate-limit surfaces above:
//   - MaxAttempts=5: enough headroom for the per-minute quota to roll over.
//   - BaseDelay=200ms: tight enough for fast 429 recovery.
//   - MaxDelay=60s: covers the per-minute quota window so an exponential
//     ramp can land at ~30–60s before the final attempt.
type retryOpts struct {
	MaxAttempts int
	BaseDelay   time.Duration
	MaxDelay    time.Duration
}

// MaxAttempts=8 gives 7 retries × 30s quotaPerMinuteHint = 210s wait budget,
// which spans 3+ per-minute quota windows. Right-sized for the case where
// multiple parallel batches share the same quota and individual retry loops
// can't observe each other's pacing.
var defaultRetryOpts = retryOpts{
	MaxAttempts: 8,
	BaseDelay:   200 * time.Millisecond,
	MaxDelay:    60 * time.Second,
}

// quotaPerMinuteHint is the default wait for a 403 rateLimitExceeded when no
// Retry-After header is present. Aligns with Gmail's per-minute window.
const quotaPerMinuteHint = 30 * time.Second

// sleepFunc is overridable for tests so retry loops don't actually sleep.
var sleepFunc = time.Sleep

// httpStatusErr captures non-2xx HTTP responses from Gmail in a form
// classifyRetry can dispatch on. Body is bounded to a few KB by callers
// (Gmail error responses are small).
type httpStatusErr struct {
	Status     int
	Reason     string        // parsed from error.errors[0].reason; empty if absent
	Body       []byte        // raw response body for surfacing to callers
	RetryAfter time.Duration // from Retry-After header; 0 if absent
}

func (e *httpStatusErr) Error() string {
	if e.Reason != "" {
		return fmt.Sprintf("HTTP %d (%s): %s", e.Status, e.Reason, string(e.Body))
	}
	return fmt.Sprintf("HTTP %d: %s", e.Status, string(e.Body))
}

// readHTTPError reads a non-2xx response body and constructs an httpStatusErr.
func readHTTPError(resp *http.Response) *httpStatusErr {
	body, _ := io.ReadAll(resp.Body)
	return &httpStatusErr{
		Status:     resp.StatusCode,
		Reason:     parseGmailErrorReason(body),
		Body:       body,
		RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After")),
	}
}

// parseGmailErrorReason extracts the first error.errors[].reason from a Gmail
// JSON error body. Gmail's error envelope:
//
//	{"error":{"code":429,"errors":[{"reason":"rateLimitExceeded",...}],...}}
//
// Returns "" if the body is missing, malformed, or has no errors array.
func parseGmailErrorReason(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	var v struct {
		Error struct {
			Errors []struct {
				Reason string `json:"reason"`
			} `json:"errors"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &v); err != nil {
		return ""
	}
	if len(v.Error.Errors) == 0 {
		return ""
	}
	return v.Error.Errors[0].Reason
}

// parseRetryAfter parses a Retry-After header value. Supports both
// delta-seconds (e.g. "30") and HTTP-date forms.
func parseRetryAfter(s string) time.Duration {
	if s == "" {
		return 0
	}
	if n, err := strconv.Atoi(s); err == nil && n >= 0 {
		return time.Duration(n) * time.Second
	}
	if t, err := http.ParseTime(s); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}

// classifyRetry decides whether a request error warrants a retry, and what
// minimum wait to apply (0 means "use default exponential backoff"). Used by
// both apiGet's whole-call retry and apiBatch's per-sub-request retry.
func classifyRetry(err error) (retry bool, wait time.Duration) {
	if err == nil {
		return false, 0
	}
	var he *httpStatusErr
	if errors.As(err, &he) {
		switch {
		case he.Status >= 500 && he.Status < 600:
			return true, he.RetryAfter
		case he.Status == http.StatusTooManyRequests:
			return true, he.RetryAfter
		case he.Status == http.StatusForbidden:
			switch he.Reason {
			case "dailyLimitExceeded":
				return false, 0 // user-action required; don't burn attempts
			case "rateLimitExceeded", "userRateLimitExceeded", "quotaExceeded":
				if he.RetryAfter > 0 {
					return true, he.RetryAfter
				}
				return true, quotaPerMinuteHint
			}
			return false, 0 // unrelated 403 — caller misconfigured
		case he.Status == http.StatusProxyAuthRequired:
			return false, 0 // Charon scope_missing — won't help
		}
		return false, 0 // other 4xx — caller error
	}
	// Transport/parse errors — usually transient, retry.
	return true, 0
}

// doWithRetry runs fn with retry classification + exponential backoff. Returns
// the last error if attempts are exhausted, or nil on success.
func doWithRetry(opts retryOpts, fn func() error) error {
	var lastErr error
	for attempt := 0; attempt < opts.MaxAttempts; attempt++ {
		err := fn()
		if err == nil {
			return nil
		}
		retry, wait := classifyRetry(err)
		if !retry {
			return err
		}
		lastErr = err
		if attempt+1 == opts.MaxAttempts {
			break
		}
		if wait == 0 {
			wait = backoffDelay(opts, attempt)
		}
		sleepFunc(wait)
	}
	return fmt.Errorf("retries exhausted (%d attempts): %w", opts.MaxAttempts, lastErr)
}

// backoffDelay computes exponential backoff with ±25% jitter.
func backoffDelay(opts retryOpts, attempt int) time.Duration {
	delay := opts.BaseDelay * time.Duration(1<<attempt)
	if delay > opts.MaxDelay {
		delay = opts.MaxDelay
	}
	// Jitter in [-delay/4, delay/4)
	jitter := time.Duration(rand.Int63n(int64(delay)/2)) - delay/4
	return delay + jitter
}
