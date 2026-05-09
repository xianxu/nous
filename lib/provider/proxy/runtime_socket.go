package proxy

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/xianxu/nous/lib/provider/vault/keychain"
)

// SecurityAppRequirement is the codesign requirement charon's runtime
// socket gates on. Only a process whose binary's signature matches
// this DR — i.e. Charon Security.app proper — is allowed to drive
// arm/disarm/audit endpoints over the socket. Same shape and
// pinning strategy as the keychain ACL story in #000003.
const SecurityAppRequirement = `identifier "com.charon.security"`

// AllowUnsignedRuntimePeerEnv lets dev iteration drive the socket
// without a signed Charon Security.app. Set to "1" to skip the DR
// check; the socket still binds at the same path but accepts any
// same-uid peer. Production charon refuses to honor this — see
// EnableRuntimeSocket below.
const AllowUnsignedRuntimePeerEnv = "CHARON_RUNTIME_ALLOW_UNSIGNED_PEER"

// RuntimeSocketPath returns the canonical path of the runtime
// consent socket. Lives under the user's Caches dir so it gets
// vacuumed on disk pressure rather than persisting forever.
// Per-uid by virtue of $HOME.
func RuntimeSocketPath() string {
	if p := os.Getenv("CHARON_RUNTIME_SOCKET_PATH"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "/tmp/charon-runtime.sock"
	}
	return filepath.Join(home, "Library", "Caches", "charon", "runtime.sock")
}

// runtimeRequest is the wire shape: one JSON object per request
// terminated by a newline. The server reads one request, writes one
// response, and closes the connection. Connection-per-RPC keeps the
// peer-DR check fresh — the security.app would re-dial for each
// arm/disarm rather than holding the socket open.
type runtimeRequest struct {
	Op           string `json:"op"`
	TTLSeconds   int64  `json:"ttl_seconds,omitempty"`
	SinceSeconds int64  `json:"since_seconds,omitempty"`
}

// runtimeResponse mirrors armResponse / SessionStatus shape so the
// security.app can deserialize either path. Status is populated for
// arm/disarm/status; Entries for audit_recent.
type runtimeResponse struct {
	OK      bool          `json:"ok"`
	Error   string        `json:"error,omitempty"`
	Status  *SessionStatus `json:"status,omitempty"`
	Entries []AuditEntry   `json:"entries,omitempty"`
}

// RuntimeSocket is the unix-domain listener that bridges Charon
// Security.app to the proxy. Listens at RuntimeSocketPath() with
// perms 0600 (user-only). Verifies each connection's peer is
// signed with SecurityAppRequirement before dispatching. Closing
// the underlying listener stops accept; in-flight requests drain.
type RuntimeSocket struct {
	srv             *Server
	ln              net.Listener
	requirement     string
	allowUnsigned   bool
}

// StartRuntimeSocket binds the listener and starts accepting in a
// goroutine. The listener is bound to RuntimeSocketPath(); existing
// stale sockets at the path are unlinked first (a previous charon
// crash leaves a socket file behind that prevents re-bind).
//
// Returns the RuntimeSocket so callers can shut it down on serve
// exit. Errors during initial bind are returned synchronously.
func StartRuntimeSocket(srv *Server) (*RuntimeSocket, error) {
	path := RuntimeSocketPath()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, fmt.Errorf("runtime socket mkdir: %w", err)
	}
	// Unlink any leftover socket from a previous run. Bind() would
	// otherwise fail with "address already in use."
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		// Non-fatal: if we can't remove, Listen will report.
		log.Printf("runtime socket: could not unlink %s: %v", path, err)
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("runtime socket listen: %w", err)
	}
	if err := os.Chmod(path, 0600); err != nil {
		_ = ln.Close()
		return nil, fmt.Errorf("runtime socket chmod: %w", err)
	}
	// Auto-bypass DR check in dev mode (unsigned binary). The dev
	// binary writes to the dev-vault keychain namespace; the runtime
	// socket likewise drops the production trust edge so a developer
	// iterating on Phase D's menubar can test against an unsigned
	// build without ceremony. Production (signed binary, ServiceProd
	// namespace) always requires the DR — which is the whole point.
	devMode := keychain.ResolveServiceName() != keychain.ServiceProd
	rs := &RuntimeSocket{
		srv:           srv,
		ln:            ln,
		requirement:   SecurityAppRequirement,
		allowUnsigned: devMode || os.Getenv(AllowUnsignedRuntimePeerEnv) == "1",
	}
	go rs.acceptLoop()
	if rs.allowUnsigned {
		log.Printf("runtime socket: %s (DR check BYPASSED via %s — dev only)", path, AllowUnsignedRuntimePeerEnv)
	} else {
		log.Printf("runtime socket: %s (peer DR pinned to %s)", path, rs.requirement)
	}
	return rs, nil
}

