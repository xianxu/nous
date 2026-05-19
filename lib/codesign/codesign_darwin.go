//go:build darwin && cgo

package codesign

/*
#cgo LDFLAGS: -framework CoreFoundation -framework Security
#include <stdlib.h>
#include <CoreFoundation/CoreFoundation.h>
#include <Security/Security.h>

// nous_self_satisfies_requirement checks whether the running binary
// satisfies the given codesign requirement string, e.g.
// `identifier "com.charon.cli"`. Returns 1 if it does, 0 otherwise.
//
// Self-contained in C so we don't have to thread CoreFoundation
// lifecycle (CFRelease, NULL-checks) across cgo boundaries.
static int nous_self_satisfies_requirement(const char *requirement_str) {
    CFStringRef cfReq = CFStringCreateWithCString(NULL, requirement_str, kCFStringEncodingUTF8);
    if (cfReq == NULL) return 0;

    SecRequirementRef req = NULL;
    OSStatus rc = SecRequirementCreateWithString(cfReq, kSecCSDefaultFlags, &req);
    CFRelease(cfReq);
    if (rc != 0 || req == NULL) return 0;

    SecCodeRef code = NULL;
    rc = SecCodeCopySelf(kSecCSDefaultFlags, &code);
    if (rc != 0) {
        CFRelease(req);
        return 0;
    }

    rc = SecCodeCheckValidity(code, kSecCSDefaultFlags, req);
    CFRelease(code);
    CFRelease(req);
    return (rc == 0) ? 1 : 0;
}
*/
import "C"

import "unsafe"

// signedIdentifier is the codesign --identifier value that
// `make nous-install` stamps into the signed binary. Must stay in sync
// with the identifier in scripts/sign.sh (currently "com.charon.cli",
// preserved through nous#20's unification from when charon was its own
// binary). The runtime self-check uses this to distinguish a binary
// signed by `make nous-install` from the linker-signed binary that
// `go build` / `go run` emits by default on Apple Silicon (which
// would otherwise pass a generic "is this binary signed?" test).
const signedIdentifier = "com.charon.cli"

func init() {
	Check = selfSignatureValid
}

// selfSignatureValid returns true if the running binary's code
// signature satisfies `identifier "<signedIdentifier>"` — i.e., it
// was signed via `make nous-install` (which stamps that identifier)
// rather than linker-signed or ad-hoc.
//
// Purposefully not a strict identity check. It only differentiates
// "signed by make nous-install" from "anything else." The actual
// security boundary — "only the nous binary may read these keychain
// entries" — is enforced by the keychain ACL, which pins to the
// specific cert leaf hash and is checked by the OS on every read.
func selfSignatureValid() bool {
	pred := `identifier "` + signedIdentifier + `"`
	cs := C.CString(pred)
	defer C.free(unsafe.Pointer(cs))
	return C.nous_self_satisfies_requirement(cs) != 0
}
