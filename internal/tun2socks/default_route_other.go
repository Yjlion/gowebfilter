//go:build !windows

package tun2socks

import (
	"errors"
	"net"
)

func defaultRouteInterfaceIP(ifaces []net.Interface) (string, string, error) {
	return "", "", errors.New("default route lookup is not implemented on this platform")
}
