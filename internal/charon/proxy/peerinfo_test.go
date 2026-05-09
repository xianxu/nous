package proxy

import (
	"net"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"
)

func execLookPath(name string) (string, error) { return exec.LookPath(name) }

// Real-TCP self-test: spin up a listener, connect to it from the same
// test process, then look up the peer that owns the connection's
// LOCAL port. The test process is both the server and the client, so
// the resolved PID must equal os.Getpid() when the test's filtering
// logic works (lsof will report both endpoints; we want the LOCAL
// side of peerPort).
//
// macOS-only because lsof's output format and the parsing in
// peerinfo.go assume BSD lsof; Linux ships a different lsof variant
// with subtly different `-Fpn` output (pid format diverges).
func TestResolvePeer_SelfTCPConnection(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("peerinfo lookup is darwin-only (BSD lsof)")
	}
	if _, err := execLookPath("lsof"); err != nil {
		t.Skipf("lsof unavailable: %v", err)
	}

	// Server side: bind localhost:0, accept once.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() {
		c, _ := ln.Accept()
		if c != nil {
			defer c.Close()
			// Hold open long enough for the lsof lookup to observe
			// it. lsof needs the connection in ESTABLISHED state.
			time.Sleep(2 * time.Second)
		}
	}()

	// Client side: dial. After Dial returns, the connection is
	// established and lsof should see it.
	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	clientLocal, ok := conn.LocalAddr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("LocalAddr not *net.TCPAddr: %T", conn.LocalAddr())
	}

	// Tiny grace period so the kernel has registered ESTABLISHED.
	time.Sleep(200 * time.Millisecond)

	got := ResolvePeer(clientLocal.Port)
	if got == nil {
		t.Fatalf("ResolvePeer(%d) returned nil — expected to find this test process", clientLocal.Port)
	}
	if got.PID != os.Getpid() {
		t.Errorf("PID = %d, want this test's pid %d", got.PID, os.Getpid())
	}
	// Exe / ParentChain are populated by `ps` shellouts. Some
	// sandboxed test environments allow exec("lsof") but silently
	// deny exec("ps"); we don't fail on missing exe — the PID
	// match is the contract that matters here.
}

// Looking up a port nobody is using returns nil rather than erroring.
// Best-effort contract: caller logs "unknown" and proceeds.
func TestResolvePeer_NonexistentPort(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("peerinfo lookup is darwin-only")
	}
	// Port 1 — reserved tcpmux, almost never bound.
	if got := ResolvePeer(1); got != nil {
		t.Errorf("ResolvePeer(unbound port) = %+v, want nil", got)
	}
}

// readPidExe should give the absolute path of the executable on
// macOS — `ps -o command=`'s first whitespace-separated token. The
// test asserts the path looks like a path (contains /), but doesn't
// pin the exact value because go's test binary path varies.
func TestReadPidExe_AbsolutePath(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("ps -o command= shape is BSD-specific")
	}
	if _, err := execLookPath("ps"); err != nil {
		t.Skipf("ps unavailable: %v", err)
	}
	exe := readPidExe(os.Getpid())
	if exe == "" {
		t.Skip("ps returned empty (sandbox?) — not a contract violation")
	}
	if !strings.Contains(exe, "/") {
		t.Errorf("readPidExe = %q, expected absolute path with /", exe)
	}
}

// Out-of-range port shorts to nil without invoking lsof.
func TestResolvePeer_InvalidPort(t *testing.T) {
	for _, p := range []int{0, -1} {
		if got := ResolvePeer(p); got != nil {
			t.Errorf("ResolvePeer(%d) = %+v, want nil", p, got)
		}
	}
}

