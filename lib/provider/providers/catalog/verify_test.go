package catalog

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func entryWithVerifyURL(verifyURL string) Entry {
	return Entry{
		ID:        "test",
		Name:      "Test",
		VerifyURL: verifyURL,
		Auth: Auth{
			Style:        "header",
			Header:       "x-api-key",
			ExtraHeaders: map[string]string{"anthropic-version": "2023-06-01"},
		},
	}
}

func TestVerify_NoURL_IsNoOp(t *testing.T) {
	e := Entry{ID: "noverify", Auth: Auth{Style: "bearer"}}
	res, err := e.Verify(context.Background(), "anykey")
	if res != VerifyOK || err != nil {
		t.Errorf("Verify with no URL = (%v, %v), want (VerifyOK, nil)", res, err)
	}
}

func TestVerify_2xx_ReturnsOK(t *testing.T) {
	var gotAuth, gotVersion string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("x-api-key")
		gotVersion = r.Header.Get("anthropic-version")
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, `{"data":[]}`)
	}))
	defer srv.Close()

	e := entryWithVerifyURL(srv.URL)
	res, err := e.Verify(context.Background(), "sk-ant-test-key")
	if res != VerifyOK || err != nil {
		t.Errorf("Verify 200 = (%v, %v), want (VerifyOK, nil)", res, err)
	}
	if gotAuth != "sk-ant-test-key" {
		t.Errorf("verify call missing x-api-key auth: got %q", gotAuth)
	}
	if gotVersion != "2023-06-01" {
		t.Errorf("verify call missing anthropic-version: got %q", gotVersion)
	}
}

func TestVerify_401_ReturnsRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, `{"error":"invalid_api_key"}`)
	}))
	defer srv.Close()
	e := entryWithVerifyURL(srv.URL)
	res, err := e.Verify(context.Background(), "bad-key")
	if res != VerifyRejected {
		t.Errorf("Verify 401 = %v, want VerifyRejected", res)
	}
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Errorf("Verify 401 error = %v, want 401-mentioning error", err)
	}
}

func TestVerify_403_ReturnsRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	e := entryWithVerifyURL(srv.URL)
	res, _ := e.Verify(context.Background(), "scoped-key")
	if res != VerifyRejected {
		t.Errorf("Verify 403 = %v, want VerifyRejected (key is the bad cause)", res)
	}
}

func TestVerify_500_ReturnsEndpointError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		io.WriteString(w, `oops`)
	}))
	defer srv.Close()
	e := entryWithVerifyURL(srv.URL)
	res, err := e.Verify(context.Background(), "any-key")
	if res != VerifyEndpointError {
		t.Errorf("Verify 500 = %v, want VerifyEndpointError (inconclusive — provider issue, not key)", res)
	}
	if err == nil {
		t.Error("Verify 500 should produce a descriptive error")
	}
}

func TestVerify_NetworkError_ReturnsEndpointError(t *testing.T) {
	// Unreachable URL — closed-port localhost guarantees connection refused.
	e := entryWithVerifyURL("http://127.0.0.1:1") // port 1 is reserved/unused
	res, err := e.Verify(context.Background(), "any")
	if res != VerifyEndpointError {
		t.Errorf("Verify network-fail = %v, want VerifyEndpointError", res)
	}
	if err == nil {
		t.Error("Verify network-fail should produce an error")
	}
}
