package keychain

import "github.com/xianxu/nous/lib/codesign"

// Keychain service-name namespaces.
//
// A signed nous binary stores credentials under ServiceProd. An
// unsigned dev binary (`make build`, `go test`) stores under ServiceDev,
// so it doesn't collide with the signed install's ACL'd entries and
// doesn't trip Allow/Deny prompts during iteration.
//
// The split is invisible to callers: ResolveServiceName() picks the
// right one by inspecting the running binary's signing state via
// lib/codesign. No env vars, no flags — the binary adapts to its own
// state, and `make build` vs `make nous-install` differ only in
// whether codesign runs.
const (
	ServiceProd = "charon"
	ServiceDev  = "charon-dev"
)

// ResolveServiceName picks the keychain service namespace for this
// process. Cheap (a single Security framework call on darwin+cgo, a
// no-op closure elsewhere); callers may invoke it freely. Backends
// snapshot the result at construction time.
func ResolveServiceName() string {
	if codesign.IsSigned() {
		return ServiceProd
	}
	return ServiceDev
}
