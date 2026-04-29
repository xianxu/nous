package gmail

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestDecodeBase64URL(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{"simple", "SGVsbG8gV29ybGQ", "Hello World", false},
		{"with url-safe chars", "SGVsbG8-V29ybGQ_", "Hello>World?", false}, // - maps to +, _ maps to /
		{"padding 2", "YQ", "a", false},
		{"padding 1", "YWI", "ab", false},
		{"no padding needed", "YWJj", "abc", false},
		{"empty", "", "", false},
		{"invalid", "!!!!", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := decodeBase64URL(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("decodeBase64URL() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("decodeBase64URL() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseHeaders(t *testing.T) {
	headers := []header{
		{Name: "From", Value: "alice@example.com"},
		{Name: "To", Value: "bob@example.com"},
		{Name: "Subject", Value: "Hello"},
	}
	m := parseHeaders(headers)
	if m["From"] != "alice@example.com" {
		t.Errorf("From = %q, want alice@example.com", m["From"])
	}
	if m["Subject"] != "Hello" {
		t.Errorf("Subject = %q, want Hello", m["Subject"])
	}
	if len(m) != 3 {
		t.Errorf("len = %d, want 3", len(m))
	}
}

func TestHeaderOr(t *testing.T) {
	m := map[string]string{"From": "alice@example.com"}

	if got := headerOr(m, "From", "?"); got != "alice@example.com" {
		t.Errorf("existing key: got %q, want alice@example.com", got)
	}
	if got := headerOr(m, "Subject", "(none)"); got != "(none)" {
		t.Errorf("missing key: got %q, want (none)", got)
	}
}

func TestExtractBody(t *testing.T) {
	tests := []struct {
		name    string
		payload payload
		want    string
	}{
		{
			name: "direct text/plain",
			payload: payload{
				MimeType: "text/plain",
				Body:     body{Data: "SGVsbG8gV29ybGQ"}, // "Hello World"
			},
			want: "Hello World",
		},
		{
			name: "multipart with text/plain child",
			payload: payload{
				MimeType: "multipart/alternative",
				Parts: []payload{
					{MimeType: "text/plain", Body: body{Data: "SGVsbG8"}},   // "Hello"
					{MimeType: "text/html", Body: body{Data: "PFBIPG8K"}},  // html
				},
			},
			want: "Hello",
		},
		{
			name: "nested multipart",
			payload: payload{
				MimeType: "multipart/mixed",
				Parts: []payload{
					{
						MimeType: "multipart/alternative",
						Parts: []payload{
							{MimeType: "text/plain", Body: body{Data: "TmVzdGVk"}}, // "Nested"
							{MimeType: "text/html", Body: body{Data: "PFBIPG8K"}},
						},
					},
					{MimeType: "application/pdf", Body: body{Data: "cGRm"}},
				},
			},
			want: "Nested",
		},
		{
			name: "no text/plain",
			payload: payload{
				MimeType: "multipart/alternative",
				Parts: []payload{
					{MimeType: "text/html", Body: body{Data: "PFBIPG8K"}},
				},
			},
			want: "",
		},
		{
			name: "empty payload",
			payload: payload{MimeType: "text/plain"},
			want:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractBody(tt.payload)
			if got != tt.want {
				t.Errorf("extractBody() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestSearchThreads tests the full search flow with a mock HTTP server,
// including the batch endpoint used for metadata fan-out.
func TestSearchThreads(t *testing.T) {
	// Per-thread metadata payloads the mock will return.
	threadData := map[string]string{
		"thread1": `{"id":"thread1","messages":[
			{"id":"msg1","snippet":"First thread snippet","payload":{"headers":[
				{"name":"Subject","value":"Hello"},
				{"name":"From","value":"alice@example.com"},
				{"name":"Date","value":"Mon, 1 Jan 2026 00:00:00 +0000"}
			]}},
			{"id":"msg2","snippet":"reply","payload":{"headers":[]}}
		]}`,
		"thread2": `{"id":"thread2","messages":[
			{"id":"msg3","snippet":"Second thread","payload":{"headers":[
				{"name":"Subject","value":"Goodbye"},
				{"name":"From","value":"bob@example.com"},
				{"name":"Date","value":"Tue, 2 Jan 2026 00:00:00 +0000"}
			]}}
		]}`,
	}

	mux := http.NewServeMux()

	// threads.list — unchanged
	mux.HandleFunc("/gmail/v1/users/me/threads", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Charon-Account") != "test@example.com" {
			t.Errorf("missing X-Charon-Account header")
		}
		if r.Header.Get("X-Charon-Scope") != "gmail.readonly" {
			t.Errorf("X-Charon-Scope = %q, want gmail.readonly", r.Header.Get("X-Charon-Scope"))
		}
		json.NewEncoder(w).Encode(map[string]any{
			"threads": []map[string]string{
				{"id": "thread1"},
				{"id": "thread2"},
			},
		})
	})

	// Batch endpoint — parse multipart, look up each sub-request's thread,
	// emit a multipart response with one part per sub-request.
	mux.HandleFunc("/batch/gmail/v1", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Charon-Scope") != "gmail.readonly" {
			t.Errorf("batch X-Charon-Scope = %q", r.Header.Get("X-Charon-Scope"))
		}
		_, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil {
			t.Fatalf("parse incoming content-type: %v", err)
		}
		mr := multipart.NewReader(r.Body, params["boundary"])

		type sub struct {
			contentID string
			threadID  string
		}
		var subs []sub
		for {
			p, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatalf("read sub-part: %v", err)
			}
			cid := p.Header.Get("Content-ID")
			body, _ := io.ReadAll(p)
			p.Close()
			// Parse "GET /gmail/v1/users/me/threads/<id>?..." from first line.
			line := strings.SplitN(string(body), "\r\n", 2)[0]
			parts := strings.Fields(line)
			if len(parts) < 2 {
				t.Fatalf("bad request line: %q", line)
			}
			path := parts[1]
			path = strings.TrimPrefix(path, "/gmail/v1/users/me/threads/")
			path = strings.SplitN(path, "?", 2)[0]
			subs = append(subs, sub{contentID: cid, threadID: path})
		}

		// Build multipart response.
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		for _, s := range subs {
			h := make(map[string][]string)
			h["Content-Type"] = []string{"application/http"}
			// Echo as <response-N>: take the digits inside <N> from the request CID.
			cid := strings.TrimSuffix(strings.TrimPrefix(s.contentID, "<"), ">")
			h["Content-ID"] = []string{fmt.Sprintf("<response-%s>", cid)}
			pw, _ := mw.CreatePart(h)
			body, ok := threadData[s.threadID]
			if !ok {
				fmt.Fprintf(pw, "HTTP/1.1 404 Not Found\r\nContent-Type: application/json\r\nContent-Length: %d\r\n\r\n%s",
					len(`{"error":{"code":404}}`), `{"error":{"code":404}}`)
				continue
			}
			fmt.Fprintf(pw, "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: %d\r\n\r\n%s",
				len(body), body)
		}
		mw.Close()
		w.Header().Set("Content-Type", "multipart/mixed; boundary="+mw.Boundary())
		w.WriteHeader(200)
		w.Write(buf.Bytes())
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	// Override the API base URLs and client for testing
	origAPI := gmailAPI
	origBatch := gmailBatchURL
	origOnce := clientOnce
	origClient := httpClient

	gmailAPI = srv.URL + "/gmail/v1/users/me"
	gmailBatchURL = srv.URL + "/batch/gmail/v1"
	clientOnce = &sync.Once{}
	httpClient = nil
	clientOnce.Do(func() { httpClient = srv.Client() })

	defer func() {
		gmailAPI = origAPI
		gmailBatchURL = origBatch
		clientOnce = origOnce
		httpClient = origClient
	}()

	summaries, err := SearchThreads("test@example.com", "query", 10)
	if err != nil {
		t.Fatalf("SearchThreads() error: %v", err)
	}
	if len(summaries) != 2 {
		t.Fatalf("got %d summaries, want 2", len(summaries))
	}

	// Verify order preserved
	if summaries[0].ID != "thread1" {
		t.Errorf("first thread ID = %q, want thread1", summaries[0].ID)
	}
	if summaries[0].Subject != "Hello" {
		t.Errorf("first subject = %q, want Hello", summaries[0].Subject)
	}
	if summaries[0].Sender != "alice@example.com" {
		t.Errorf("first sender = %q, want alice@example.com", summaries[0].Sender)
	}
	if summaries[0].MessageCount != 2 {
		t.Errorf("first message count = %d, want 2", summaries[0].MessageCount)
	}
	if summaries[1].ID != "thread2" {
		t.Errorf("second thread ID = %q, want thread2", summaries[1].ID)
	}
	if summaries[1].Subject != "Goodbye" {
		t.Errorf("second subject = %q, want Goodbye", summaries[1].Subject)
	}
}

// TestGetThread tests full thread retrieval with body extraction.
func TestGetThread(t *testing.T) {
	mux := http.NewServeMux()

	mux.HandleFunc("/gmail/v1/users/me/threads/t1", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"id": "t1",
			"messages": []map[string]any{
				{
					"id": "m1",
					"payload": map[string]any{
						"mimeType": "multipart/alternative",
						"headers": []map[string]string{
							{"name": "From", "value": "alice@example.com"},
							{"name": "To", "value": "bob@example.com"},
							{"name": "Date", "value": "Mon, 1 Jan 2026 00:00:00 +0000"},
							{"name": "Subject", "value": "Test"},
						},
						"parts": []map[string]any{
							{"mimeType": "text/plain", "body": map[string]any{"data": "SGVsbG8gQm9i"}}, // "Hello Bob"
							{"mimeType": "text/html", "body": map[string]any{"data": "PGI-SGk8L2I-"}},
						},
					},
				},
				{
					"id": "m2",
					"payload": map[string]any{
						"mimeType": "text/plain",
						"headers": []map[string]string{
							{"name": "From", "value": "bob@example.com"},
							{"name": "To", "value": "alice@example.com"},
							{"name": "Date", "value": "Mon, 1 Jan 2026 01:00:00 +0000"},
							{"name": "Subject", "value": "Re: Test"},
						},
						"body": map[string]any{"data": "SGkgQWxpY2U"}, // "Hi Alice"
					},
				},
			},
		})
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	origAPI := gmailAPI
	origOnce := clientOnce
	origClient := httpClient

	gmailAPI = srv.URL + "/gmail/v1/users/me"
	clientOnce = &sync.Once{}
	httpClient = nil
	clientOnce.Do(func() { httpClient = srv.Client() })

	defer func() {
		gmailAPI = origAPI
		clientOnce = origOnce
		httpClient = origClient
	}()

	thread, err := GetThread("test@example.com", "t1")
	if err != nil {
		t.Fatalf("GetThread() error: %v", err)
	}
	if thread.ID != "t1" {
		t.Errorf("thread ID = %q, want t1", thread.ID)
	}
	if len(thread.Messages) != 2 {
		t.Fatalf("got %d messages, want 2", len(thread.Messages))
	}

	msg1 := thread.Messages[0]
	if msg1.Sender != "alice@example.com" {
		t.Errorf("msg1 sender = %q, want alice@example.com", msg1.Sender)
	}
	if msg1.Body != "Hello Bob" {
		t.Errorf("msg1 body = %q, want Hello Bob", msg1.Body)
	}

	msg2 := thread.Messages[1]
	if msg2.Sender != "bob@example.com" {
		t.Errorf("msg2 sender = %q, want bob@example.com", msg2.Sender)
	}
	if msg2.Body != "Hi Alice" {
		t.Errorf("msg2 body = %q, want Hi Alice", msg2.Body)
	}
	if msg2.Subject != "Re: Test" {
		t.Errorf("msg2 subject = %q, want Re: Test", msg2.Subject)
	}
}
