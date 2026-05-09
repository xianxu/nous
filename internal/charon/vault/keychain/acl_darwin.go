//go:build darwin && cgo

package keychain

/*
#cgo CFLAGS: -Wno-deprecated-declarations
#cgo LDFLAGS: -framework CoreFoundation -framework Security
#include <stdlib.h>
#include <CoreFoundation/CoreFoundation.h>
#include <Security/Security.h>

// charon_set_generic_password upserts a generic-password keychain item.
//
// Uses the legacy file-based keychain APIs throughout:
//   - SecKeychainFindGenericPassword to locate an existing entry,
//   - SecKeychainItemModifyContent to update an existing entry's data
//     atomically (preserving its ACL — important for token rotation),
//   - SecKeychainAddGenericPassword + SecKeychainItemSetAccess to create
//     a new entry with an SecAccess pinned to the current process's
//     designated requirement.
//
// Why legacy APIs and not modern SecItemAdd: SecItemAdd on macOS file-
// based keychains accepts kSecAttrAccess in its attribute dictionary
// without returning an error, but in practice does NOT attach the
// SecAccess to the resulting item — verified empirically: entries
// written via SecItemAdd had no Access attribute when inspected via
// `security find-generic-password`. The legacy SecKeychainItemSetAccess
// reliably attaches the SecAccess and is the path used by
// `security add-generic-password -T`.
//
// SecTrustedApplicationCreateFromPath / SecAccessCreate / Sec*Keychain*
// family APIs are formally deprecated since macOS 10.10 but remain
// functional for the file-based keychain (login.keychain-db). Modern
// replacements (SecAccessControlCreateWithFlags) are for iOS-style
// biometric-gated access, not codesign-DR-based ACLs.
//
// If with_acl is 0, the entry is created with the default SecAccess
// from SecKeychainAddGenericPassword (the writing process is trusted;
// everything else prompts). Equivalent to ServiceDev semantics.
//
// Returns 0 (errSecSuccess) on success, non-zero OSStatus on failure.
//
// Memory: the function owns and releases all CF/SecKeychain objects it
// creates; the caller-passed C strings remain owned by Go.
static OSStatus charon_set_generic_password(
    const char *service,
    const char *account,
    const void *data,
    long data_len,
    int with_acl
) {
    OSStatus rc = errSecSuccess;
    UInt32 serviceLen = (UInt32)strlen(service);
    UInt32 accountLen = (UInt32)strlen(account);
    SecKeychainItemRef item = NULL;

    // Step 1: try to find an existing entry.
    rc = SecKeychainFindGenericPassword(
        NULL,
        serviceLen, service,
        accountLen, account,
        NULL, NULL,
        &item);

    if (rc == errSecSuccess) {
        // Update in place. Preserves the existing SecAccess — token
        // rotation doesn't reset the ACL.
        //
        // Other errors (notably errSecAuthFailed, when an existing entry
        // has an ACL pinned to a different designated requirement than
        // the current process) propagate to the caller intentionally —
        // we don't silently overwrite an entry someone else owns.
        // Operator workaround:
        //   security delete-generic-password -s <service> -a <account>
        rc = SecKeychainItemModifyContent(item, NULL, (UInt32)data_len, data);
        CFRelease(item);
        return rc;
    }

    if (rc != errSecItemNotFound) {
        // Find failed for some non-not-found reason (auth, malformed,
        // etc.). Surface it.
        return rc;
    }

    // Step 2: not found — create new with optional ACL.
    SecAccessRef access = NULL;
    if (with_acl) {
        // SecTrustedApplicationCreateFromPath(NULL, ...) represents the
        // current process; the SecAccess stores its designated requirement
        // (not its path), so the ACL evaluates by-DR for future reads
        // including reinstalls of the same DR-matching binary.
        SecTrustedApplicationRef self = NULL;
        rc = SecTrustedApplicationCreateFromPath(NULL, &self);
        if (rc != errSecSuccess) return rc;

        CFArrayRef trustList = CFArrayCreate(
            NULL, (const void **)&self, 1, &kCFTypeArrayCallBacks);
        rc = SecAccessCreate(CFSTR("charon"), trustList, &access);
        CFRelease(self);
        CFRelease(trustList);
        if (rc != errSecSuccess) return rc;
    }

    // Build attribute list for SecKeychainItemCreateFromContent — atomic
    // create-with-access, so we never call SecKeychainItemSetAccess on
    // an existing item. SetAccess triggers the owner-ACL auth prompt
    // (a fresh SecAccess from SecAccessCreate has an empty owner ACL =
    // "always prompt"), which non-interactive contexts can't satisfy
    // and which surfaces as errSecUserCanceledAuthentication (-128).
    SecKeychainAttribute attrs[] = {
        { kSecServiceItemAttr, serviceLen, (void *)service },
        { kSecAccountItemAttr, accountLen, (void *)account },
    };
    SecKeychainAttributeList attrList = { 2, attrs };

    rc = SecKeychainItemCreateFromContent(
        kSecGenericPasswordItemClass,
        &attrList,
        (UInt32)data_len, data,
        NULL,     // default keychain
        access,   // NULL when with_acl=0 → default access
        &item);

    if (access) CFRelease(access);
    if (rc == errSecSuccess) CFRelease(item);
    return rc;
}

// charon_inspect_generic_password reports ACL signals about an
// existing keychain item:
//   *out_acl_count — number of ACL entries on the item's SecAccess
//   *out_app_count — total trusted applications across all ACLs
//   out_drs (optional, NULL to skip) — newline-separated DR strings
//                  for each trusted app, deduplicated. Caller must
//                  free() the returned buffer.
//
// Returns 0 on success, errSecItemNotFound if no such item, or a
// non-zero OSStatus on failure.
//
// "No ACL attached" is reported as out_acl_count=0, out_app_count=0
// — distinguishable from "default ACL trusts only writer" (acl_count>0,
// app_count=1) and "ACL with multiple trusted apps" (app_count>1).
//
// Mirrors the DR-extraction logic in charon_inspect_key_acl_by_label
// (signing-key path); the SecACLCopyContents → SecACL contents →
// SecTrustedApplicationCopyData → SecRequirement decode chain is
// identical between the two item classes.
static OSStatus charon_inspect_generic_password(
    const char *service,
    const char *account,
    int *out_acl_count,
    int *out_app_count,
    char **out_drs
) {
    *out_acl_count = 0;
    *out_app_count = 0;

    SecKeychainItemRef item = NULL;
    OSStatus rc = SecKeychainFindGenericPassword(
        NULL,
        (UInt32)strlen(service), service,
        (UInt32)strlen(account), account,
        NULL, NULL,
        &item);
    if (rc != errSecSuccess) return rc;

    SecAccessRef access = NULL;
    rc = SecKeychainItemCopyAccess(item, &access);
    CFRelease(item);
    if (rc != errSecSuccess) return rc;

    CFArrayRef aclList = NULL;
    rc = SecAccessCopyACLList(access, &aclList);
    CFRelease(access);
    if (rc != errSecSuccess || aclList == NULL) return rc;

    *out_acl_count = (int)CFArrayGetCount(aclList);
    int app_total = 0;
    CFMutableStringRef drBuf = (out_drs != NULL) ? CFStringCreateMutable(NULL, 0) : NULL;
    CFMutableSetRef seenDRs = (out_drs != NULL) ? CFSetCreateMutable(NULL, 0, &kCFTypeSetCallBacks) : NULL;

    for (CFIndex i = 0; i < CFArrayGetCount(aclList); i++) {
        SecACLRef acl = (SecACLRef)CFArrayGetValueAtIndex(aclList, i);
        CFArrayRef apps = NULL;
        CFStringRef desc = NULL;
        SecKeychainPromptSelector ps = 0;
        OSStatus subrc = SecACLCopyContents(acl, &apps, &desc, &ps);
        if (subrc != errSecSuccess) continue;
        if (apps != NULL) {
            app_total += (int)CFArrayGetCount(apps);
            if (drBuf != NULL) {
                for (CFIndex j = 0; j < CFArrayGetCount(apps); j++) {
                    SecTrustedApplicationRef appRef = (SecTrustedApplicationRef)CFArrayGetValueAtIndex(apps, j);
                    CFDataRef appData = NULL;
                    if (SecTrustedApplicationCopyData(appRef, &appData) != errSecSuccess || appData == NULL) {
                        continue;
                    }
                    CFStringRef appDesc = NULL;
                    SecRequirementRef req = NULL;
                    if (SecRequirementCreateWithData(appData, kSecCSDefaultFlags, &req) == errSecSuccess && req != NULL) {
                        SecRequirementCopyString(req, kSecCSDefaultFlags, &appDesc);
                        CFRelease(req);
                    }
                    if (appDesc == NULL) {
                        const UInt8 *bytes = CFDataGetBytePtr(appData);
                        CFIndex len = CFDataGetLength(appData);
                        while (len > 0 && bytes[len-1] == 0) len--;
                        if (len > 0) {
                            CFStringRef path = CFStringCreateWithBytes(
                                NULL, bytes, len, kCFStringEncodingUTF8, false);
                            if (path != NULL) {
                                appDesc = CFStringCreateWithFormat(
                                    NULL, NULL, CFSTR("identifier \"%@\""), path);
                                CFRelease(path);
                            }
                        }
                    }
                    if (appDesc != NULL) {
                        if (!CFSetContainsValue(seenDRs, appDesc)) {
                            CFSetAddValue(seenDRs, appDesc);
                            if (CFStringGetLength(drBuf) > 0) {
                                CFStringAppendCString(drBuf, "\n", kCFStringEncodingUTF8);
                            }
                            CFStringAppend(drBuf, appDesc);
                        }
                        CFRelease(appDesc);
                    }
                    CFRelease(appData);
                }
            }
            CFRelease(apps);
        }
        if (desc != NULL) CFRelease(desc);
    }
    *out_app_count = app_total;

    if (out_drs != NULL && drBuf != NULL) {
        CFIndex bufSize = CFStringGetMaximumSizeForEncoding(CFStringGetLength(drBuf), kCFStringEncodingUTF8) + 1;
        char *cstr = malloc(bufSize);
        if (cstr != NULL) {
            if (CFStringGetCString(drBuf, cstr, bufSize, kCFStringEncodingUTF8)) {
                *out_drs = cstr;
            } else {
                free(cstr);
                *out_drs = NULL;
            }
        }
        CFRelease(drBuf);
    }
    if (seenDRs != NULL) CFRelease(seenDRs);

    CFRelease(aclList);
    return errSecSuccess;
}

// charon_inspect_key_acl_by_label looks up a code-signing identity
// by its CERTIFICATE label (the string shown by `security
// find-identity`, e.g. "Charon Self-Signed" or "Developer ID
// Application: <Name> (<TEAMID>)"), pulls the matching private key
// out of the identity, and reports its ACL signals. Used by the
// security audit tool to verify the signing-key trusted-apps list
// is empty (the property defense layer 5 / A10 hinges on).
//
// out_drs is filled with a newline-separated list of Designated
// Requirement strings — one per trusted application across all
// ACLs, deduplicated. Caller must free() it. Pass NULL to skip
// extraction (count-only mode).
//
// The cert label and the private-key label often differ — Apple's
// Certificate Assistant labels the key with the CSR's Common Name
// while the cert may show "Developer ID Application: ...". Looking
// up by cert label and following SecIdentityCopyPrivateKey gives us
// a stable hook regardless of how the key was labeled at creation.
//
// Returns errSecItemNotFound when no identity matches the label.
//
// Reading the ACL is metadata-only; does NOT trigger key-use
// authentication, so this function does NOT prompt the user.
static OSStatus charon_inspect_key_acl_by_label(
    const char *label,
    int *out_acl_count,
    int *out_app_count,
    char **out_drs
) {
    *out_acl_count = 0;
    *out_app_count = 0;

    CFStringRef cfLabel = CFStringCreateWithCString(NULL, label, kCFStringEncodingUTF8);
    if (cfLabel == NULL) return errSecAllocate;

    // Walk all identities and match by certificate Common Name. The
    // cert-attribute kSecAttrLabel is sometimes set to the CN, but
    // not always — the bootstrap script for Charon Self-Signed uses
    // SecCertificateCopyCommonName via openssl-imported certs, while
    // Apple's Certificate Assistant for Dev ID labels both fields.
    // CN is the consistent identifier across both.
    CFMutableDictionaryRef query = CFDictionaryCreateMutable(
        NULL, 0, &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
    CFDictionarySetValue(query, kSecClass, kSecClassIdentity);
    CFDictionarySetValue(query, kSecReturnRef, kCFBooleanTrue);
    CFDictionarySetValue(query, kSecMatchLimit, kSecMatchLimitAll);

    CFTypeRef result = NULL;
    OSStatus rc = SecItemCopyMatching(query, &result);
    CFRelease(query);
    if (rc != errSecSuccess) {
        CFRelease(cfLabel);
        return rc;
    }

    CFArrayRef identities = (CFArrayRef)result;
    SecIdentityRef matched = NULL;
    for (CFIndex i = 0; i < CFArrayGetCount(identities); i++) {
        SecIdentityRef identity = (SecIdentityRef)CFArrayGetValueAtIndex(identities, i);
        SecCertificateRef cert = NULL;
        if (SecIdentityCopyCertificate(identity, &cert) != errSecSuccess) continue;
        CFStringRef cn = NULL;
        if (SecCertificateCopyCommonName(cert, &cn) == errSecSuccess && cn != NULL) {
            if (CFStringCompare(cn, cfLabel, 0) == kCFCompareEqualTo) {
                CFRetain(identity);
                matched = identity;
                CFRelease(cn);
                CFRelease(cert);
                break;
            }
            CFRelease(cn);
        }
        CFRelease(cert);
    }
    CFRelease(identities);
    CFRelease(cfLabel);

    if (matched == NULL) return errSecItemNotFound;

    SecKeyRef privateKey = NULL;
    rc = SecIdentityCopyPrivateKey(matched, &privateKey);
    CFRelease(matched);
    if (rc != errSecSuccess) return rc;

    // Toll-free bridge: legacy private keys (those imported via the
    // Certificate Assistant or our openssl-bootstrap path) ARE
    // SecKeychainItem-compatible and SecKeychainItemCopyAccess works
    // on them.
    SecAccessRef access = NULL;
    rc = SecKeychainItemCopyAccess((SecKeychainItemRef)privateKey, &access);
    CFRelease(privateKey);
    if (rc != errSecSuccess) return rc;

    CFArrayRef aclList = NULL;
    rc = SecAccessCopyACLList(access, &aclList);
    CFRelease(access);
    if (rc != errSecSuccess || aclList == NULL) return rc;

    *out_acl_count = (int)CFArrayGetCount(aclList);
    int app_total = 0;
    CFMutableStringRef drBuf = (out_drs != NULL) ? CFStringCreateMutable(NULL, 0) : NULL;
    CFMutableSetRef seenDRs = (out_drs != NULL) ? CFSetCreateMutable(NULL, 0, &kCFTypeSetCallBacks) : NULL;

    for (CFIndex i = 0; i < CFArrayGetCount(aclList); i++) {
        SecACLRef acl = (SecACLRef)CFArrayGetValueAtIndex(aclList, i);
        CFArrayRef apps = NULL;
        CFStringRef desc = NULL;
        SecKeychainPromptSelector ps = 0;
        OSStatus subrc = SecACLCopyContents(acl, &apps, &desc, &ps);
        if (subrc != errSecSuccess) continue;
        if (apps != NULL) {
            app_total += (int)CFArrayGetCount(apps);
            if (drBuf != NULL) {
                for (CFIndex j = 0; j < CFArrayGetCount(apps); j++) {
                    SecTrustedApplicationRef appRef = (SecTrustedApplicationRef)CFArrayGetValueAtIndex(apps, j);
                    // Apple doesn't expose a SecTrustedApplicationCopyRequirement
                    // in the public API. The supported path is to pull the
                    // application's serialized data and try two interpretations:
                    //   1. SecRequirementCreateWithData → DR string (newer apps,
                    //      created from-requirement)
                    //   2. UTF-8 path bytes (legacy apps, created from path)
                    // Whichever decodes wins; we emit one line per trusted app.
                    CFDataRef appData = NULL;
                    if (SecTrustedApplicationCopyData(appRef, &appData) != errSecSuccess || appData == NULL) {
                        continue;
                    }
                    CFStringRef appDesc = NULL;

                    // Path #1: try as SecRequirement.
                    SecRequirementRef req = NULL;
                    if (SecRequirementCreateWithData(appData, kSecCSDefaultFlags, &req) == errSecSuccess && req != NULL) {
                        SecRequirementCopyString(req, kSecCSDefaultFlags, &appDesc);
                        CFRelease(req);
                    }

                    // Path #2: treat as a UTF-8 path. Trim trailing NUL.
                    if (appDesc == NULL) {
                        const UInt8 *bytes = CFDataGetBytePtr(appData);
                        CFIndex len = CFDataGetLength(appData);
                        while (len > 0 && bytes[len-1] == 0) len--;
                        if (len > 0) {
                            CFStringRef path = CFStringCreateWithBytes(
                                NULL, bytes, len, kCFStringEncodingUTF8, false);
                            if (path != NULL) {
                                // Wrap in a path-shaped synthetic DR so the
                                // Go-side classifier sees a uniform format.
                                appDesc = CFStringCreateWithFormat(
                                    NULL, NULL, CFSTR("identifier \"%@\""), path);
                                CFRelease(path);
                            }
                        }
                    }

                    if (appDesc != NULL) {
                        if (!CFSetContainsValue(seenDRs, appDesc)) {
                            CFSetAddValue(seenDRs, appDesc);
                            if (CFStringGetLength(drBuf) > 0) {
                                CFStringAppendCString(drBuf, "\n", kCFStringEncodingUTF8);
                            }
                            CFStringAppend(drBuf, appDesc);
                        }
                        CFRelease(appDesc);
                    }
                    CFRelease(appData);
                }
            }
            CFRelease(apps);
        }
        if (desc != NULL) CFRelease(desc);
    }
    *out_app_count = app_total;

    if (out_drs != NULL && drBuf != NULL) {
        CFIndex bufSize = CFStringGetMaximumSizeForEncoding(CFStringGetLength(drBuf), kCFStringEncodingUTF8) + 1;
        char *cstr = malloc(bufSize);
        if (cstr != NULL) {
            if (CFStringGetCString(drBuf, cstr, bufSize, kCFStringEncodingUTF8)) {
                *out_drs = cstr;
            } else {
                free(cstr);
                *out_drs = NULL;
            }
        }
        CFRelease(drBuf);
    }
    if (seenDRs != NULL) CFRelease(seenDRs);

    CFRelease(aclList);
    return errSecSuccess;
}

// charon_delete_generic_password deletes a generic-password keychain
// item by service+account.
//
// Tries SecItemDelete (modern API) first. If it returns
// errSecInvalidOwnerEdit (-25244) — observed when an item's internal
// access object is owned by a different process than the caller, even
// for items with no explicit ACL — falls back to the legacy
// SecKeychainFindGenericPassword + SecKeychainItemDelete pair, which
// uses the file-based keychain code path that the `security` CLI uses
// and doesn't trip the same access-modification check.
//
// Returns 0 on success, errSecItemNotFound when no entry exists (caller
// treats this as idempotent success), or another OSStatus on failure.
static OSStatus charon_delete_generic_password(
    const char *service,
    const char *account
) {
    OSStatus rc = errSecSuccess;
    CFStringRef cfService = NULL;
    CFStringRef cfAccount = NULL;

    cfService = CFStringCreateWithCString(NULL, service, kCFStringEncodingUTF8);
    cfAccount = CFStringCreateWithCString(NULL, account, kCFStringEncodingUTF8);
    if (cfService == NULL || cfAccount == NULL) {
        rc = errSecAllocate;
        goto cleanup_delete;
    }

    // Try modern SecItemDelete.
    {
        const void *qkeys[] = { kSecClass,                kSecAttrService, kSecAttrAccount, kSecMatchLimit    };
        const void *qvals[] = { kSecClassGenericPassword, cfService,       cfAccount,       kSecMatchLimitOne };
        CFDictionaryRef query = CFDictionaryCreate(
            NULL, qkeys, qvals, 4,
            &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
        rc = SecItemDelete(query);
        CFRelease(query);

        if (rc != errSecInvalidOwnerEdit) goto cleanup_delete;
        // -25244: fall through to the legacy path.
    }

    // Legacy fallback. NULL keychainOrArray means "search the default
    // keychain search list" (typically just login.keychain-db). NULL
    // password out-params mean "I just want the itemRef, don't return
    // the password data."
    {
        SecKeychainItemRef itemRef = NULL;
        rc = SecKeychainFindGenericPassword(
            NULL,
            (UInt32)strlen(service), service,
            (UInt32)strlen(account), account,
            NULL, NULL,
            &itemRef);
        if (rc != errSecSuccess) goto cleanup_delete;

        rc = SecKeychainItemDelete(itemRef);
        CFRelease(itemRef);
    }

cleanup_delete:
    if (cfService) CFRelease(cfService);
    if (cfAccount) CFRelease(cfAccount);
    return rc;
}
*/
import "C"

