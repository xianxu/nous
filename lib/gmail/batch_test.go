package gmail

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestEncodeBatch_StructureAndContentIDs(t *testing.T) {
	reqs := []batchRequest{
		{Method: "GET", Path: "/threads/abc?format=metadata"},
		{Method: "GET", Path: "/threads/def?format=metadata"},
	}
	body, contentType, err := encodeBatch(reqs)
	if err != nil {
		t.Fatalf("encodeBatch: %v", err)
	}
	if !strings.HasPrefix(contentType, "multipart/mixed; boundary=") {
		t.Fatalf("content-type = %q, want multipart/mixed prefix", contentType)
	}

	// Parse the body back out and verify structure.
	_, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		t.Fatalf("parse content-type: %v", err)
	}
	mr := multipart.NewReader(body, params["boundary"])

	for i, want := range reqs {
		part, err := mr.NextPart()
		if err != nil {
			t.Fatalf("part %d: %v", i, err)
		}
		if got := part.Header.Get("Content-ID"); got != fmt.Sprintf("<%d>", i) {
			t.Errorf("part %d Content-ID = %q, want <%d>", i, got, i)
		}
		if got := part.Header.Get("Content-Type"); got != "application/http" {
			t.Errorf("part %d Content-Type = %q, want application/http", i, got)
		}
		b, _ := io.ReadAll(part)
		// Sub-request line should reference /gmail/v1/users/me prefix.
		wantLine := fmt.Sprintf("%s /gmail/v1/users/me%s HTTP/1.1", want.Method, want.Path)
		if !strings.HasPrefix(string(b), wantLine) {
			t.Errorf("part %d body = %q, want prefix %q", i, string(b), wantLine)
		}
	}
	if _, err := mr.NextPart(); err != io.EOF {
		t.Errorf("expected EOF after %d parts, got %v", len(reqs), err)
	}
}

// fakeBatchResponse builds a multipart/mixed body that mimics what Gmail
// returns: each part is "Content-Type: application/http" + "Content-ID: <response-N>",
// containing an embedded HTTP/1.1 response.
func fakeBatchResponse(t *testing.T, parts []struct {
	idx    int
	status int
	body   string
}) (string, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for _, p := range parts {
		hdr := make(map[string][]string)
		hdr["Content-Type"] = []string{"application/http"}
		hdr["Content-ID"] = []string{fmt.Sprintf("<response-%d>", p.idx)}
		w, err := mw.CreatePart(hdr)
		if err != nil {
			t.Fatalf("CreatePart: %v", err)
		}
		statusText := http.StatusText(p.status)
		fmt.Fprintf(w, "HTTP/1.1 %d %s\r\n", p.status, statusText)
		fmt.Fprintf(w, "Content-Type: application/json\r\n")
		fmt.Fprintf(w, "Content-Length: %d\r\n", len(p.body))
		fmt.Fprint(w, "\r\n")
		fmt.Fprint(w, p.body)
	}
	mw.Close()
	return buf.String(), "multipart/mixed; boundary=" + mw.Boundary()
}

