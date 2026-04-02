package main

// Tray abstracts the platform-native system tray icon and menu.
// Each platform provides its own implementation via build tags.
//
// On macOS, Wails v2's Objective-C AppDelegate conflicts with external
// systray libraries (getlantern/systray, progrium/macdriver). Until Wails v3
// stabilises its integrated tray support, the macOS implementation logs a
// deferred message and returns a no-op tray. The interface is defined now so
// platform implementations can be swapped in without changing the call sites.
type Tray interface {
	// Show makes the tray icon visible.
	Show()
	// SetConnected updates the icon to reflect daemon connection state.
	SetConnected(connected bool)
	// SetLevel updates the tray menu to reflect the current notification level.
	SetLevel(level int)
	// OnOpen registers a callback invoked when the user clicks "Open".
	OnOpen(fn func())
	// OnQuit registers a callback invoked when the user clicks "Quit".
	OnQuit(fn func())
	// OnSetLevel registers a callback for level changes from the tray menu.
	OnSetLevel(fn func(int))
	// OnPause registers a callback for pause/resume toggle.
	OnPause(fn func())
	// Destroy releases tray resources.
	Destroy()
}

// trayInstance holds the current tray (or nil if not available).
var trayInstance Tray

// setupTray creates and configures the platform tray. Called from startup().
func setupTray(app *App) {
	t, err := newTray()
	if err != nil {
		app.log.Info("system tray unavailable", "err", err)
		return
	}
	trayInstance = t

	t.OnOpen(func() {
		if app.ctx != nil {
			wailsShow(app.ctx)
		}
	})
	t.OnQuit(func() {
		if app.ctx != nil {
			wailsQuit(app.ctx)
		}
	})
	t.OnSetLevel(func(level int) {
		if err := app.SetLevel(level); err != nil {
			app.log.Warn("tray: set level failed", "err", err)
		}
	})
	t.OnPause(func() {
		// Toggle between ambient (2) and silent (0).
		if app.IsConnected() {
			if err := app.SetLevel(0); err != nil {
				app.log.Warn("tray: pause failed", "err", err)
			}
		}
	})
	t.Show()
}

// updateTrayStatus updates the tray icon for connection state changes.
func updateTrayStatus(connected bool) {
	if trayInstance != nil {
		trayInstance.SetConnected(connected)
	}
}
