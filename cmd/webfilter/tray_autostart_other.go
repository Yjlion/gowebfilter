//go:build !windows

package main

// shouldAutoStartTray is always false outside Windows: there's no
// desktop-session guarantee for an interactively-launched `webfilter run`
// (e.g. it's routinely started headless under systemd), so the tray stays
// opt-in via the standalone `webfilter tray` command instead.
func shouldAutoStartTray(settingsPath string) bool { return false }
