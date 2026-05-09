//go:build darwin && cgo

package proxy

/*
#cgo LDFLAGS: -framework CoreFoundation -framework Security
#include <stdlib.h>
#include <CoreFoundation/CoreFoundation.h>
#include <Security/Security.h>

// charon_peer_satisfies_requirement returns 1 if the process at pid
// satisfies the given codesign requirement, 0 otherwise.
//
// The requirement is constructed via SecRequirementCreateWithString
// (e.g. `identifier "com.charon.security"`). The peer's SecCodeRef
// comes from SecCodeCopyGuestWithAttributes with kSecGuestAttributePid.
//
// Race note: a TOCTOU window exists between getsockopt(LOCAL_PEEREPID)
// returning a pid and this call. Mitigation in Phase C.5 (audit-token
// path via kSecGuestAttributeAudit) — for MVP, the pid path matches
// the existing keychain-ACL approach in #000003.
//
// Self-contained in C so we don't have to manage CoreFoundation
// lifecycle (CFRelease, NULL-checks) across the cgo boundary.
static int charon_peer_satisfies_requirement(int pid, const char *requirement_str) {
    CFStringRef cfReq = CFStringCreateWithCString(NULL, requirement_str, kCFStringEncodingUTF8);
    if (cfReq == NULL) return 0;

    SecRequirementRef req = NULL;
    OSStatus rc = SecRequirementCreateWithString(cfReq, kSecCSDefaultFlags, &req);
    CFRelease(cfReq);
    if (rc != 0 || req == NULL) return 0;

    // Build the attributes dict: { kSecGuestAttributePid: <pid> }.
    int32_t p = (int32_t)pid;
    CFNumberRef pidNum = CFNumberCreate(NULL, kCFNumberSInt32Type, &p);
    if (pidNum == NULL) {
        CFRelease(req);
        return 0;
    }
    const void *keys[] = { kSecGuestAttributePid };
    const void *vals[] = { pidNum };
    CFDictionaryRef attrs = CFDictionaryCreate(NULL, keys, vals, 1,
        &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
    CFRelease(pidNum);
    if (attrs == NULL) {
        CFRelease(req);
        return 0;
    }

    SecCodeRef peer = NULL;
    rc = SecCodeCopyGuestWithAttributes(NULL, attrs, kSecCSDefaultFlags, &peer);
    CFRelease(attrs);
    if (rc != 0 || peer == NULL) {
        CFRelease(req);
        return 0;
    }

    // Strict validation: reject bundles with detached signatures,
    // tampered nested code, or resource manifests broken outside
    // the seal. kSecCSDefaultFlags accepts some of those depending
    // on macOS version; this is a security-critical trust edge so
    // the perf cost (one connect-time check) is irrelevant.
    SecCSFlags checkFlags = kSecCSStrictValidate
                          | kSecCSCheckAllArchitectures
                          | kSecCSCheckNestedCode;
    rc = SecCodeCheckValidity(peer, checkFlags, req);
    CFRelease(peer);
    CFRelease(req);
    return (rc == 0) ? 1 : 0;
}
*/
import "C"

import "unsafe"

// verifyPeerDR returns true if the process at pid satisfies the
// codesign requirement string. False on any error path (process
// gone, malformed requirement, requirement not satisfied).
//
// Used by the runtime-consent socket to gate connections from
// Charon Security.app: only a process whose binary's signature
// matches `identifier "com.charon.security"` may drive arm/disarm.
func verifyPeerDR(pid int, requirement string) bool {
	cs := C.CString(requirement)
	defer C.free(unsafe.Pointer(cs))
	return C.charon_peer_satisfies_requirement(C.int(pid), cs) != 0
}
