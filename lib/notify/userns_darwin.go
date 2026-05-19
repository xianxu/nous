//go:build darwin && cgo

package notify

/*
#cgo CFLAGS: -x objective-c -fobjc-arc -Wno-deprecated-declarations
#cgo LDFLAGS: -framework Foundation -framework UserNotifications

#import <Foundation/Foundation.h>
#import <UserNotifications/UserNotifications.h>

// nous_has_bundle returns 1 when this process is running inside an
// .app bundle (e.g. Charon Security.app once the prod packaging
// follow-up lands), 0 when invoked as a bare binary.
// UserNotifications.framework requires a bundle context — posting
// from a bare binary throws or silently no-ops depending on macOS
// version. The Go layer uses this to choose terminal-notifier
// fallback during dev iteration.
static int nous_has_bundle(void) {
    NSString *bundleID = [[NSBundle mainBundle] bundleIdentifier];
    return (bundleID != nil) ? 1 : 0;
}

// nous_request_notification_auth pops the system "[App] would like to
// send notifications" prompt the first time it's called for a given
// bundle id. After the user answers, the choice persists in
// System Settings → Notifications → <App>.
//
// Idempotent: subsequent calls are no-ops if auth is already
// granted/denied. Errors are swallowed — the post path also no-ops
// in those cases, so callers stay usable even when notifications
// can't be delivered.
static void nous_request_notification_auth(void) {
    UNUserNotificationCenter *center = [UNUserNotificationCenter currentNotificationCenter];
    UNAuthorizationOptions options = UNAuthorizationOptionAlert
                                   | UNAuthorizationOptionSound;
    [center requestAuthorizationWithOptions:options
                          completionHandler:^(BOOL granted, NSError * _Nullable error) {
        (void)granted;
        (void)error;
    }];
}

// nous_post_notification adds a notification request to the user's
// notification center. Trigger is nil → delivered immediately.
//
// Identity: attributed to the bundle that owns this process via
// CFBundleIdentifier. The user's Alert/Banner choice in System
// Settings is scoped to that bundle.
static void nous_post_notification(const char *title_cstr, const char *subtitle_cstr, const char *body_cstr) {
    NSString *title    = [NSString stringWithUTF8String:title_cstr];
    NSString *subtitle = (subtitle_cstr && subtitle_cstr[0]) ? [NSString stringWithUTF8String:subtitle_cstr] : nil;
    NSString *body     = [NSString stringWithUTF8String:body_cstr];

    UNMutableNotificationContent *content = [[UNMutableNotificationContent alloc] init];
    content.title = title;
    if (subtitle) {
        content.subtitle = subtitle;
    }
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
        (void)error;
    }];
}
*/
import "C"

import "unsafe"

// realHasBundle reports whether this process is running inside an
// .app bundle. Default value of the swappable hasBundle var in
// backend_darwin.go.
func realHasBundle() bool {
	return C.nous_has_bundle() != 0
}

// RequestAuth pops the system permission prompt (first call only).
// Cheap to call repeatedly — subsequent calls are no-ops. Callers
// that want notifications to fire should invoke this once at startup
// (typically the menubar's init path); skipping it just means the
// first notification is silently denied until the user grants permission
// via System Settings.
//
// No-op when UserNotifications.framework isn't the active backend
// (e.g. running under terminal-notifier): the auth grant is per-bundle,
// so prompting from a bare binary is meaningless.
func RequestAuth() {
	if !realHasBundle() {
		return
	}
	C.nous_request_notification_auth()
}

// postViaUserNotifications is called by userNotificationsBackend; the
// indirection exists so the cgo build constraint stays scoped to this
// file. backend_darwin.go imports the symbol but doesn't need cgo.
func postViaUserNotifications(title, subtitle, body string) {
	t := C.CString(title)
	s := C.CString(subtitle)
	b := C.CString(body)
	defer C.free(unsafe.Pointer(t))
	defer C.free(unsafe.Pointer(s))
	defer C.free(unsafe.Pointer(b))
	C.nous_post_notification(t, s, b)
}
