// Package codesign provides a single primitive — "is the running
// binary signed by `make nous-install`?" — shared across surfaces that
// route behavior on signed-vs-unsigned state (the keychain service
// namespace, the notification dispatcher, future telemetry / packaging
// surfaces).
//
// The primitive is purposefully narrow: it only differentiates "signed
// by `make nous-install`" from "anything else." Real security
// boundaries (keychain ACLs that pin to a specific cert leaf hash,
// codesign requirements enforced by the OS on each operation) are
// elsewhere; this package answers the routing question, not the
// authorization question.
//
// Default `Check` returns false (unsigned, or non-darwin / CGO_ENABLED=0).
// `codesign_darwin.go`'s init overrides it with a real Security
// framework check on darwin+cgo. Tests override `Check` directly to
// exercise both branches without depending on the test binary's own
// signing state — tests that do this MUST NOT call `t.Parallel()`,
// since there's no mutex around the variable.
package codesign

// Check is the swappable backend; overridden by the darwin+cgo init
// when available. Exported so tests in any package can substitute a
// fake (the production write happens during package init, before any
// goroutine reads it).
var Check = func() bool { return false }

// IsSigned reports whether the running binary is code-signed in a way
// that satisfies `make nous-install`'s designated requirement.
// Cheap (a single Security framework call on darwin+cgo, a no-op
// closure elsewhere); callers may invoke it freely. Surfaces that
// snapshot the result at construction time (e.g. keychain Store)
// should continue to do so to avoid mid-process namespace flips.
func IsSigned() bool { return Check() }
