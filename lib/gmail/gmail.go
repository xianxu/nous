// Package gmail provides Gmail API access through the Charon proxy.
//
// No credential management — Charon handles OAuth, token refresh, and injection.
// The library sets X-Charon-Account to select the account; the proxy adds the bearer token.
//
// Requires: invocation via `charon run` so HTTPS_PROXY is set.
package gmail

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
)

var gmailAPI = "https://gmail.googleapis.com/gmail/v1/users/me"

// ThreadSummary is the search-result view of a thread.
type ThreadSummary struct {
	ID           string `json:"id"`
	Subject      string `json:"subject"`
	Sender       string `json:"sender"`
	Date         string `json:"date"`
	Snippet      string `json:"snippet"`
	MessageCount int    `json:"message_count"`
}

// Message is a single message within a thread.
type Message struct {
	Sender  string `json:"sender"`
	To      string `json:"to"`
	Date    string `json:"date"`
	Subject string `json:"subject"`
	Body    string `json:"body"`
}

// Thread is a full Gmail thread with all messages.
type Thread struct {
	ID       string    `json:"id"`
	Messages []Message `json:"messages"`
}

// SearchThreads finds threads matching a Gmail query for the given account.
// idEntry is the slim {id} record returned by threads.list / messages.list.
type idEntry struct {
	ID string `json:"id"`
}

func SearchThreads(account, query string, maxResults int) ([]ThreadSummary, error) {
	// Step 1: list thread IDs (paginate until we have maxResults; Gmail caps at 500/page)
	var ids []idEntry
	pageToken := ""
	for len(ids) < maxResults {
		remaining := maxResults - len(ids)
		page := remaining
		if page > 500 {
			page = 500
		}
		params := url.Values{
			"q":          {query},
			"maxResults": {strconv.Itoa(page)},
		}
		if pageToken != "" {
			params.Set("pageToken", pageToken)
		}
		var listResp struct {
			Threads       []idEntry `json:"threads"`
			NextPageToken string    `json:"nextPageToken"`
		}
		if err := apiGet(account, "/threads?"+params.Encode(), &listResp); err != nil {
			return nil, fmt.Errorf("list threads: %w", err)
		}
		ids = append(ids, listResp.Threads...)
		if listResp.NextPageToken == "" || len(listResp.Threads) == 0 {
			break
		}
		pageToken = listResp.NextPageToken
	}
	if len(ids) > maxResults {
		ids = ids[:maxResults]
	}

	// Step 2: fetch metadata via batched threads.get. Chunk by Gmail's 100/batch
	// cap; run multiple batches in parallel under a semaphore. Per-user concurrent
	// cap (~10–20 outer connections) is the binding constraint, so 8 batches in
	// flight = up to 800 logical sub-requests "open" without tripping the cap.
	chunks := chunkSlice(ids, MaxBatchSize)
	summaries := make([]ThreadSummary, len(ids))

	sem := make(chan struct{}, 8)
	errCh := make(chan error, len(chunks))
	var wg sync.WaitGroup
	for chunkIdx, chunk := range chunks {
		wg.Add(1)
		go func(chunkIdx int, chunk []idEntry) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			if err := fetchMetadataBatch(account, summaries, chunkIdx*MaxBatchSize, chunk); err != nil {
				errCh <- err
			}
		}(chunkIdx, chunk)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			return nil, err
		}
	}

	// Filter out empty slots (404s, empty threads). Order is preserved because
	// each goroutine writes to a disjoint contiguous range of the slice.
	out := make([]ThreadSummary, 0, len(summaries))
	for _, s := range summaries {
		if s.ID != "" {
			out = append(out, s)
		}
	}
	return out, nil
}

// fetchMetadataBatch issues one batched threads.get?format=metadata for the
// given chunk and writes parsed ThreadSummary entries into out[baseIdx:].
// Out-of-range threads (404) are left as zero-valued entries; the caller
// filters those.
func fetchMetadataBatch(account string, out []ThreadSummary, baseIdx int, chunk []idEntry) error {
	reqs := make([]batchRequest, len(chunk))
	for i, e := range chunk {
		reqs[i] = batchRequest{Method: "GET", Path: "/threads/" + e.ID + "?format=metadata"}
	}
	results, err := apiBatchWithRetry(account, "gmail.readonly", reqs)
	if err != nil {
		return fmt.Errorf("batch threads.get: %w", err)
	}
	for i, r := range results {
		switch {
		case r.Status == http.StatusNotFound:
			continue // thread vanished; leave zero-valued
		case r.Status != http.StatusOK:
			return fmt.Errorf("thread %s: status %d: %s", chunk[i].ID, r.Status, string(r.Body))
		}
		var threadResp threadResponse
		if err := json.Unmarshal(r.Body, &threadResp); err != nil {
			return fmt.Errorf("thread %s: parse JSON: %w", chunk[i].ID, err)
		}
		if len(threadResp.Messages) == 0 {
			continue
		}
		msg := threadResp.Messages[0]
		headers := parseHeaders(msg.Payload.Headers)
		out[baseIdx+i] = ThreadSummary{
			ID:           chunk[i].ID,
			Subject:      headerOr(headers, "Subject", "(no subject)"),
			Sender:       headerOr(headers, "From", "?"),
			Date:         headerOr(headers, "Date", "?"),
			Snippet:      msg.Snippet,
			MessageCount: len(threadResp.Messages),
		}
	}
	return nil
}