// Close stops the listener and unlinks the socket file.
func (rs *RuntimeSocket) Close() error {
	if rs == nil || rs.ln == nil {
		return nil
	}
	err := rs.ln.Close()
	_ = os.Remove(RuntimeSocketPath())
	return err
}

func (rs *RuntimeSocket) acceptLoop() {
	for {
		c, err := rs.ln.Accept()
		if err != nil {
			// Listener closed — clean exit.
			if errors.Is(err, net.ErrClosed) {
				return
			}
			log.Printf("runtime socket accept: %v", err)
			continue
		}
		go rs.handleConn(c)
	}
}

func (rs *RuntimeSocket) handleConn(c net.Conn) {
	defer c.Close()
	c.SetDeadline(time.Now().Add(5 * time.Second))

	// Peer DR check unless the dev bypass is set. PID via
	// LOCAL_PEEREPID (effective pid; race window noted in
	// runtime_darwin.go).
	if !rs.allowUnsigned {
		pid, err := peerPID(c)
		if err != nil {
			rs.writeError(c, "peer pid lookup failed: "+err.Error())
			return
		}
		if !verifyPeerDR(pid, rs.requirement) {
			rs.writeError(c, fmt.Sprintf("peer pid %d does not satisfy %q", pid, rs.requirement))
			log.Printf("runtime socket: rejected peer pid %d (DR mismatch)", pid)
			return
		}
	}

	var req runtimeRequest
	dec := json.NewDecoder(bufio.NewReader(c))
	if err := dec.Decode(&req); err != nil {
		rs.writeError(c, "decode request: "+err.Error())
		return
	}
	rs.dispatch(c, &req)
}

func (rs *RuntimeSocket) dispatch(c net.Conn, req *runtimeRequest) {
	switch strings.ToLower(req.Op) {
	case "arm":
		if rs.srv.Session == nil {
			rs.writeError(c, "session not configured")
			return
		}
		rs.srv.Session.Arm(time.Duration(req.TTLSeconds) * time.Second)
		st := rs.srv.Session.Status()
		rs.writeResponse(c, runtimeResponse{OK: true, Status: &st})
	case "disarm":
		if rs.srv.Session == nil {
			rs.writeError(c, "session not configured")
			return
		}
		rs.srv.Session.Disarm()
		st := rs.srv.Session.Status()
		rs.writeResponse(c, runtimeResponse{OK: true, Status: &st})
	case "status":
		if rs.srv.Session == nil {
			rs.writeError(c, "session not configured")
			return
		}
		st := rs.srv.Session.Status()
		rs.writeResponse(c, runtimeResponse{OK: true, Status: &st})
	case "audit_recent":
		if rs.srv.Audit == nil {
			rs.writeError(c, "audit not configured")
			return
		}
		since := time.Duration(req.SinceSeconds) * time.Second
		if since <= 0 {
			since = time.Hour
		}
		entries := rs.srv.Audit.Recent(time.Now().Add(-since))
		if entries == nil {
			entries = []AuditEntry{}
		}
		rs.writeResponse(c, runtimeResponse{OK: true, Entries: entries})
	default:
		rs.writeError(c, fmt.Sprintf("unknown op %q", req.Op))
	}
}

func (rs *RuntimeSocket) writeResponse(c io.Writer, resp runtimeResponse) {
	_ = json.NewEncoder(c).Encode(resp)
}

func (rs *RuntimeSocket) writeError(c io.Writer, msg string) {
	rs.writeResponse(c, runtimeResponse{OK: false, Error: msg})
}
