//go:build windows

package tun2socks

import (
	"fmt"

	"golang.org/x/sys/windows"
)

func checkPlatformPrerequisites() error {
	handle, err := windows.LoadLibraryEx("wintun.dll", 0, windows.LOAD_LIBRARY_SEARCH_APPLICATION_DIR|windows.LOAD_LIBRARY_SEARCH_SYSTEM32)
	if err != nil {
		return fmt.Errorf("wintun.dll is required on Windows; place the matching architecture DLL next to webfilter.exe or in System32: %w", err)
	}
	return windows.FreeLibrary(handle)
}
