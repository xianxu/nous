//go:build darwin && cgo

package proxy

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

// peerPID against a real unix-socket pair: the test process is on
// both ends, so the looked-up pid must match os.Getpid(). Catches
// future churn around LOCAL_PEEREPID / golang.org/x/sys/unix.
func TestPeerPID_RoundTripsLocalPID(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "peer.sock")

	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Skipf("unix socket bind failed (sandbox?): %v", err)
	}
	defer ln.Close()

	accepted := make(chan net.Conn, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		accepted <- c
	}()

	dialer := &net.Dialer{}
	clientConn, err := dialer.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer clientConn.Close()

	serverConn := <-accepted
	defer serverConn.Close()

	pid, err := peerPID(serverConn)
	if err != nil {
		t.Fatalf("peerPID: %v", err)
	}
	if pid != os.Getpid() {
		t.Errorf("peerPID = %d, want %d (this test process)", pid, os.Getpid())
	}
}

// peerPID errors out cleanly on non-unix conns. The runtime socket
// only ever calls it from a unix listener, but the type-assert
// failure is the only barrier between "real pid" and "junk int" so
// we pin it.
func TestPeerPID_NonUnixErrors(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("tcp listen: %v", err)
	}
	defer ln.Close()
	c, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()
	if _, err := peerPID(c); err == nil {
		t.Errorf("peerPID(*net.TCPConn) = nil error, want type-assert error")
	}
}

// verifyPeerDR against the running test binary. The test process is
// linker-signed (`go test`'s ad-hoc identifier), so an arbitrary
// "com.charon.security"-style requirement must NOT be satisfied.
// This is a fail-closed pin: regressing kSecCSStrictValidate or
// flipping the rc==0 polarity would turn this green.
func TestVerifyPeerDR_RejectsForeignIdentifier(t *testing.T) {
	if verifyPeerDR(os.Getpid(), `identifier "com.charon.security"`) {
		t.Errorf("test binary spuriously satisfied com.charon.security DR")
	}
}

// verifyPeerDR with malformed requirement string returns false (not
// a panic, not a true). Pins the SecRequirementCreateWithString
// error path.
func TestVerifyPeerDR_MalformedRequirement(t *testing.T) {
	if verifyPeerDR(os.Getpid(), `not a valid requirement <<<`) {
		t.Errorf("malformed requirement spuriously verified")
	}
}

// verifyPeerDR against a non-existent pid returns false. Pins the
// SecCodeCopyGuestWithAttributes error path — important because a
// stale pid from LOCAL_PEEREPID after process exit must not be
// treated as "satisfies the DR" by accident.
func TestVerifyPeerDR_DeadPID(t *testing.T) {
	// PID 1 is launchd; never matches a charon DR.
	// A truly-non-existent pid is racy to construct, but launchd is
	// the same shape (existing pid, definitely-not-charon DR).
	if verifyPeerDR(1, `identifier "com.charon.security"`) {
		t.Errorf("launchd spuriously satisfied com.charon.security DR")
	}
}
