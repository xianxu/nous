//go:build darwin

package proxy

import (
	"fmt"
	"net"

	"golang.org/x/sys/unix"
)

// peerPID returns the effective pid of the peer connected via a
// unix-domain socket on darwin. Uses getsockopt(LOCAL_PEEREPID).
//
// Returns an error if c is not a *net.UnixConn or the syscall fails.
// Note the TOCTOU caveat in runtime_darwin.go's verifyPeerDR comment:
// the pid here is best-effort and can race with fork/exec; the
// production path will harden via audit-token (kSecGuestAttributeAudit)
// in a follow-up.
func peerPID(c net.Conn) (int, error) {
	uc, ok := c.(*net.UnixConn)
	if !ok {
		return 0, fmt.Errorf("peer is not a unix socket: %T", c)
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return 0, err
	}
	var pid int
	var sockErr error
	cerr := raw.Control(func(fd uintptr) {
		// LOCAL_PEEREPID = 0x003. Defined in <sys/un.h> on darwin.
		// SOL_LOCAL = 0; the macOS-specific level for local-domain
		// socket options. golang.org/x/sys/unix exposes both.
		pid, sockErr = unix.GetsockoptInt(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEEREPID)
	})
	if cerr != nil {
		return 0, cerr
	}
	if sockErr != nil {
		return 0, sockErr
	}
	return pid, nil
}
