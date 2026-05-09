//go:build darwin && cgo

package keychain

/*
#cgo LDFLAGS: -framework CoreFoundation -framework Security
#include <stdlib.h>
#include <CoreFoundation/CoreFoundation.h>
#include <Security/Security.h>

// charon_self_satisfies_requirement checks whether the running binary
// satisfies the given codesign requirement string, e.g.
// `identifier "com.charon.cli"`. Returns 1 if it does, 0 otherwise.
//
// Self-contained in C so we don't have to thread CoreFoundation
// lifecycle (CFRelease, NULL-checks) across cgo boundaries.
static int charon_self_satisfies_requirement(const char *requirement_str) {
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

// charonCodesignIdentifier is the codesign --identifier value that
// `make install` stamps into the signed binary. Must stay in sync with
// CODESIGN_IDENTIFIER in Makefile.local. The runtime self-check uses
// this to distinguish a charon binary signed by `make install` from
// the linker-signed binary that `go build` and `go run` emit by default
// on Apple Silicon (which would otherwise pass a generic "is this
// binary signed?" test).
const charonCodesignIdentifier = "com.charon.cli"

func init() {
	signatureCheck = selfSignatureValid
}

// selfSignatureValid returns true if the running binary's code
// signature satisfies the codesign requirement
// `identifier "<charonCodesignIdentifier>"` — i.e., it was signed via
// `make install` (which stamps that identifier) rather than linker-signed
// or ad-hoc-signed.
//
// This is purposefully not a strict identity check. It only differentiates
// "signed by `make install`" from "anything else" so we can pick the
// keychain service-name namespace (ServiceProd vs ServiceDev). The actual
// security boundary — "only the charon binary may read these entries" —
// is enforced by the keychain ACL in M4, which pins to the specific
// cert leaf hash and is checked by the OS on every read.
func selfSignatureValid() bool {
	pred := `identifier "` + charonCodesignIdentifier + `"`
	cs := C.CString(pred)
	defer C.free(unsafe.Pointer(cs))
	return C.charon_self_satisfies_requirement(cs) != 0
}
