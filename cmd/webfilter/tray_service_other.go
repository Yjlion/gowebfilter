//go:build !windows

package main

import "github.com/gogpu/systray"

func addServiceItems(menu *systray.Menu) {
	menu.Add("Service controls unavailable", func() {})
}
