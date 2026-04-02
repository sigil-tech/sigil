//go:build linux

package main

import (
	"fmt"
	"log/slog"
)

// linuxTray is a no-op tray for Linux.
//
// A real implementation would use org.kde.StatusNotifierItem via D-Bus
// (godbus/dbus/v5), with a fallback to no-op when no StatusNotifierHost
// is available. Deferred to avoid adding godbus as a dependency until the
// feature is fully tested across KDE, GNOME+AppIndicator, and bare GNOME.
type linuxTray struct{}

func newTray() (Tray, error) {
	slog.Info("system tray: D-Bus StatusNotifierItem not yet implemented")
	return &linuxTray{}, fmt.Errorf("linux tray not yet implemented")
}

func (t *linuxTray) Show()                {}
func (t *linuxTray) SetConnected(bool)    {}
func (t *linuxTray) SetLevel(int)         {}
func (t *linuxTray) OnOpen(func())        {}
func (t *linuxTray) OnQuit(func())        {}
func (t *linuxTray) OnSetLevel(func(int)) {}
func (t *linuxTray) OnPause(func())       {}
func (t *linuxTray) Destroy()             {}
