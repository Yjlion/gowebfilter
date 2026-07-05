//go:build windows

package main

import "github.com/yjlion/gowebfilter/internal/config"

// shouldAutoStartTray reports whether `webfilter run`, launched as a normal
// interactive process (not dispatched to us by the Windows Service Control
// Manager - see runAsWindowsServiceIfApplicable), should bring up the system
// tray itself. An interactively-launched process on Windows always has a
// desktop/GUI session available, so the tray shows by default; set
// disable_tray in settings.json to opt out and get a plain foreground run
// instead.
func shouldAutoStartTray(settingsPath string) bool {
	if err := config.BootstrapRuntimeFiles(settingsPath); err != nil {
		return false
	}
	settings, err := config.LoadSettings(settingsPath)
	if err != nil {
		return false
	}
	return !settings.DisableTray
}
