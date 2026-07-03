//go:build windows

package tun2socks

import (
	"errors"
	"net"
	"os/exec"
	"strings"
)

func defaultRouteInterfaceIP(ifaces []net.Interface) (string, string, error) {
	out, err := exec.Command("route", "print", "-4", "0.0.0.0").Output()
	if err != nil {
		return "", "", err
	}
	ip := parseWindowsDefaultRouteInterfaceIP(string(out))
	if ip == "" {
		return "", "", errors.New("no IPv4 default route interface found")
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		if hasInterfaceIPv4(iface, ip) {
			return iface.Name, ip, nil
		}
	}
	return "", "", errors.New("default route interface IP did not match an up interface")
}

func parseWindowsDefaultRouteInterfaceIP(output string) string {
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 5 || fields[0] != "0.0.0.0" || fields[1] != "0.0.0.0" {
			continue
		}
		if net.ParseIP(fields[3]) != nil {
			return fields[3]
		}
	}
	return ""
}

func hasInterfaceIPv4(iface net.Interface, want string) bool {
	addrs, err := iface.Addrs()
	if err != nil {
		return false
	}
	for _, addr := range addrs {
		var ip net.IP
		switch v := addr.(type) {
		case *net.IPNet:
			ip = v.IP
		case *net.IPAddr:
			ip = v.IP
		}
		if ip4 := ip.To4(); ip4 != nil && ip4.String() == want {
			return true
		}
	}
	return false
}
