//go:build windows

package main

import "github.com/gogpu/systray"

// addServiceItems wires the tray's service-control menu items to the same
// service-management funcs `webfilter service ...` uses. Those funcs print
// their result via fmt.Printf, which is invisible from the tray (no console,
// or a hidden one - see hideOwnConsoleWindow) - so results are surfaced via
// a tray balloon notification instead, and errors are no longer silently
// dropped.
func addServiceItems(tray *systray.SystemTray, menu *systray.Menu) {
	menu.Add("Start Service", func() {
		notifyServiceResult(tray, "Start Service", startWindowsService(windowsServiceRunName))
	})
	menu.Add("Stop Service", func() {
		notifyServiceResult(tray, "Stop Service", stopWindowsService(windowsServiceRunName))
	})
	menu.Add("Service Status", func() {
		state, err := queryWindowsServiceState(windowsServiceRunName)
		if err != nil {
			tray.ShowNotification("WebFilter Proxy: Service Status failed", err.Error())
			return
		}
		tray.ShowNotification("WebFilter Proxy", "Service: "+serviceStateString(state))
	})
}

func notifyServiceResult(tray *systray.SystemTray, action string, err error) {
	if err != nil {
		tray.ShowNotification("WebFilter Proxy: "+action+" failed", err.Error())
		return
	}
	tray.ShowNotification("WebFilter Proxy", action+" succeeded")
}
