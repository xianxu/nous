// Package notify delivers user-facing macOS notifications from
// anywhere in nous (menubar arm/disarm events, security-audit
// findings, future surfaces). The substrate routes by signed-vs-
// unsigned state so dev iteration doesn't need a signed bundle:
//
//   signed + .app bundle  → UserNotifications.framework (cgo)
//   signed without bundle → terminal-notifier (fallback; UNUSR
//                           silently no-ops without a CFBundleIdentifier)
//   unsigned dev binary   → terminal-notifier when available, else osascript
//
// terminal-notifier is the preferred fallback because it supports
// actions (reply, snooze, click → URL) that the menubar will grow into.
// osascript is the universal last-resort; works without any deps but
// no actions and notifications are attributed to "Script Editor."
//
// Backend selection happens lazily at first Send() call and is cached
// for the process lifetime. Tests swap via SetBackend.
package notify

import "sync"

// Notification is the shape every caller produces. Subtitle is
// optional; Title and Body are required (the backends silently coerce
// empty values, but the resulting banner looks degenerate).
type Notification struct {
	Title    string
	Subtitle string
	Body     string
}

// Backend dispatches a Notification to the OS-level notification
// surface. Returns the underlying error from the shell-out / cgo call;
// most callers ignore it because a failed notification is best-effort
// (the menubar / TUI / log line still went through).
type Backend func(Notification) error

var (
	currentMu sync.Mutex
	current   Backend
)

// Send delivers the notification via the current backend. Selects
// (and caches) the backend on first call. Safe to call concurrently.
func Send(n Notification) error {
	currentMu.Lock()
	if current == nil {
		current = pickBackend()
	}
	b := current
	currentMu.Unlock()
	return b(n)
}

// SetBackend overrides the backend, e.g. in tests. Bypasses the
// pickBackend dispatch. Pass nil to clear (next Send picks again).
func SetBackend(b Backend) {
	currentMu.Lock()
	current = b
	currentMu.Unlock()
}