import (
	"errors"
	"fmt"
	"strings"
	"unsafe"
)

// errSecItemNotFound is the macOS Security framework "no such item"
// sentinel. Exposed as a Go error so the Store.Delete contract can
// remain idempotent without callers reaching into OSStatus codes.
const cErrSecItemNotFound = -25300

// InspectACL returns ACL counts for a charon-namespaced entry. See
// InspectACLDetailed for the variant that also returns each trusted
// application's DR string.
func (s *Store) InspectACL(account string) (aclCount, appCount int, err error) {
	ac, app, _, err := s.InspectACLDetailed(account)
	return ac, app, err
}

// InspectACLDetailed extends InspectACL with the per-trusted-app
// Designated Requirement strings. Strings are deduplicated.
func (s *Store) InspectACLDetailed(account string) (aclCount, appCount int, drs []string, err error) {
	return inspectGenericPasswordACLDetailed(s.service, account)
}

// inspectGenericPasswordACL returns ACL signals for an existing
// keychain item. Used by integration tests to verify our SecAccess
// actually attached. Production code doesn't need it.
//
// aclCount = number of ACL entries on the SecAccess.
// appCount = total trusted applications across those ACLs.
//
//	(0, 0)   → no SecAccess attached at all
//	(>0, 0)  → SecAccess present but no trusted apps (always-prompt mode)
//	(>0, 1)  → typical default: only one trusted app (the writer)
//	(>0, N)  → multiple trusted apps
func inspectGenericPasswordACL(service, account string) (aclCount, appCount int, err error) {
	ac, app, _, err := inspectGenericPasswordACLDetailed(service, account)
	return ac, app, err
}