func TestDecodeBatchResponse_OrderPreservedByContentID(t *testing.T) {
	// Send responses out of order; decoder should restore request order.
	bodyStr, ct := fakeBatchResponse(t, []struct {
		idx    int
		status int
		body   string
	}{
		{idx: 1, status: 200, body: `{"id":"thread-1"}`},
		{idx: 0, status: 200, body: `{"id":"thread-0"}`},
		{idx: 2, status: 404, body: `{"error":{"code":404}}`},
	})

	results, err := decodeBatchResponse(ct, strings.NewReader(bodyStr), 3)
	if err != nil {
		t.Fatalf("decodeBatchResponse: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("got %d results, want 3", len(results))
	}
	if results[0].Status != 200 || !strings.Contains(string(results[0].Body), "thread-0") {
		t.Errorf("results[0] = %+v, want thread-0 200", results[0])
	}
	if results[1].Status != 200 || !strings.Contains(string(results[1].Body), "thread-1") {
		t.Errorf("results[1] = %+v, want thread-1 200", results[1])
	}
	if results[2].Status != 404 {
		t.Errorf("results[2].Status = %d, want 404", results[2].Status)
	}
}

func TestDecodeBatchResponse_MissingPart(t *testing.T) {
	bodyStr, ct := fakeBatchResponse(t, []struct {
		idx    int
		status int
		body   string
	}{
		{idx: 0, status: 200, body: `{}`},
		// Note: only 1 part returned but caller expects 2.
	})
	_, err := decodeBatchResponse(ct, strings.NewReader(bodyStr), 2)
	if err == nil {
		t.Fatal("expected error for missing part, got nil")
	}
	if !strings.Contains(err.Error(), "got 1") {
		t.Errorf("error = %v, want mention of count mismatch", err)
	}
}

func TestApiBatch_RoundTrip(t *testing.T) {
	// Fake Gmail batch endpoint: read the request, return a multipart response
	// matching the request count.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("method = %s, want POST", r.Method)
		}
		// Verify Charon headers are set.
		if r.Header.Get("X-Charon-Account") != "user@example.com" {
			t.Errorf("X-Charon-Account = %q", r.Header.Get("X-Charon-Account"))
		}
		if r.Header.Get("X-Charon-Scope") != "gmail.readonly" {
			t.Errorf("X-Charon-Scope = %q", r.Header.Get("X-Charon-Scope"))
		}
		// Parse incoming multipart, count parts.
		_, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil {
			t.Fatalf("parse incoming content-type: %v", err)
		}
		mr := multipart.NewReader(r.Body, params["boundary"])
		count := 0
		for {
			p, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatalf("read part: %v", err)
			}
			// Drain the embedded request to ensure it parses as HTTP.
			body, _ := io.ReadAll(p)
			req, err := http.ReadRequest(bufio.NewReader(strings.NewReader(string(body) + "\r\n")))
			if err != nil {
				t.Errorf("part %d not a valid HTTP request: %v", count, err)
			} else if !strings.HasPrefix(req.URL.Path, "/gmail/v1/users/me/") {
				t.Errorf("part %d path = %q, want /gmail/v1/users/me/ prefix", count, req.URL.Path)
			}
			p.Close()
			count++
		}

		// Respond with one 200 per request, in reverse order to test ordering.
		respParts := make([]struct {
			idx    int
			status int
			body   string
		}, count)
		for i := 0; i < count; i++ {
			respParts[i] = struct {
				idx    int
				status int
				body   string
			}{idx: count - 1 - i, status: 200, body: fmt.Sprintf(`{"id":"t-%d"}`, count-1-i)}
		}
		bodyStr, ct := fakeBatchResponse(t, respParts)
		w.Header().Set("Content-Type", ct)
		w.WriteHeader(200)
		fmt.Fprint(w, bodyStr)
	}))
	defer srv.Close()

	// Override batch URL + http client to point at fake server with no TLS.
	origURL := gmailBatchURL
	gmailBatchURL = srv.URL
	defer func() { gmailBatchURL = origURL }()
	resetClientForTest(srv.Client())

	reqs := []batchRequest{
		{Method: "GET", Path: "/threads/a?format=metadata"},
		{Method: "GET", Path: "/threads/b?format=metadata"},
		{Method: "GET", Path: "/threads/c?format=metadata"},
	}
	results, err := apiBatch("user@example.com", "gmail.readonly", reqs)
	if err != nil {
		t.Fatalf("apiBatch: %v", err)
	}
	for i, r := range results {
		if r.Status != 200 {
			t.Errorf("results[%d].Status = %d", i, r.Status)
		}
		want := fmt.Sprintf(`"id":"t-%d"`, i)
		if !strings.Contains(string(r.Body), want) {
			t.Errorf("results[%d].Body = %q, want contains %q", i, string(r.Body), want)
		}
	}
}

func TestApiBatch_OuterError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		fmt.Fprint(w, "internal error")
	}))
	defer srv.Close()

	origURL := gmailBatchURL
	gmailBatchURL = srv.URL
	defer func() { gmailBatchURL = origURL }()
	resetClientForTest(srv.Client())

	_, err := apiBatch("u", "s", []batchRequest{{Method: "GET", Path: "/x"}})
	if err == nil {
		t.Fatal("expected error on 500, got nil")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error = %v, want mention of 500", err)
	}
}

