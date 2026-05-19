//go:build !darwin

package notify

// Non-darwin stub. nous's notification surface is currently macOS-only
// — every consumer ships into a Mac context (operator's host, tart VM,
// wife's machine). When Linux support arrives, this is where notify-send
// / dbus dispatch will live.

func pickBackend() Backend {
	return noopBackend
}

func noopBackend(Notification) error { return nil }

// RequestAuth is a no-op outside darwin. Kept exported so consumers
// (the menubar startup path) don't need build tags around the call.
func RequestAuth() {}
