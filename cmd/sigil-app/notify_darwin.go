//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c -mmacosx-version-min=10.14
#cgo LDFLAGS: -framework Foundation -framework UserNotifications

#import <Foundation/Foundation.h>
#import <UserNotifications/UserNotifications.h>

// requestNotificationPermission asks macOS for permission to show notifications.
// Must be called from the main thread early in the app lifecycle.
void requestNotificationPermission() {
    UNUserNotificationCenter *center = [UNUserNotificationCenter currentNotificationCenter];
    [center requestAuthorizationWithOptions:(UNAuthorizationOptionAlert | UNAuthorizationOptionSound | UNAuthorizationOptionBadge)
                          completionHandler:^(BOOL granted, NSError * _Nullable error) {
        if (error) {
            NSLog(@"Sigil: notification permission error: %@", error);
        }
    }];
}

// showNotification delivers a native macOS notification via UNUserNotificationCenter.
void showNotification(const char *title, const char *body, const char *identifier) {
    UNMutableNotificationContent *content = [[UNMutableNotificationContent alloc] init];
    content.title = [NSString stringWithUTF8String:title];
    content.body = [NSString stringWithUTF8String:body];
    content.sound = [UNNotificationSound defaultSound];

    NSString *ident = [NSString stringWithUTF8String:identifier];
    UNNotificationRequest *request = [UNNotificationRequest requestWithIdentifier:ident
                                                                          content:content
                                                                          trigger:nil];

    UNUserNotificationCenter *center = [UNUserNotificationCenter currentNotificationCenter];
    [center addNotificationRequest:request withCompletionHandler:^(NSError * _Nullable error) {
        if (error) {
            NSLog(@"Sigil: notification delivery error: %@", error);
        }
    }];
}
*/
import "C"

import (
	"fmt"
	"sync"
)

// darwinNotifier uses Apple's UNUserNotificationCenter for native macOS
// notifications. Notifications appear as "Sigil" in Notification Center
// when the app is built as a .app bundle with a CFBundleIdentifier.
type darwinNotifier struct {
	once sync.Once
}

func newNotifier() Notifier {
	n := &darwinNotifier{}
	// Request permission on first creation. Apple requires this before
	// any notification can be delivered. The prompt appears once; the
	// user's choice is remembered by macOS.
	n.once.Do(func() {
		C.requestNotificationPermission()
	})
	return n
}

func (n *darwinNotifier) Show(title, body, iconPath string, suggestionID int64) error {
	if title == "" {
		return fmt.Errorf("notification title is required")
	}
	identifier := fmt.Sprintf("sigil-suggestion-%d", suggestionID)
	C.showNotification(C.CString(title), C.CString(body), C.CString(identifier))
	return nil
}

func (n *darwinNotifier) Close() {}
