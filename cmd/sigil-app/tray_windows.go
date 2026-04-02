//go:build windows

package main

import (
	"fmt"
	"log/slog"
)

// windowsTray is a no-op tray for Windows.
//
// A real implementation would use Shell_NotifyIcon via syscall, loading the
// icon from embedded assets and handling WM_COMMAND messages. Deferred until
// Windows CI and manual testing are available.
type windowsTray struct{}

func newTray() (Tray, error) {
	slog.Info("system tray: Shell_NotifyIcon not yet implemented")
	return &windowsTray{}, fmt.Errorf("windows tray not yet implemented")
}

func (t *windowsTray) Show()                {}
func (t *windowsTray) SetConnected(bool)    {}
func (t *windowsTray) SetLevel(int)         {}
func (t *windowsTray) OnOpen(func())        {}
func (t *windowsTray) OnQuit(func())        {}
func (t *windowsTray) OnSetLevel(func(int)) {}
func (t *windowsTray) OnPause(func())       {}
func (t *windowsTray) Destroy()             {}