func inspectGenericPasswordACLDetailed(service, account string) (aclCount, appCount int, drs []string, err error) {
	cService := C.CString(service)
	defer C.free(unsafe.Pointer(cService))
	cAccount := C.CString(account)
	defer C.free(unsafe.Pointer(cAccount))

	var ac, app C.int
	var drsPtr *C.char
	rc := C.charon_inspect_generic_password(cService, cAccount, &ac, &app, &drsPtr)
	if rc != 0 {
		return 0, 0, nil, fmt.Errorf("inspect %s/%s: OSStatus %d", service, account, int(rc))
	}
	if drsPtr != nil {
		s := C.GoString(drsPtr)
		C.free(unsafe.Pointer(drsPtr))
		if s != "" {
			drs = strings.Split(s, "\n")
		}
	}
	return int(ac), int(app), drs, nil
}

// ErrSigningKeyNotFound is returned by InspectSigningKeyACL when no
// private key with the given label exists in the user's login
// keychain. Distinguishes "key absent" from "key present but ACL
// inspection failed" so callers can ignore expected absence (e.g.
// when checking both a Charon Self-Signed and a Developer ID
// identity but only one of the two exists on this machine).
var ErrSigningKeyNotFound = errors.New("signing key not found")

// InspectSigningKeyACL looks up a private-key item by its certificate
// label and returns ACL signals. See InspectSigningKeyACLDetailed for
// the variant that also returns each trusted application's DR text.
func InspectSigningKeyACL(label string) (aclCount, appCount int, err error) {
	ac, app, _, err := InspectSigningKeyACLDetailed(label)
	return ac, app, err
}

