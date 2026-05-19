//go:build darwin

package notify

import (
	"fmt"
	"os/exec"

	"github.com/xianxu/nous/lib/codesign"
)

// Swappable for tests. hasBundle wraps the cgo call in
// userns_darwin.go; terminalNotifierPath wraps exec.LookPath so tests
// can simulate "terminal-notifier installed / missing" without
// touching the PATH.
var (
	hasBundle            = realHasBundle
	terminalNotifierPath = func() (string, error) { return exec.LookPath("terminal-notifier") }
)

// pickBackend selects the right backend at first Send. Decision tree:
//
//	signed + bundled  → UserNotifications.framework
//	(else) terminal-notifier installed → terminal-notifier
//	(else) → osascript
//
// "signed without bundle" falls through to terminal-notifier — that's
// the common state after `make nous-install` until a separate signed
// .app wrapper for the menubar lands (deferred follow-up).
func pickBackend() Backend {
	if codesign.IsSigned() && hasBundle() {
		return userNotificationsBackend
	}
	if _, err := terminalNotifierPath(); err == nil {
		return terminalNotifierBackend
	}
	return osascriptBackend
}

// userNotificationsBackend delegates to the cgo body in
// userns_darwin.go (which posts via UserNotifications.framework and
// returns nothing — best-effort, errors swallowed at the framework
// boundary because callers can't act on them).
func userNotificationsBackend(n Notification) error {
	postViaUserNotifications(n.Title, n.Subtitle, n.Body)
	return nil
}

// terminalNotifierBackend shells out to terminal-notifier. We assume
// it's on $PATH because pickBackend wouldn't have chosen this branch
// otherwise. -title / -subtitle / -message map directly to the
// Notification fields.
func terminalNotifierBackend(n Notification) error {
	args := []string{"-title", n.Title}
	if n.Subtitle != "" {
		args = append(args, "-subtitle", n.Subtitle)
	}
	args = append(args, "-message", n.Body)
	return exec.Command("terminal-notifier", args...).Run()
}

// osascriptBackend uses macOS's built-in `display notification`
// AppleScript. Notifications attribute to "Script Editor" and lack
// actions, but it works without any external dependency. Last-resort
// path.
func osascriptBackend(n Notification) error {
	// `display notification "body" with title "title" subtitle "subtitle"`
	// — AppleScript double-quotes are the only escape we have to handle.
	body := escapeAppleScript(n.Body)
	title := escapeAppleScript(n.Title)
	script := fmt.Sprintf(`display notification "%s" with title "%s"`, body, title)
	if n.Subtitle != "" {
		script += fmt.Sprintf(` subtitle "%s"`, escapeAppleScript(n.Subtitle))
	}
	return exec.Command("osascript", "-e", script).Run()
}

// escapeAppleScript quotes embedded double quotes and backslashes for
// inclusion inside an AppleScript string literal. Minimal — neither
// title nor body should contain control characters in practice.
func escapeAppleScript(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\\' || c == '"' {
			out = append(out, '\\')
		}
		out = append(out, c)
	}
	return string(out)
}
