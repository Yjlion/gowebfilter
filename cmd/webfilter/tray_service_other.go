//go:build !windows

package main

import "github.com/gogpu/systray"

func addServiceItems(tray *systray.SystemTray, menu *systray.Menu) {
	menu.Add("Service controls unavailable", func() {})
}
