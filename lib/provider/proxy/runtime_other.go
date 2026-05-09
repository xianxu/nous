//go:build !darwin || !cgo

package proxy

// verifyPeerDR is a no-op on non-darwin/non-cgo builds. The runtime-
// consent socket is darwin-only (Charon Security.app is a macOS
// bundle); other platforms shouldn't be running the listener at all.
// Returning false here makes any peer rejected if the socket
// somehow does come up.
func verifyPeerDR(pid int, requirement string) bool {
	return false
}
