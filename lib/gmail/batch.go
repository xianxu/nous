// Gmail HTTP batch API support.
//
// Gmail accepts up to 100 sub-requests per batch via multipart/mixed POST
// to https://gmail.googleapis.com/batch/gmail/v1. Each sub-request is an
// embedded HTTP request; each sub-response is an embedded HTTP response.
// Auth on the outer request propagates to all sub-requests, so Charon's
// X-Charon-Account / X-Charon-Scope work transparently.
package gmail

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strconv"
	"strings"
	"time"
)

// gmailBatchURL is the batch endpoint. Exposed as var for tests to override.
var gmailBatchURL = "https://gmail.googleapis.com/batch/gmail/v1"

// MaxBatchSize is Gmail's documented hard cap on sub-requests per batch.
const MaxBatchSize = 100

// batchRequest is one sub-request inside a batch. Path is relative to
// /gmail/v1/users/me — apiBatch prepends that prefix when writing the
// sub-request line, mirroring apiGet's convention.
type batchRequest struct {
	Method string // "GET", "POST", etc.
	Path   string // e.g. "/threads/abc123?format=metadata"
}

// batchResponse is one sub-response from a batch. Status is the HTTP status
// code of the embedded sub-response; Body is the raw response body.
type batchResponse struct {
	Status int
	Body   []byte
}

// apiBatch issues a single batch request to Gmail with the given sub-requests.
// Returns a slice of sub-responses in the same order as the input. The outer
// request itself failing (transport error, 4xx/5xx on the batch endpoint)
// returns an error; per-sub-request HTTP errors are surfaced via batchResponse.Status.
//
// Caller is responsible for chunking — len(reqs) must not exceed MaxBatchSize.
func apiBatch(account, scope string, reqs []batchRequest) ([]batchResponse, error) {
	if len(reqs) == 0 {
		return nil, nil
	}
	if len(reqs) > MaxBatchSize {
		return nil, fmt.Errorf("batch size %d exceeds Gmail max %d", len(reqs), MaxBatchSize)
	}

	body, contentType, err := encodeBatch(reqs)
	if err != nil {
		return nil, fmt.Errorf("encode batch: %w", err)
	}

	httpReq, err := http.NewRequest("POST", gmailBatchURL, body)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", contentType)
	httpReq.Header.Set("X-Charon-Account", account)
	httpReq.Header.Set("X-Charon-Scope", scope)

	resp, err := getClient().Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("batch HTTP failed: %w", err)
	}
	defer func() {
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}()

	if resp.StatusCode == http.StatusProxyAuthRequired {
		return nil, parseCharonScopeError(resp.Body)
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("batch endpoint returned %d: %s", resp.StatusCode, string(b))
	}

	return decodeBatchResponse(resp.Header.Get("Content-Type"), resp.Body, len(reqs))
}

// encodeBatch builds a multipart/mixed body for a batch request.
// Returns the body reader, the Content-Type header value (with boundary), and any error.
func encodeBatch(reqs []batchRequest) (io.Reader, string, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)

	for i, req := range reqs {
		partHeader := textproto.MIMEHeader{}
		partHeader.Set("Content-Type", "application/http")
		partHeader.Set("Content-ID", fmt.Sprintf("<%d>", i))
		partHeader.Set("Content-Transfer-Encoding", "binary")

		part, err := mw.CreatePart(partHeader)
		if err != nil {
			return nil, "", err
		}
		// Sub-request: request line + blank line (no headers, no body for GET).
		// Path must be absolute from API root.
		if _, err := fmt.Fprintf(part, "%s /gmail/v1/users/me%s HTTP/1.1\r\n\r\n", req.Method, req.Path); err != nil {
			return nil, "", err
		}
	}
	if err := mw.Close(); err != nil {
		return nil, "", err
	}
	return &buf, "multipart/mixed; boundary=" + mw.Boundary(), nil
}

// decodeBatchResponse parses a multipart/mixed batch response body. Each part
// contains an embedded HTTP response. The Content-ID header on each response
// part is "<response-N>" where N matches the original request's Content-ID;
// we use that to restore order, since Google does not guarantee response order
// matches request order.
func decodeBatchResponse(contentType string, body io.Reader, expectedCount int) ([]batchResponse, error) {
	_, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return nil, fmt.Errorf("parse outer content-type: %w", err)
	}
	boundary := params["boundary"]
	if boundary == "" {
		return nil, fmt.Errorf("missing boundary in batch response content-type %q", contentType)
	}

	mr := multipart.NewReader(body, boundary)
	results := make([]batchResponse, expectedCount)
	seen := make([]bool, expectedCount)
	count := 0

	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read part %d: %w", count, err)
		}

		contentID := part.Header.Get("Content-ID")
		idx, idxErr := parseResponseContentID(contentID)
		// Read the embedded HTTP response.
		partBytes, readErr := io.ReadAll(part)
		part.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read part %d body: %w", count, readErr)
		}

		innerResp, err := http.ReadResponse(bufio.NewReader(bytes.NewReader(partBytes)), nil)
		if err != nil {
			return nil, fmt.Errorf("parse embedded response (Content-ID %q): %w", contentID, err)
		}
		innerBody, _ := io.ReadAll(innerResp.Body)
		innerResp.Body.Close()

		// Determine target slot. Prefer Content-ID match; fall back to sequential
		// position if the header is missing/malformed.
		slot := count
		if idxErr == nil && idx >= 0 && idx < expectedCount {
			slot = idx
		}
		if slot >= expectedCount {
			return nil, fmt.Errorf("part index %d exceeds expected count %d", slot, expectedCount)
		}
		if seen[slot] {
			return nil, fmt.Errorf("duplicate response for index %d", slot)
		}
		seen[slot] = true
		results[slot] = batchResponse{
			Status: innerResp.StatusCode,
			Body:   innerBody,
		}
		count++
	}

	if count != expectedCount {
		return nil, fmt.Errorf("got %d sub-responses, expected %d", count, expectedCount)
	}
	return results, nil
}

