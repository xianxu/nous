//go:build darwin

package notify

import (
	"errors"
	"testing"

	"github.com/xianxu/nous/lib/codesign"
)

// withSig swaps codesign.Check for the test's duration. No t.Parallel()
// — codesign.Check has no mutex (per its package doc).
func withSig(t *testing.T, signed bool) {
	t.Helper()
	orig := codesign.Check
	t.Cleanup(func() { codesign.Check = orig })
	codesign.Check = func() bool { return signed }
}

// withHasBundle swaps the package-local hasBundle. Same caveat.
func withHasBundle(t *testing.T, bundled bool) {
	t.Helper()
	orig := hasBundle
	t.Cleanup(func() { hasBundle = orig })
	hasBundle = func() bool { return bundled }
}

// withTerminalNotifier swaps terminalNotifierPath. found=true means
// the lookup returns a path with no error (simulates installed);
// found=false simulates missing.
func withTerminalNotifier(t *testing.T, found bool) {
	t.Helper()
	orig := terminalNotifierPath
	t.Cleanup(func() { terminalNotifierPath = orig })
	if found {
		terminalNotifierPath = func() (string, error) { return "/opt/homebrew/bin/terminal-notifier", nil }
	} else {
		terminalNotifierPath = func() (string, error) { return "", errors.New("not found") }
	}
}

// backendID identifies which backend pickBackend returned without
// asserting on function pointer equality (which Go disallows for
// non-nil functions in a comparison-clean way — fmt.Sprint of the
// pointer would work but is fragile). Instead, we compare via a
// side channel: ask each candidate backend its identity by inspecting
// its captured behavior through a known input. Simpler: rely on the
// package-private addresses.
func backendID(b Backend) string {
	switch {
	case sameFunc(b, userNotificationsBackend):
		return "userns"
	case sameFunc(b, terminalNotifierBackend):
		return "terminal-notifier"
	case sameFunc(b, osascriptBackend):
		return "osascript"
	default:
		return "unknown"
	}
}

// sameFunc compares two Backend values by their underlying pointer
// (via reflect.ValueOf().Pointer() through an inline helper to avoid
// the reflect import bloating this small test file).
func sameFunc(a, b Backend) bool {
	return funcPtr(a) == funcPtr(b)
}

func TestPickBackend_SignedAndBundled_UsesUserNotifications(t *testing.T) {
	withSig(t, true)
	withHasBundle(t, true)
	withTerminalNotifier(t, true) // present but should not be chosen
	if got := backendID(pickBackend()); got != "userns" {
		t.Errorf("signed+bundled: got %q, want userns", got)
	}
}

func TestPickBackend_SignedNotBundled_FallsToTerminalNotifier(t *testing.T) {
	withSig(t, true)
	withHasBundle(t, false)
	withTerminalNotifier(t, true)
	if got := backendID(pickBackend()); got != "terminal-notifier" {
		t.Errorf("signed+nobundle: got %q, want terminal-notifier", got)
	}
}

func TestPickBackend_UnsignedWithTerminalNotifier(t *testing.T) {
	withSig(t, false)
	withHasBundle(t, false)
	withTerminalNotifier(t, true)
	if got := backendID(pickBackend()); got != "terminal-notifier" {
		t.Errorf("unsigned+tn: got %q, want terminal-notifier", got)
	}
}

func TestPickBackend_UnsignedWithoutTerminalNotifier_FallsToOsascript(t *testing.T) {
	withSig(t, false)
	withHasBundle(t, false)
	withTerminalNotifier(t, false)
	if got := backendID(pickBackend()); got != "osascript" {
		t.Errorf("unsigned+notn: got %q, want osascript", got)
	}
}

func TestEscapeAppleScript(t *testing.T) {
	cases := []struct{ in, want string }{
		{"hello", "hello"},
		{`with "quotes"`, `with \"quotes\"`},
		{`back\slash`, `back\\slash`},
		{`"`, `\"`},
		{``, ``},
	}
	for _, c := range cases {
		if got := escapeAppleScript(c.in); got != c.want {
			t.Errorf("escapeAppleScript(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
