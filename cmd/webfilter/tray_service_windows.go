//go:build windows

package main

import "github.com/gogpu/systray"

func addServiceItems(menu *systray.Menu) {
	menu.Add("Start Service", func() { _ = startWindowsService(windowsServiceRunName) })
	menu.Add("Stop Service", func() { _ = stopWindowsService(windowsServiceRunName) })
	menu.Add("Service Status", func() { _ = statusWindowsService(windowsServiceRunName) })
}
