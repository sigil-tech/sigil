package main

import (
	"log/slog"
)

// setupTray is a placeholder for system tray functionality.
//
// Wails v2 and getlantern/systray both define an Objective-C AppDelegate on
// macOS, causing a duplicate symbol linker error. Until Wails v3 (which has
// integrated systray support) is stable, we skip the external tray library.
//
// The app still works as intended: it starts hidden (StartHidden: true in
// Wails options) and hides on close (HideWindowOnClose: true). Users can
// reopen it by launching sigil-app again, which will show the existing
// instance's window via single-instance detection.
//
// TODO: Re-add system tray icon when Wails v3 is stable.
func setupTray(app *App) {
	slog.Info("system tray: deferred (Wails v2 + systray AppDelegate conflict)")
}

// updateTrayStatus is a no-op until the tray icon is implemented.
func updateTrayStatus(connected bool) {
	// Will update tray icon when systray integration is available.
}
