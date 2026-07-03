//go:build windows

package tun2socks

import "testing"

func TestParseWindowsDefaultRouteInterfaceIP(t *testing.T) {
	out := `
IPv4 Route Table
===========================================================================
Active Routes:
Network Destination        Netmask          Gateway       Interface  Metric
          0.0.0.0          0.0.0.0     192.168.12.1   192.168.12.126     30
===========================================================================
`
	if got := parseWindowsDefaultRouteInterfaceIP(out); got != "192.168.12.126" {
		t.Fatalf("parseWindowsDefaultRouteInterfaceIP = %q", got)
	}
}
