//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c -fobjc-arc -Wno-deprecated-declarations
#cgo LDFLAGS: -framework Foundation -framework UserNotifications

#import <Foundation/Foundation.h>
#import <UserNotifications/UserNotifications.h>

// charon_has_bundle returns 1 when this process is running inside an
// .app bundle (Charon Security.app), 0 when invoked as a bare
// binary. UserNotifications.framework requires a bundle context —
// posting from a bare binary throws or silently no-ops depending on
// macOS version. The Go layer uses this to fall back to osascript
// during dev iteration.
static int charon_has_bundle(void) {
    NSString *bundleID = [[NSBundle mainBundle] bundleIdentifier];
    return (bundleID != nil) ? 1 : 0;
}

// charon_request_notification_auth pops the system "[App] would like
// to send notifications" prompt the first time it's called for a
// given bundle id. After the user answers, the choice persists in
// System Settings → Notifications → Charon Security.
//
// Idempotent: subsequent calls are no-ops if auth is already
// granted/denied. Errors (no bundle, daemon unreachable) are
// swallowed — the post path also no-ops in those cases, so the
// menubar stays usable even when notifications can't be delivered.
static void charon_request_notification_auth(void) {
    UNUserNotificationCenter *center = [UNUserNotificationCenter currentNotificationCenter];
    UNAuthorizationOptions options = UNAuthorizationOptionAlert
                                   | UNAuthorizationOptionSound;
    [center requestAuthorizationWithOptions:options
                          completionHandler:^(BOOL granted, NSError * _Nullable error) {
        // No-op. If denied or errored, charon_post_notification
        // will silently no-op too.
        (void)granted;
        (void)error;
    }];
}

// charon_post_notification adds a notification request to the user's
// notification center. The trigger is nil → delivered immediately.
//
// Identity: the notification is attributed to the bundle that owns
// this process (Charon Security via CFBundleIdentifier=com.charon.security).
// The user's Alert/Banner choice in System Settings is scoped to that
// bundle, so making notifications stay (Alert style) is now a
// configurable preference rather than a global Script-Editor toggle.
//
// Sound: default banner sound — present so the notification is
// noticeable even when banner style auto-dismisses.
static void charon_post_notification(const char *title_cstr, const char *body_cstr) {
    NSString *title = [NSString stringWithUTF8String:title_cstr];
    NSString *body  = [NSString stringWithUTF8String:body_cstr];

    UNMutableNotificationContent *content = [[UNMutableNotificationContent alloc] init];
    content.title = title;
    content.body  = body;
    content.sound = [UNNotificationSound defaultSound];

    // Per-call unique identifier so re-posting doesn't replace an
    // earlier banner that the user might still be reading.
    NSString *uid = [[NSUUID UUID] UUIDString];
    UNNotificationRequest *req = [UNNotificationRequest
        requestWithIdentifier:uid
                      content:content
                      trigger:nil];

    UNUserNotificationCenter *center = [UNUserNotificationCenter currentNotificationCenter];
    [center addNotificationRequest:req
             withCompletionHandler:^(NSError * _Nullable error) {
        // Best-effort; the menubar stays usable even on delivery failure.
        (void)error;
    }];
}
*/
import "C"

import "unsafe"

// hasBundle reports whether this process is running inside an .app
// bundle. False when invoked as `./bin/charon-security menubar`
// directly during dev iteration.
func hasBundle() bool {
	return C.charon_has_bundle() != 0
}

// requestNotificationAuth pops the system permission prompt (first
// call only). Cheap to call repeatedly — subsequent calls are
// no-ops. Should be invoked once at menubar startup so the prompt
// appears at a sensible moment.
func requestNotificationAuth() {
	C.charon_request_notification_auth()
}

// postNativeNotification delivers a notification via UserNotifications.
// Caller is responsible for falling back to osascript when hasBundle()
// is false.
func postNativeNotification(title, body string) {
	t := C.CString(title)
	b := C.CString(body)
	defer C.free(unsafe.Pointer(t))
	defer C.free(unsafe.Pointer(b))
	C.charon_post_notification(t, b)
}