// InspectSigningKeyACLDetailed extends InspectSigningKeyACL with the
// per-trusted-application Designated Requirement strings — the actual
// codesign predicates the kernel evaluates. Strings are deduplicated
// (one per unique DR even if it appears in multiple ACLs).
//
// Returns ErrSigningKeyNotFound when no key matches the label.
func InspectSigningKeyACLDetailed(label string) (aclCount, appCount int, drs []string, err error) {
	cLabel := C.CString(label)
	defer C.free(unsafe.Pointer(cLabel))

	var ac, app C.int
	var drsPtr *C.char
	rc := C.charon_inspect_key_acl_by_label(cLabel, &ac, &app, &drsPtr)
	const errSecItemNotFound = -25300
	if rc == errSecItemNotFound {
		return 0, 0, nil, ErrSigningKeyNotFound
	}
	if rc != 0 {
		return 0, 0, nil, fmt.Errorf("inspect signing key %q: OSStatus %d", label, int(rc))
	}
	if drsPtr != nil {
		s := C.GoString(drsPtr)
		C.free(unsafe.Pointer(drsPtr))
		if s != "" {
			drs = strings.Split(s, "\n")
		}
	}
	return int(ac), int(app), drs, nil
}

// deleteGenericPassword removes a generic-password keychain item.
// Treats "not found" as success (Delete is idempotent).
//
// Used by Store.Delete on darwin+cgo. Calls into a CGo helper that
// tries the modern SecItemDelete first and falls back to the legacy
// file-based keychain API on errSecInvalidOwnerEdit (-25244) — the
// modern API surfaces -25244 for items whose internal access object
// is owned by another process even when no explicit ACL is set, and
// the legacy path doesn't have that hazard.
func deleteGenericPassword(service, account string) error {
	cService := C.CString(service)
	defer C.free(unsafe.Pointer(cService))
	cAccount := C.CString(account)
	defer C.free(unsafe.Pointer(cAccount))

	rc := C.charon_delete_generic_password(cService, cAccount)
	switch rc {
	case 0, cErrSecItemNotFound:
		return nil
	default:
		return fmt.Errorf("keychain Delete %s/%s: OSStatus %d", service, account, int(rc))
	}
}

// setGenericPassword upserts a generic-password keychain item under
// `service` + `account`, atomic via SecItemUpdate when the entry
// already exists. New entries written with `withACL=true` get an ACL
// bound to the current process's designated requirement; readers with
// a different signature trigger the macOS "Allow/Deny" dialog.
//
// Used by Store.Set and SetRaw on darwin+cgo. Both paths route through
// this; ACL is gated by the caller (typically: ACL for ServiceProd,
// no ACL for ServiceDev).
func setGenericPassword(service, account string, data []byte, withACL bool) error {
	cService := C.CString(service)
	defer C.free(unsafe.Pointer(cService))
	cAccount := C.CString(account)
	defer C.free(unsafe.Pointer(cAccount))

	var dataPtr unsafe.Pointer
	var dataLen C.long
	if len(data) > 0 {
		dataPtr = unsafe.Pointer(&data[0])
		dataLen = C.long(len(data))
	}

	withAclC := C.int(0)
	if withACL {
		withAclC = 1
	}

	rc := C.charon_set_generic_password(cService, cAccount, dataPtr, dataLen, withAclC)
	if rc != 0 {
		return fmt.Errorf("keychain Set %s/%s: OSStatus %d", service, account, int(rc))
	}
	return nil
}
