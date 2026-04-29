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

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, readHTTPError(resp)
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

// apiBatchWithRetry calls apiBatch with retry classification. Two retry
// surfaces are handled:
//
//   - Whole-batch transport / outer-status errors retry the whole batch via
//     classifyRetry (handles 429, 5xx, 403-rateLimitExceeded, etc.).
//   - Per-sub-request status errors retry only the failing sub-requests in
//     the next attempt, preserving the per-request slot in the result. Each
//     sub-response status is classified via the same shared logic by wrapping
//     it in an httpStatusErr.
//
// Auth errors (Charon 407 / scope_missing), dailyLimitExceeded, and 4xx other
// than the rate-limit family are surfaced immediately — retrying won't help.
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
			retry, wait := classifyRetry(err)
			if !retry {
				return nil, err
			}
			lastErr = err
			if attempt+1 == opts.MaxAttempts {
				break
			}
			if wait == 0 {
				wait = backoffDelay(opts, attempt)
			}
			sleepFunc(wait)
			continue
		}

		var nextPending []int
		var subWait time.Duration
		for i, r := range results {
			origIdx := pending[i]
			subErr := subResponseAsError(r)
			if subErr == nil {
				out[origIdx] = r
				continue
			}
			retry, wait := classifyRetry(subErr)
			if !retry {
				// Non-retriable sub-status (e.g. 404 vanished thread). Pass
				// the response through to the caller; it's the final state.
				out[origIdx] = r
				continue
			}
			nextPending = append(nextPending, origIdx)
			lastErr = fmt.Errorf("sub-request %s: %w", reqs[origIdx].Path, subErr)
			if wait > subWait {
				subWait = wait
			}
		}

		if len(nextPending) == 0 {
			return out, nil
		}
		if attempt+1 == opts.MaxAttempts {
			break
		}
		pending = nextPending
		if subWait == 0 {
			subWait = backoffDelay(opts, attempt)
		}
		sleepFunc(subWait)
	}

	// Exhausted attempts with retriable sub-requests still pending.
	return nil, fmt.Errorf("batch retries exhausted (%d attempts): %w", opts.MaxAttempts, lastErr)
}

// subResponseAsError converts a sub-response into an httpStatusErr if its
// status is non-2xx, so classifyRetry can dispatch on it. Returns nil for 2xx.
func subResponseAsError(r batchResponse) error {
	if r.Status >= 200 && r.Status < 300 {
		return nil
	}
	return &httpStatusErr{
		Status: r.Status,
		Reason: parseGmailErrorReason(r.Body),
		Body:   r.Body,
	}
}
