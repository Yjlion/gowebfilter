//go:build linux

package tun2socks

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// hasRoutePrivileges reports whether this process can create the TUN device
// and rewrite routes. Root is the obvious way, not the only one: systemd can
// hand an otherwise unprivileged unit exactly the capability that matters
// (AmbientCapabilities=CAP_NET_ADMIN - see packaging/tun2socks.conf), which is
// how the filtering engine keeps running as the unprivileged `webfilter` user
// while TUN capture still works.
//
// Ambient capabilities are the right mechanism here because they survive
// execve, so both the `ip` invocations in configureLinux and the tun2socks
// child inherit them. File capabilities (setcap on the downloaded binary)
// would NOT work: the shipped units set NoNewPrivileges=true, which disables
// them, and it is this process - not the child - that runs `ip`.
func hasRoutePrivileges() (bool, string) {
	return describeRoutePrivilege(os.Geteuid(), effectiveCaps())
}

// capState is the slice of the effective capability set this package cares
// about, plus a failed-probe marker. Keeping it a plain struct lets
// describeRoutePrivilege stay a pure function that an unprivileged `go test`
// can exercise across every case.
type capState struct {
	netAdmin bool
	netRaw   bool
	err      error
}

func describeRoutePrivilege(euid int, caps capState) (bool, string) {
	if euid == 0 {
		return true, "root"
	}
	switch {
	case caps.netAdmin && caps.netRaw:
		return true, "CAP_NET_ADMIN"
	case caps.netAdmin:
		// Capture still works; only tun2socks' --interface flag (which binds
		// its upstream sockets with SO_BINDTODEVICE) needs CAP_NET_RAW, and
		// that flag is only passed when tun2socks.interface_name is set.
		return true, "CAP_NET_ADMIN (no CAP_NET_RAW: interface_name binding will fail)"
	}
	detail := "not running as root and CAP_NET_ADMIN is not held; under systemd, grant it with AmbientCapabilities=CAP_NET_ADMIN CAP_NET_RAW (see packaging/README.md)"
	if caps.err != nil {
		detail += fmt.Sprintf(" (capability probe failed: %v)", caps.err)
	}
	return false, detail
}

// effectiveCaps reads the calling thread's effective capability set with
// capget(2). golang.org/x/sys is already a dependency (platform_windows.go
// uses x/sys/windows) and this is a bare syscall, so the check stays
// CGO_ENABLED=0 clean. Both capabilities we look for are below 32, so they
// live in the first of the two 32-bit words.
func effectiveCaps() capState {
	hdr := unix.CapUserHeader{Version: unix.LINUX_CAPABILITY_VERSION_3}
	var data [2]unix.CapUserData
	if err := unix.Capget(&hdr, &data[0]); err != nil {
		return capState{err: err}
	}
	return capState{
		netAdmin: data[0].Effective&(uint32(1)<<uint(unix.CAP_NET_ADMIN)) != 0,
		netRaw:   data[0].Effective&(uint32(1)<<uint(unix.CAP_NET_RAW)) != 0,
	}
}