// chunkSlice splits a slice into chunks of size <= n.
func chunkSlice[T any](s []T, n int) [][]T {
	if n <= 0 || len(s) == 0 {
		return nil
	}
	chunks := make([][]T, 0, (len(s)+n-1)/n)
	for i := 0; i < len(s); i += n {
		end := i + n
		if end > len(s) {
			end = len(s)
		}
		chunks = append(chunks, s[i:end])
	}
	return chunks
}

// GetThread retrieves a full thread with all message bodies.
func GetThread(account, threadID string) (*Thread, error) {
	var resp threadResponse
	if err := apiGet(account, "/threads/"+threadID+"?format=full", &resp); err != nil {
		return nil, fmt.Errorf("get thread %s: %w", threadID, err)
	}

	messages := make([]Message, 0, len(resp.Messages))
	for _, msg := range resp.Messages {
		headers := parseHeaders(msg.Payload.Headers)
		messages = append(messages, Message{
			Sender:  headerOr(headers, "From", "?"),
			To:      headerOr(headers, "To", "?"),
			Date:    headerOr(headers, "Date", "?"),
			Subject: headerOr(headers, "Subject", "?"),
			Body:    extractBody(msg.Payload),
		})
	}
	return &Thread{ID: threadID, Messages: messages}, nil
}

// --- Gmail API response types (internal) ---

type threadResponse struct {
	ID       string           `json:"id"`
	Messages []messagePayload `json:"messages"`
}

type messagePayload struct {
	ID      string  `json:"id"`
	Snippet string  `json:"snippet"`
	Payload payload `json:"payload"`
}

type payload struct {
	MimeType string   `json:"mimeType"`
	Headers  []header `json:"headers"`
	Body     body     `json:"body"`
	Parts    []payload `json:"parts"`
}

type header struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type body struct {
	Data string `json:"data"`
	Size int    `json:"size"`
}

// --- helpers ---

// httpClient returns an *http.Client that trusts the CA cert from SSL_CERT_FILE.
// Go on macOS does not always respect SSL_CERT_FILE, so we load it explicitly.
// The client is built once and reused.
var (
	clientOnce = &sync.Once{}
	httpClient *http.Client
)

func getClient() *http.Client {
	clientOnce.Do(func() {
		httpClient = http.DefaultClient // fallback

		certFile := os.Getenv("SSL_CERT_FILE")
		if certFile == "" {
			return
		}
		pem, err := os.ReadFile(certFile)
		if err != nil {
			return
		}
		pool, err := x509.SystemCertPool()
		if err != nil {
			pool = x509.NewCertPool()
		}
		pool.AppendCertsFromPEM(pem)
		httpClient = &http.Client{
			Transport: &http.Transport{
				Proxy:               http.ProxyFromEnvironment,
				TLSClientConfig:     &tls.Config{RootCAs: pool},
				MaxIdleConnsPerHost: 10, // parallel metadata fetches reuse connections
			},
		}
	})
	return httpClient
}

func apiGet(account, path string, dest any) error {
	req, err := http.NewRequest("GET", gmailAPI+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-Charon-Account", account)
	// Read-only Gmail operations need gmail.readonly. Charon enforces by
	// returning HTTP 407 with a structured fix command if it's not granted
	// yet (see charon/docs/agent-protocol.md).
	req.Header.Set("X-Charon-Scope", "gmail.readonly")

	resp, err := getClient().Do(req)
	if err != nil {
		return fmt.Errorf("HTTP request failed: %w", err)
	}
	defer func() {
		// Drain remaining body so the connection can be reused (keep-alive).
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}()

	if resp.StatusCode == http.StatusProxyAuthRequired { // 407 from charon
		b, _ := io.ReadAll(resp.Body)
		var charonErr struct {
			Error    string   `json:"error"`
			Missing  []string `json:"missing"`
			Account  string   `json:"account"`
			Provider string   `json:"provider"`
			Fix      string   `json:"fix"`
		}
		if json.Unmarshal(b, &charonErr) == nil && charonErr.Error == "scope_missing" {
			return fmt.Errorf("missing scope %v for %s. To fix: run `charon auth` (TUI) or `%s`",
				charonErr.Missing, charonErr.Account, charonErr.Fix)
		}
		return fmt.Errorf("charon 407: %s", string(b))
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("API returned %d: %s", resp.StatusCode, string(b))
	}
	return json.NewDecoder(resp.Body).Decode(dest)
}

func parseHeaders(headers []header) map[string]string {
	m := make(map[string]string, len(headers))
	for _, h := range headers {
		m[h.Name] = h.Value
	}
	return m
}

func headerOr(headers map[string]string, key, fallback string) string {
	if v, ok := headers[key]; ok {
		return v
	}
	return fallback
}

// extractBody recursively finds the text/plain body in a MIME payload.
// Gmail returns base64url-encoded body data.
func extractBody(p payload) string {
	// Direct text/plain with data
	if p.MimeType == "text/plain" && p.Body.Data != "" {
		if decoded, err := decodeBase64URL(p.Body.Data); err == nil {
			return decoded
		}
	}

	// Check immediate children for text/plain first
	for _, part := range p.Parts {
		if part.MimeType == "text/plain" && part.Body.Data != "" {
			if decoded, err := decodeBase64URL(part.Body.Data); err == nil {
				return decoded
			}
		}
	}

	// Recurse into children
	for _, part := range p.Parts {
		if result := extractBody(part); result != "" {
			return result
		}
	}

	return ""
}

func decodeBase64URL(s string) (string, error) {
	// Gmail uses base64url encoding (RFC 4648 §5) without padding
	s = strings.ReplaceAll(s, "-", "+")
	s = strings.ReplaceAll(s, "_", "/")
	// Add padding if needed
	switch len(s) % 4 {
	case 2:
		s += "=="
	case 3:
		s += "="
	}
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
