//go:build linux

package netpriv

import (
	"os"

	"golang.org/x/sys/unix"
)

// Current probes the calling thread's effective capability set with capget(2).
// golang.org/x/sys is already a dependency and this is a bare syscall, so the
// check stays CGO_ENABLED=0 clean. Both capabilities are below 32, so they live
// in the first of the two 32-bit words.
func Current() (bool, string) {
	hdr := unix.CapUserHeader{Version: unix.LINUX_CAPABILITY_VERSION_3}
	var data [2]unix.CapUserData
	if err := unix.Capget(&hdr, &data[0]); err != nil {
		return Describe(os.Geteuid(), State{Err: err})
	}
	return Describe(os.Geteuid(), State{
		NetAdmin: data[0].Effective&(uint32(1)<<uint(unix.CAP_NET_ADMIN)) != 0,
		NetRaw:   data[0].Effective&(uint32(1)<<uint(unix.CAP_NET_RAW)) != 0,
	})
}
