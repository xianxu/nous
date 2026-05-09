package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeListBody mimics Anthropic's GET /v1/organizations/api_keys response
// shape: a `data` array of objects each carrying an opaque id and a
// partial_key_hint suffix the pasted key can be matched against.
const fakeListBody = `{
  "data": [
    {"id": "apikey_001", "partial_key_hint": "sk-ant-…AAAA", "status": "active"},
    {"id": "apikey_002", "partial_key_hint": "sk-ant-…ZZZZ", "status": "active"},
    {"id": "apikey_003", "partial_key_hint": "sk-ant-…XYZW", "status": "inactive"}
  ],
  "has_more": false
}`

// pastedKey is a fake key whose suffix matches apikey_002's hint.
const pastedKey = "sk-ant-api03-abcdefghijklmnopqrstuvwxyZZZZ"

func anthropicLikeEntry(listURL, revokeURL string) Entry {
	return Entry{
		ID:               "anthropic",
		Name:             "Anthropic",
		HostnamePatterns: []string{"api.anthropic.com"},
		Auth: Auth{
			Style:        "header",
			Header:       "x-api-key",
			ExtraHeaders: map[string]string{"anthropic-version": "2023-06-01"},
		},
		Revoke: &Revoke{
			ListEndpoint: &ListEndpoint{
				URL:        listURL,
				KeyMatch:   "partial_key_hint",
				ResultPath: "data[].id",
			},
			Method: "POST",
			URL:    revokeURL,
			Body:   `{"status":"inactive"}`,
		},
	}
}

func TestRevoke_NoEndpoint_ReturnsSentinel(t *testing.T) {
	e := Entry{ID: "noop", Auth: Auth{Style: "bearer"}}
	err := e.RevokeKey(context.Background(), "anykey")
	if !errors.Is(err, ErrNoRevokeEndpoint) {
		t.Fatalf("Revoke() with no schema = %v, want ErrNoRevokeEndpoint", err)
	}
}

func TestRevoke_ListThenDeactivate_HappyPath(t *testing.T) {
	var listCalls, revokeCalls int
	var listAuth, revokeAuth string
	var revokeBody string

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/organizations/api_keys", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "GET":
			listCalls++
			listAuth = r.Header.Get("x-api-key")
			if r.Header.Get("anthropic-version") != "2023-06-01" {
				t.Errorf("list missing anthropic-version header: %v", r.Header)
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			io.WriteString(w, fakeListBody)
		default:
			t.Errorf("unexpected method on list URL: %s", r.Method)
		}
	})
	mux.HandleFunc("/v1/organizations/api_keys/apikey_002", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("revoke method = %s, want POST", r.Method)
		}
		revokeCalls++
		revokeAuth = r.Header.Get("x-api-key")
		body, _ := io.ReadAll(r.Body)
		revokeBody = string(body)
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, `{"id":"apikey_002","status":"inactive"}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	e := anthropicLikeEntry(
		srv.URL+"/v1/organizations/api_keys",
		srv.URL+"/v1/organizations/api_keys/{key_id}",
	)
	if err := e.RevokeKey(context.Background(), pastedKey); err != nil {
		t.Fatalf("Revoke() = %v, want nil", err)
	}
	if listCalls != 1 || revokeCalls != 1 {
		t.Errorf("calls: list=%d revoke=%d, want 1/1", listCalls, revokeCalls)
	}
	if listAuth != pastedKey || revokeAuth != pastedKey {
		t.Errorf("auth: list=%q revoke=%q, want both = pasted key", listAuth, revokeAuth)
	}
	var got map[string]string
	if err := json.Unmarshal([]byte(revokeBody), &got); err != nil {
		t.Fatalf("revoke body parse: %v (body=%q)", err, revokeBody)
	}
	if got["status"] != "inactive" {
		t.Errorf("revoke body status = %q, want inactive", got["status"])
	}
}

func TestRevoke_ListNoMatch_ReturnsError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/organizations/api_keys", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, fakeListBody)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	e := anthropicLikeEntry(
		srv.URL+"/v1/organizations/api_keys",
		srv.URL+"/v1/organizations/api_keys/{key_id}",
	)
	// Suffix that doesn't match any hint in fakeListBody.
	err := e.RevokeKey(context.Background(), "sk-ant-api03-aaaaaaaaaaaaaaaaaaaaaQQQQ")
	if err == nil {
		t.Fatal("Revoke() with non-matching key = nil, want error")
	}
	if !errors.Is(err, ErrKeyNotFound) {
		t.Errorf("Revoke() = %v, want wrapping ErrKeyNotFound", err)
	}
}

func TestRevoke_ListReturns401_ReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, `{"error":"invalid_api_key"}`)
	}))
	defer srv.Close()
	e := anthropicLikeEntry(srv.URL, srv.URL+"/{key_id}")
	err := e.RevokeKey(context.Background(), pastedKey)
	if err == nil {
		t.Fatal("Revoke() with 401 list = nil, want error")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("Revoke() error = %q, want 401 mentioned", err.Error())
	}
}

func TestRevoke_RevokeReturns500_ReturnsError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/organizations/api_keys", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, fakeListBody)
	})
	mux.HandleFunc("/v1/organizations/api_keys/apikey_002", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		io.WriteString(w, `boom`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	e := anthropicLikeEntry(
		srv.URL+"/v1/organizations/api_keys",
		srv.URL+"/v1/organizations/api_keys/{key_id}",
	)
	err := e.RevokeKey(context.Background(), pastedKey)
	if err == nil {
		t.Fatal("Revoke() with 500 deactivate = nil, want error")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("Revoke() error = %q, want 500 mentioned", err.Error())
	}
}

func TestRevoke_DirectRevoke_NoListEndpoint(t *testing.T) {
	// Hypothetical provider where the pasted key IS the id (DELETE
	// /keys/{key} with the key as bearer). Exercises the no-list path.
	var revokeCalls int
	var gotURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "DELETE" {
			t.Errorf("method = %s, want DELETE", r.Method)
		}
		revokeCalls++
		gotURL = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	e := Entry{
		ID: "hypothetical",
		Auth: Auth{Style: "bearer"},
		Revoke: &Revoke{
			Method: "DELETE",
			URL:    srv.URL + "/keys/{key_id}",
		},
	}
	if err := e.RevokeKey(context.Background(), "secret-key-123"); err != nil {
		t.Fatalf("Revoke() = %v, want nil", err)
	}
	if revokeCalls != 1 {
		t.Errorf("revokeCalls = %d, want 1", revokeCalls)
	}
	if gotURL != "/keys/secret-key-123" {
		t.Errorf("gotURL = %q, want /keys/secret-key-123", gotURL)
	}
}

func TestRevoke_PartialKeyHint_MatchByLastFour(t *testing.T) {
	// Lock the matcher contract: hint format "<prefix>…<suffix>" or
	// "<prefix>...<suffix>"; pasted key must end with the suffix.
	cases := []struct {
		name      string
		hint      string
		pasted    string
		wantMatch bool
	}{
		{"unicode-ellipsis", "sk-ant-…XYZW", "abcXYZW", true},
		{"ascii-dots", "sk-ant-...XYZW", "abcXYZW", true},
		{"no-match", "sk-ant-…XYZW", "abcAAAA", false},
		{"empty-pasted", "sk-ant-…XYZW", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := matchesPartialKeyHint(tc.hint, tc.pasted)
			if got != tc.wantMatch {
				t.Errorf("matchesPartialKeyHint(%q, %q) = %v, want %v",
					tc.hint, tc.pasted, got, tc.wantMatch)
			}
		})
	}
}
