//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c -mmacosx-version-min=10.14
#cgo LDFLAGS: -framework Foundation -framework ApplicationServices

#import <Foundation/Foundation.h>
#import <ApplicationServices/ApplicationServices.h>

// checkAccessibility returns 1 if the app has Accessibility access, 0 otherwise.
int checkAccessibility() {
    return AXIsProcessTrusted() ? 1 : 0;
}

// promptAccessibility opens the macOS Accessibility preferences pane and
// registers the calling app for trust. The user must toggle the switch manually.
void promptAccessibility() {
    NSDictionary *opts = @{(__bridge NSString *)kAXTrustedCheckOptionPrompt: @YES};
    AXIsProcessTrustedWithOptions((__bridge CFDictionaryRef)opts);
}
*/
import "C"

// CheckAccessibility returns whether the app has macOS Accessibility permission.
func (a *App) CheckAccessibility() bool {
	return C.checkAccessibility() == 1
}

// PromptAccessibility opens the macOS System Settings > Privacy > Accessibility
// pane and registers the app for trust. The user must toggle the switch.
func (a *App) PromptAccessibility() {
	C.promptAccessibility()
}
