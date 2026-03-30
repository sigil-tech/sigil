//go:build darwin

package main

import (
	"fmt"
	"log/slog"
)

// darwinTray is a no-op tray for macOS.
//
// Wails v2 and external systray libraries (getlantern/systray,
// progrium/macdriver) both define an Objective-C AppDelegate, causing a
// duplicate symbol linker error. The app still works: HideWindowOnClose
// keeps it running, and relaunching shows the existing window.
//
// TODO: Replace with real NSStatusItem via Wails v3 integrated tray support.
type darwinTray struct{}

func newTray() (Tray, error) {
	slog.Info("system tray: deferred (Wails v2 + systray AppDelegate conflict on macOS)")
	return &darwinTray{}, fmt.Errorf("macOS tray deferred until Wails v3")
}

func (t *darwinTray) Show()                {}
func (t *darwinTray) SetConnected(bool)    {}
func (t *darwinTray) SetLevel(int)         {}
func (t *darwinTray) OnOpen(func())        {}
func (t *darwinTray) OnQuit(func())        {}
func (t *darwinTray) OnSetLevel(func(int)) {}
func (t *darwinTray) OnPause(func())       {}
func (t *darwinTray) Destroy()             {}
