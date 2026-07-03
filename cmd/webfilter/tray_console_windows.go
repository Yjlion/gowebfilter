//go:build windows

package main

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// hideOwnConsoleWindow hides the console window if, and only if, this
// process is the sole process attached to it - i.e. Windows allocated a
// brand new console for us because we were double-clicked or launched via
// "Start > Run" rather than from an existing interactive shell. A "system
// tray" utility popping up a bare console window alongside the tray icon
// looks broken; hiding it here keeps `webfilter run`/`proxy`/`mgmt`
// (launched from a terminal the user wants to keep watching) unaffected,
// since GetConsoleProcessList returns more than one PID in that case.
func hideOwnConsoleWindow() {
	kernel32 := windows.NewLazySystemDLL("kernel32.dll")
	user32 := windows.NewLazySystemDLL("user32.dll")
	procGetConsoleProcessList := kernel32.NewProc("GetConsoleProcessList")
	procGetConsoleWindow := kernel32.NewProc("GetConsoleWindow")
	procShowWindow := user32.NewProc("ShowWindow")

	const swHide = 0

	var pids [2]uint32
	n, _, _ := procGetConsoleProcessList.Call(
		uintptr(unsafe.Pointer(&pids[0])),
		uintptr(len(pids)),
	)
	if n != 1 {
		return
	}

	hwnd, _, _ := procGetConsoleWindow.Call()
	if hwnd == 0 {
		return
	}
	_, _, _ = procShowWindow.Call(hwnd, swHide)
}
