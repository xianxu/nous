package proxy

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Disarmed CONNECT must return 407 with structured JSON. The error
// body's `fix` field is what agents surface to humans, so we pin it
// here to avoid silent UX drift.
func TestSession_Gate_DisarmedCONNECTReturns407(t *testing.T) {
	srv := &Server{
		Audit:   NopAuditLog(),
		Session: NewSession(), // boots disarmed
	}
	req := httptest.NewRequest(http.MethodConnect, "https://gmail.googleapis.com:443", nil)
	req.Host = "gmail.googleapis.com:443"
	w := httptest.NewRecorder()
	srv.handleConnect(w, req)

	if w.Code != http.StatusProxyAuthRequired {
		t.Fatalf("status = %d, want 407", w.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("body not JSON: %v\n%s", err, w.Body)
	}
	if body["error"] != "session_disarmed" {
		t.Errorf("error = %q, want session_disarmed", body["error"])
	}
	if !strings.Contains(body["fix"], "charon arm") {
		t.Errorf("fix should mention 'charon arm', got %q", body["fix"])
	}
}

// Disarmed denials must still appear in the audit ring so that
// `charon who` can show what was knocking on the proxy while the
// user was away. Visibility is the entire reason the gate logs at
// all — without this, background processes hammering the proxy
// look indistinguishable from silence.
func TestSession_Gate_DisarmedRequestIsAudited(t *testing.T) {
	audit := NopAuditLog()
	srv := &Server{
		Audit:   audit,
		Session: NewSession(),
		Now:     time.Now,
	}
	req := httptest.NewRequest(http.MethodConnect, "https://gmail.googleapis.com:443", nil)
	req.Host = "gmail.googleapis.com:443"
	srv.handleConnect(httptest.NewRecorder(), req)

	plainReq := httptest.NewRequest(http.MethodGet, "http://example.com/foo", nil)
	srv.handleHTTP(httptest.NewRecorder(), plainReq)

	entries := audit.Recent(time.Time{})
	if len(entries) != 2 {
		t.Fatalf("expected 2 audited denials, got %d", len(entries))
	}
	for _, e := range entries {
		if e.StatusCode != http.StatusProxyAuthRequired {
			t.Errorf("entry %q: status = %d, want 407", e.Method, e.StatusCode)
		}
		if e.Error != "session_disarmed" {
			t.Errorf("entry %q: error = %q, want session_disarmed", e.Method, e.Error)
		}
	}
	if entries[0].Method != "CONNECT" || entries[0].Host != "gmail.googleapis.com" {
		t.Errorf("CONNECT entry = %+v", entries[0])
	}
	if entries[1].Method != "GET" || entries[1].Host != "example.com" || entries[1].Path != "/foo" {
		t.Errorf("HTTP entry = %+v", entries[1])
	}
}

// /session/arm with no body uses default TTL.
func TestSession_HTTP_ArmDefault(t *testing.T) {
	srv := &Server{Session: NewSession()}
	req := httptest.NewRequest(http.MethodPost, "/session/arm", nil)
	w := httptest.NewRecorder()
	srv.handleSessionArm(w, req)
	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
	var resp armResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.OK || !resp.Status.Armed {
		t.Errorf("expected armed, got %+v", resp)
	}
}

func TestSession_HTTP_ArmWithExplicitTTL(t *testing.T) {
	srv := &Server{Session: NewSession()}
	body := strings.NewReader(`{"ttl_seconds": 300}`)
	req := httptest.NewRequest(http.MethodPost, "/session/arm", body)
	req.ContentLength = int64(body.Len())
	w := httptest.NewRecorder()
	srv.handleSessionArm(w, req)
	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
	var resp armResponse
	json.NewDecoder(w.Body).Decode(&resp)
	want := 5 * time.Minute
	got := resp.Status.AbsoluteCapAt.Sub(resp.Status.ArmedAt)
	if got != want {
		t.Errorf("AbsoluteCap delta = %v, want %v", got, want)
	}
}

func TestSession_HTTP_DisarmDropsArm(t *testing.T) {
	srv := &Server{Session: NewSession()}
	srv.Session.Arm(time.Hour)

	req := httptest.NewRequest(http.MethodPost, "/session/disarm", nil)
	w := httptest.NewRecorder()
	srv.handleSessionDisarm(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
	if srv.Session.IsArmed() {
		t.Error("Disarm endpoint should drop armed state")
	}
}

func TestSession_HTTP_StatusReturnsCurrent(t *testing.T) {
	srv := &Server{Session: NewSession()}
	srv.Session.Arm(time.Hour)
	req := httptest.NewRequest(http.MethodGet, "/session/status", nil)
	w := httptest.NewRecorder()
	srv.handleSessionStatus(w, req)
	var st SessionStatus
	json.NewDecoder(w.Body).Decode(&st)
	if !st.Armed {
		t.Errorf("expected armed=true, got %+v", st)
	}
}

// arm/disarm reject non-POST. status accepts GET (default cobra case).
func TestSession_HTTP_RejectsNonPOSTOnArm(t *testing.T) {
	srv := &Server{Session: NewSession()}
	req := httptest.NewRequest(http.MethodGet, "/session/arm", nil)
	w := httptest.NewRecorder()
	srv.handleSessionArm(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", w.Code)
	}
}

// nil Session means the gate is off (legacy behavior for tests that
// pre-date this issue). Verifies handleConnect doesn't fault on a
// nil-Session Server.
func TestSession_NilSessionDoesNotGate(t *testing.T) {
	// Spin up an httptest server so we have a real listener — the
	// gate path returns early without touching listener state.
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {})
	hs := httptest.NewServer(mux)
	defer hs.Close()

	srv := &Server{Session: nil, Audit: NopAuditLog()} // gate disabled, audit no-op
	req := httptest.NewRequest(http.MethodConnect, hs.URL, nil)
	w := httptest.NewRecorder()
	// We don't run a full CONNECT — just verify the gate doesn't
	// fire (would set 407). Past the gate, handleConnect tries to
	// hijack which httptest.Recorder doesn't support; we accept any
	// non-407 status as evidence the gate didn't catch.
	srv.handleConnect(w, req)
	if w.Code == http.StatusProxyAuthRequired {
		body, _ := io.ReadAll(w.Body)
		if strings.Contains(string(body), "session_disarmed") {
			t.Errorf("nil Session should NOT gate; got 407 session_disarmed")
		}
	}
}
