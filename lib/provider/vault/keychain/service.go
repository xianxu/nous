package keychain

// Keychain service-name namespaces.
//
// A signed charon binary stores credentials under ServiceProd. An
// unsigned dev binary (`make build`, `go test`) stores under ServiceDev,
// so it doesn't collide with the signed install's ACL'd entries and
// doesn't trip Allow/Deny prompts during iteration.
//
// The split is invisible to callers: ResolveServiceName() picks the
// right one by inspecting the running binary's signing state. No env
// vars, no flags — the binary adapts to its own state, and `make build`
// vs `make install` differ only in whether codesign runs.
const (
	ServiceProd = "charon"
	ServiceDev  = "charon-dev"
)

// signatureCheck reports whether the running binary is code-signed and
// satisfies its own designated requirement. The default returns false
// (unsigned, or non-darwin / CGO_ENABLED=0); the darwin+cgo init in
// codesign_darwin.go overrides this with a real Security framework
// check.
//
// Tests override this directly to verify routing without depending on
// the test binary's signing state. Tests that override signatureCheck
// MUST NOT call t.Parallel() — there's no mutex around this var; in
// production it's written twice during init (here + the darwin init)
// before any goroutine reads it, but parallel tests would race.
var signatureCheck = func() bool { return false }

// ResolveServiceName picks the keychain service namespace for this
// process. Cheap (a single Security framework call on darwin+cgo, a
// no-op closure elsewhere); callers may invoke it freely. Backends
// snapshot the result at construction time.
func ResolveServiceName() string {
	if signatureCheck() {
		return ServiceProd
	}
	return ServiceDev
}