// parseResponseContentID extracts N from "<response-N>".
func parseResponseContentID(s string) (int, error) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "<")
	s = strings.TrimSuffix(s, ">")
	s = strings.TrimPrefix(s, "response-")
	if s == "" {
		return -1, fmt.Errorf("empty content-id")
	}
	return strconv.Atoi(s)
}

// parseCharonScopeError reads a Charon 407 body and returns a structured error
// matching apiGet's format. Shared with apiGet via a small refactor.
func parseCharonScopeError(body io.Reader) error {
	b, _ := io.ReadAll(body)
	return fmt.Errorf("charon 407 (batch): %s", string(b))
}

// retryOpts controls apiBatchWithRetry behavior. Default is 4 attempts,
// 200ms base delay, 5s cap; jitter is ±25%.
type retryOpts struct {
	MaxAttempts int
	BaseDelay   time.Duration
	MaxDelay    time.Duration
}

var defaultRetryOpts = retryOpts{
	MaxAttempts: 4,
	BaseDelay:   200 * time.Millisecond,
	MaxDelay:    5 * time.Second,
}

// sleepFunc is overridable for tests so retry loops don't actually sleep.
var sleepFunc = time.Sleep

// apiBatchWithRetry calls apiBatch with exponential backoff. Two retry
// surfaces are handled:
//
//   - Whole-batch transport / outer-status errors retry the whole batch.
//   - Per-sub-request 429 / 5xx statuses retry only the failing sub-requests
//     in the next attempt, preserving the per-request slot in the result.
//
// Auth errors (Charon 407 / scope_missing) and 4xx other than 429 are
// surfaced immediately — retrying them won't help.
func apiBatchWithRetry(account, scope string, reqs []batchRequest) ([]batchResponse, error) {
	return apiBatchWithRetryOpts(account, scope, reqs, defaultRetryOpts)
}

func apiBatchWithRetryOpts(account, scope string, reqs []batchRequest, opts retryOpts) ([]batchResponse, error) {
	if len(reqs) == 0 {
		return nil, nil
	}
	out := make([]batchResponse, len(reqs))
	// pending is the list of original-index positions still needing a result.
	pending := make([]int, len(reqs))
	for i := range pending {
		pending[i] = i
	}

	var lastErr error
	for attempt := 0; attempt < opts.MaxAttempts; attempt++ {
		current := make([]batchRequest, len(pending))
		for i, origIdx := range pending {
			current[i] = reqs[origIdx]
		}

		results, err := apiBatch(account, scope, current)
		if err != nil {
			if !isRetriableErr(err) {
				return nil, err
			}
			lastErr = err
			if attempt+1 == opts.MaxAttempts {
				return nil, fmt.Errorf("batch failed after %d attempts: %w", opts.MaxAttempts, err)
			}
			sleepFunc(backoffDelay(opts, attempt))
			continue
		}

		var nextPending []int
		for i, r := range results {
			origIdx := pending[i]
			if isRetriableStatus(r.Status) && attempt+1 < opts.MaxAttempts {
				nextPending = append(nextPending, origIdx)
				lastErr = fmt.Errorf("sub-request %d returned %d", origIdx, r.Status)
				continue
			}
			out[origIdx] = r
		}

		if len(nextPending) == 0 {
			return out, nil
		}
		pending = nextPending
		sleepFunc(backoffDelay(opts, attempt))
	}

	// Exhausted attempts with sub-requests still pending. Fill in their
	// last-seen status if any, then return an error.
	if lastErr != nil {
		return out, fmt.Errorf("batch retries exhausted: %w", lastErr)
	}
	return out, nil
}

// isRetriableStatus reports whether an HTTP status code from Gmail warrants
// a retry. 429 (rate-limited) and any 5xx are retriable.
func isRetriableStatus(s int) bool {
	return s == http.StatusTooManyRequests || (s >= 500 && s < 600)
}

// isRetriableErr decides whether an outer-batch error (returned from apiBatch
// itself) should retry. Transport errors and 5xx outer responses retry;
// auth-related and other 4xx don't.
func isRetriableErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	// Charon 407 (scope_missing) — retry won't help.
	if strings.Contains(msg, "charon 407") || strings.Contains(msg, "scope_missing") {
		return false
	}
	// Outer 4xx other than 429 — caller misconfigured something.
	if strings.Contains(msg, "batch endpoint returned 4") &&
		!strings.Contains(msg, "batch endpoint returned 429") {
		return false
	}
	return true
}

// backoffDelay computes exponential backoff with ±25% jitter for the given
// attempt number (0-indexed).
func backoffDelay(opts retryOpts, attempt int) time.Duration {
	delay := opts.BaseDelay * time.Duration(1<<attempt)
	if delay > opts.MaxDelay {
		delay = opts.MaxDelay
	}
	// Jitter in [-delay/4, delay/4)
	jitter := time.Duration(rand.Int63n(int64(delay)/2)) - delay/4
	return delay + jitter
}
