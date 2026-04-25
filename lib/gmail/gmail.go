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
func SearchThreads(account, query string, maxResults int) ([]ThreadSummary, error) {
	// Step 1: list thread IDs
	params := url.Values{
		"q":          {query},
		"maxResults": {strconv.Itoa(maxResults)},
	}
	var listResp struct {
		Threads []struct {
			ID string `json:"id"`
		} `json:"threads"`
	}
	if err := apiGet(account, "/threads?"+params.Encode(), &listResp); err != nil {
		return nil, fmt.Errorf("list threads: %w", err)
	}

	// Step 2: fetch metadata for each thread (parallel)
	type result struct {
		idx     int
		summary ThreadSummary
		err     error
	}
	ch := make(chan result, len(listResp.Threads))
	for i, t := range listResp.Threads {
		go func(i int, id string) {
			var threadResp threadResponse
			if err := apiGet(account, "/threads/"+id+"?format=metadata", &threadResp); err != nil {
				ch <- result{idx: i, err: fmt.Errorf("get thread %s metadata: %w", id, err)}
				return
			}
			if len(threadResp.Messages) == 0 {
				ch <- result{idx: i}
				return
			}
			msg := threadResp.Messages[0]
			headers := parseHeaders(msg.Payload.Headers)
			ch <- result{idx: i, summary: ThreadSummary{
				ID:           id,
				Subject:      headerOr(headers, "Subject", "(no subject)"),
				Sender:       headerOr(headers, "From", "?"),
				Date:         headerOr(headers, "Date", "?"),
				Snippet:      msg.Snippet,
				MessageCount: len(threadResp.Messages),
			}}
		}(i, t.ID)
	}

	results := make([]result, 0, len(listResp.Threads))
	for range listResp.Threads {
		results = append(results, <-ch)
	}
	// Check errors, preserve order
	for _, r := range results {
		if r.err != nil {
			return nil, r.err
		}
	}
	// Sort by original index to maintain Gmail's relevance ordering
	summaries := make([]ThreadSummary, 0, len(results))
	ordered := make(map[int]ThreadSummary, len(results))
	for _, r := range results {
		if r.summary.ID != "" {
			ordered[r.idx] = r.summary
		}
	}
	for i := range listResp.Threads {
		if s, ok := ordered[i]; ok {
			summaries = append(summaries, s)
		}
	}
	return summaries, nil
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
	clientOnce sync.Once
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

	resp, err := getClient().Do(req)
	if err != nil {
		return fmt.Errorf("HTTP request failed: %w", err)
	}
	defer func() {
		// Drain remaining body so the connection can be reused (keep-alive).
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}()

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
