//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c -mmacosx-version-min=10.14
#cgo LDFLAGS: -framework Foundation -framework UserNotifications

#import <Foundation/Foundation.h>
#import <UserNotifications/UserNotifications.h>

// SigilNotificationDelegate allows notifications to be displayed even when
// the app is in the foreground. Without this, macOS silently suppresses
// notifications for the frontmost app.
@interface SigilNotificationDelegate : NSObject <UNUserNotificationCenterDelegate>
@end

@implementation SigilNotificationDelegate
- (void)userNotificationCenter:(UNUserNotificationCenter *)center
       willPresentNotification:(UNNotification *)notification
         withCompletionHandler:(void (^)(UNNotificationPresentationOptions))completionHandler {
    // Show banner + sound even when app is in foreground (we gate this in Go).
    UNNotificationPresentationOptions opts = UNNotificationPresentationOptionSound;
    if (@available(macOS 11.0, *)) {
        opts |= UNNotificationPresentationOptionBanner;
    } else {
        opts |= UNNotificationPresentationOptionAlert;
    }
    completionHandler(opts);
}
@end

static SigilNotificationDelegate *_delegate = nil;

// setupNotifications requests permission and installs the delegate.
void setupNotifications() {
    UNUserNotificationCenter *center = [UNUserNotificationCenter currentNotificationCenter];

    if (!_delegate) {
        _delegate = [[SigilNotificationDelegate alloc] init];
        center.delegate = _delegate;
    }

    [center requestAuthorizationWithOptions:(UNAuthorizationOptionAlert | UNAuthorizationOptionSound | UNAuthorizationOptionBadge)
                          completionHandler:^(BOOL granted, NSError * _Nullable error) {
        if (error) {
            NSLog(@"Sigil: notification permission error: %@", error);
        } else if (!granted) {
            NSLog(@"Sigil: notification permission denied");
        } else {
            NSLog(@"Sigil: notification permission granted");
        }
    }];
}

// showNotification delivers a native macOS notification.
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
// notifications. Includes a UNUserNotificationCenterDelegate to allow
// banner display even when the app is the frontmost process.
type darwinNotifier struct {
	once sync.Once
}

func newNotifier() Notifier {
	n := &darwinNotifier{}
	n.once.Do(func() {
		C.setupNotifications()
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
