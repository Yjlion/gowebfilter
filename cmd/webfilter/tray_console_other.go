//go:build !windows

package main

// hideOwnConsoleWindow is a no-op outside Windows: Linux/macOS terminals
// don't have the "double-click allocates a throwaway console" problem this
// works around, and the systray backend for those platforms doesn't need it.
func hideOwnConsoleWindow() {}
