//go:build !windows && !linux

package tun2socks

func hasRoutePrivileges() (bool, string) {
	return false, "unsupported platform"
}