func TestApiBatch_SizeLimit(t *testing.T) {
	reqs := make([]batchRequest, MaxBatchSize+1)
	for i := range reqs {
		reqs[i] = batchRequest{Method: "GET", Path: "/x"}
	}
	_, err := apiBatch("u", "s", reqs)
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Errorf("expected size-limit error, got %v", err)
	}
}

func TestApiBatch_Empty(t *testing.T) {
	results, err := apiBatch("u", "s", nil)
	if err != nil || results != nil {
		t.Errorf("apiBatch(nil) = %v, %v; want nil, nil", results, err)
	}
}

// resetClientForTest forces httpClient to use the test server's client (no TLS),
// bypassing the SSL_CERT_FILE Charon trust setup. Consumes clientOnce so future
// getClient() calls return the injected client.
func resetClientForTest(c *http.Client) {
	clientOnce = &sync.Once{}
	httpClient = c
	clientOnce.Do(func() {}) // burn the Once so getClient() doesn't overwrite
}

// withFastSleep replaces sleepFunc with a no-op so retry loops don't actually
// wait. Returns a restore function.
func withFastSleep(t *testing.T) func() {
	t.Helper()
	orig := sleepFunc
	sleepFunc = func(time.Duration) {}
	return func() { sleepFunc = orig }
}

func TestIsRetriableStatus(t *testing.T) {
	cases := []struct {
		s    int
		want bool
	}{
		{200, false},
		{404, false},
		{429, true},
		{500, true},
		{503, true},
		{599, true},
		{600, false},
	}
	for _, c := range cases {
		if got := isRetriableStatus(c.s); got != c.want {
			t.Errorf("isRetriableStatus(%d) = %v, want %v", c.s, got, c.want)
		}
	}
}

func TestIsRetriableErr(t *testing.T) {
	cases := []struct {
		msg  string
		want bool
	}{
		{"transport error", true},
		{"batch endpoint returned 500", true},
		{"batch endpoint returned 429: throttled", true},
		{"batch endpoint returned 400: bad request", false},
		{"batch endpoint returned 403: forbidden", false},
		{"charon 407 (batch): scope_missing", false},
	}
	for _, c := range cases {
		if got := isRetriableErr(fmt.Errorf("%s", c.msg)); got != c.want {
			t.Errorf("isRetriableErr(%q) = %v, want %v", c.msg, got, c.want)
		}
	}
}

func TestApiBatchWithRetry_OuterTransientThenSuccess(t *testing.T) {
	defer withFastSleep(t)()

	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomicAddInt32(&attempts, 1)
		if n == 1 {
			w.WriteHeader(503) // transient
			fmt.Fprint(w, "service unavailable")
			return
		}
		// Second attempt: succeed.
		bodyStr, ct := fakeBatchResponse(t, []struct {
			idx    int
			status int
			body   string
		}{
			{idx: 0, status: 200, body: `{"id":"ok"}`},
		})
		w.Header().Set("Content-Type", ct)
		w.WriteHeader(200)
		fmt.Fprint(w, bodyStr)
	}))
	defer srv.Close()

	origURL := gmailBatchURL
	gmailBatchURL = srv.URL
	defer func() { gmailBatchURL = origURL }()
	resetClientForTest(srv.Client())

	results, err := apiBatchWithRetry("u", "s", []batchRequest{{Method: "GET", Path: "/x"}})
	if err != nil {
		t.Fatalf("apiBatchWithRetry: %v", err)
	}
	if len(results) != 1 || results[0].Status != 200 {
		t.Fatalf("results = %+v", results)
	}
	if attempts != 2 {
		t.Errorf("attempts = %d, want 2", attempts)
	}
}

