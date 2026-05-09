package proxy

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func osStat(p string) (os.FileInfo, error) { return os.Stat(p) }

// devSocket spins up a RuntimeSocket on a temp path with the
// unsigned-peer bypass enabled so tests can drive the wire protocol
// without a real signed Charon Security.app. Returns the dial path.
//
// Uses a per-test directory under $HOME (not /tmp) because some
// sandboxed test environments deny unix-socket bind() in /tmp even
// when file creation works there.
func devSocket(t *testing.T, srv *Server) string {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("home dir: %v", err)
	}
	dir, err := os.MkdirTemp(filepath.Join(home, ".cache"), "charon-runtime-test-*")
	if err != nil {
		// Fall back to t.TempDir; will fail under sandbox, but on
		// non-sandboxed runs it works.
		dir = t.TempDir()
	} else {
		t.Cleanup(func() { _ = os.RemoveAll(dir) })
	}
	path := filepath.Join(dir, "runtime.sock")
	t.Setenv("CHARON_RUNTIME_SOCKET_PATH", path)
	t.Setenv(AllowUnsignedRuntimePeerEnv, "1")
	rs, err := StartRuntimeSocket(srv)
	if err != nil {
		t.Skipf("StartRuntimeSocket: %v (likely sandbox-restricted unix bind)", err)
	}
	t.Cleanup(func() { _ = rs.Close() })
	// Tiny grace so the goroutine is ready to Accept.
	time.Sleep(20 * time.Millisecond)
	return path
}

// roundTrip dials the socket, sends one JSON request, reads one JSON
// response. Mirrors the connection-per-RPC contract documented on
// runtimeRequest.
func roundTrip(t *testing.T, path string, req runtimeRequest) runtimeResponse {
	t.Helper()
	c, err := net.Dial("unix", path)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(2 * time.Second))
	if err := json.NewEncoder(c).Encode(req); err != nil {
		t.Fatalf("encode: %v", err)
	}
	var resp runtimeResponse
	if err := json.NewDecoder(c).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return resp
}

func TestRuntimeSocket_Arm(t *testing.T) {
	srv := &Server{Session: NewSession(), Audit: NopAuditLog()}
	path := devSocket(t, srv)
	resp := roundTrip(t, path, runtimeRequest{Op: "arm", TTLSeconds: 600})
	if !resp.OK {
		t.Fatalf("arm response not OK: %+v", resp)
	}
	if resp.Status == nil || !resp.Status.Armed {
		t.Errorf("expected armed=true, got status=%+v", resp.Status)
	}
	if !srv.Session.IsArmed() {
		t.Error("Session should be armed after socket-driven Arm")
	}
}

func TestRuntimeSocket_Disarm(t *testing.T) {
	srv := &Server{Session: NewSession(), Audit: NopAuditLog()}
	srv.Session.Arm(time.Hour)
	path := devSocket(t, srv)
	resp := roundTrip(t, path, runtimeRequest{Op: "disarm"})
	if !resp.OK {
		t.Fatalf("disarm response not OK: %+v", resp)
	}
	if srv.Session.IsArmed() {
		t.Error("Session should be disarmed after socket-driven Disarm")
	}
}

func TestRuntimeSocket_Status(t *testing.T) {
	srv := &Server{Session: NewSession(), Audit: NopAuditLog()}
	srv.Session.Arm(time.Hour)
	path := devSocket(t, srv)
	resp := roundTrip(t, path, runtimeRequest{Op: "status"})
	if !resp.OK || resp.Status == nil || !resp.Status.Armed {
		t.Errorf("status: %+v", resp)
	}
}

func TestRuntimeSocket_AuditRecent(t *testing.T) {
	srv := &Server{Session: NewSession(), Audit: NopAuditLog()}
	// Inject an entry directly so Recent() returns something.
	srv.Audit.Log(AuditEntry{Timestamp: time.Now(), Method: "GET", Host: "gmail.googleapis.com"})
	path := devSocket(t, srv)
	resp := roundTrip(t, path, runtimeRequest{Op: "audit_recent", SinceSeconds: 60})
	if !resp.OK {
		t.Fatalf("audit_recent: %+v", resp)
	}
	if len(resp.Entries) != 1 {
		t.Errorf("expected 1 entry, got %d", len(resp.Entries))
	}
	if resp.Entries[0].Host != "gmail.googleapis.com" {
		t.Errorf("entry.Host = %q", resp.Entries[0].Host)
	}
}

func TestRuntimeSocket_UnknownOp(t *testing.T) {
	srv := &Server{Session: NewSession(), Audit: NopAuditLog()}
	path := devSocket(t, srv)
	resp := roundTrip(t, path, runtimeRequest{Op: "spawn-rocket"})
	if resp.OK {
		t.Error("unknown op should return ok=false")
	}
	if resp.Error == "" {
		t.Error("unknown op should set error message")
	}
}

func TestRuntimeSocket_NilSessionGetsClearError(t *testing.T) {
	srv := &Server{Audit: NopAuditLog()}
	path := devSocket(t, srv)
	resp := roundTrip(t, path, runtimeRequest{Op: "arm", TTLSeconds: 60})
	if resp.OK {
		t.Error("arm with nil Session should error, not succeed")
	}
}

// Permission check — ensure the socket file is mode 0600 so other
// uids on the box can't connect at all (defense in depth atop the
// DR check).
func TestRuntimeSocket_FileMode0600(t *testing.T) {
	srv := &Server{Session: NewSession(), Audit: NopAuditLog()}
	path := devSocket(t, srv)
	// stat the socket file; permission bits should be 0600.
	info, err := osStat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	mode := info.Mode().Perm()
	if mode != 0o600 {
		t.Errorf("socket perms = %v, want 0600", mode)
	}
}
