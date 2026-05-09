//go:build !darwin

package proxy

import (
	"fmt"
	"net"
)

func peerPID(c net.Conn) (int, error) {
	return 0, fmt.Errorf("peer pid lookup not implemented on this platform")
}
