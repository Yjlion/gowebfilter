//go:build !linux

package netpriv

// Current reports no privileges off Linux. Gateway mode is Linux-only, and
// internal/tun2socks keeps its own Windows probe (an administrator-token
// check, which capget cannot express) - only the Linux half and the wording
// are shared through this package.
func Current() (bool, string) { return false, "unsupported platform" }