func TestApiBatchWithRetry_SubRequest429RetriesOnlyFailing(t *testing.T) {
	defer withFastSleep(t)()

	var attempt int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomicAddInt32(&attempt, 1)
		// Decode incoming so we know how many sub-requests.
		_, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
		mr := multipart.NewReader(r.Body, params["boundary"])
		var subCount int
		var subIDs []string
		for {
			p, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatalf("read sub: %v", err)
			}
			cid := p.Header.Get("Content-ID")
			subIDs = append(subIDs, strings.TrimSuffix(strings.TrimPrefix(cid, "<"), ">"))
			io.ReadAll(p)
			p.Close()
			subCount++
		}

		// Build response: on attempt 1, return 200 for first, 429 for second.
		// On attempt 2, return 200 for the (single) retried sub-request.
		respParts := make([]struct {
			idx    int
			status int
			body   string
		}, subCount)
		for i := 0; i < subCount; i++ {
			cid, _ := strconv.Atoi(subIDs[i])
			status := 200
			if n == 1 && i == 1 {
				status = 429
			}
			respParts[i] = struct {
				idx    int
				status int
				body   string
			}{idx: cid, status: status, body: fmt.Sprintf(`{"slot":%d,"attempt":%d}`, cid, n)}
		}
		bodyStr, ct := fakeBatchResponse(t, respParts)
		w.Header().Set("Content-Type", ct)
		w.WriteHeader(200)
		fmt.Fprint(w, bodyStr)
	}))
	defer srv.Close()

	origURL := gmailBatchURL
	gmailBatchURL = srv.URL
	defer func() { gmailBatchURL = origURL }()
	resetClientForTest(srv.Client())

	reqs := []batchRequest{
		{Method: "GET", Path: "/a"},
		{Method: "GET", Path: "/b"},
	}
	results, err := apiBatchWithRetry("u", "s", reqs)
	if err != nil {
		t.Fatalf("apiBatchWithRetry: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("len = %d", len(results))
	}
	if results[0].Status != 200 || !strings.Contains(string(results[0].Body), `"attempt":1`) {
		t.Errorf("results[0] = %+v, want 200 from attempt 1", results[0])
	}
	if results[1].Status != 200 || !strings.Contains(string(results[1].Body), `"attempt":2`) {
		t.Errorf("results[1] = %+v, want 200 from attempt 2 (after 429 retry)", results[1])
	}
	if attempt != 2 {
		t.Errorf("server attempts = %d, want 2", attempt)
	}
}

func TestApiBatchWithRetry_AuthErrorNotRetried(t *testing.T) {
	defer withFastSleep(t)()

	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomicAddInt32(&attempts, 1)
		w.WriteHeader(http.StatusProxyAuthRequired)
		fmt.Fprint(w, `{"error":"scope_missing","missing":["gmail.readonly"]}`)
	}))
	defer srv.Close()

	origURL := gmailBatchURL
	gmailBatchURL = srv.URL
	defer func() { gmailBatchURL = origURL }()
	resetClientForTest(srv.Client())

	_, err := apiBatchWithRetry("u", "s", []batchRequest{{Method: "GET", Path: "/x"}})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if attempts != 1 {
		t.Errorf("attempts = %d, want 1 (auth errors should not retry)", attempts)
	}
}

func TestApiBatchWithRetry_ExhaustReturnsError(t *testing.T) {
	defer withFastSleep(t)()

	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomicAddInt32(&attempts, 1)
		w.WriteHeader(503)
	}))
	defer srv.Close()

	origURL := gmailBatchURL
	gmailBatchURL = srv.URL
	defer func() { gmailBatchURL = origURL }()
	resetClientForTest(srv.Client())

	_, err := apiBatchWithRetry("u", "s", []batchRequest{{Method: "GET", Path: "/x"}})
	if err == nil {
		t.Fatal("expected error after exhaustion, got nil")
	}
	if attempts != int32(defaultRetryOpts.MaxAttempts) {
		t.Errorf("attempts = %d, want %d (max)", attempts, defaultRetryOpts.MaxAttempts)
	}
}

// atomicAddInt32 wraps sync/atomic for test brevity.
func atomicAddInt32(p *int32, delta int32) int32 {
	return atomic.AddInt32(p, delta)
}

