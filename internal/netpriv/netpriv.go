// Package netpriv answers one question two subsystems both need: can this
// process reconfigure the host's networking?
//
// TUN capture (internal/tun2socks) and gateway mode (internal/gateway) both
// need root or CAP_NET_ADMIN, both are reached from the same systemd unit, and
// both must report the same answer for the same reason - so the probe lives in
// one place rather than being written twice with two wordings.
package netpriv

// Describe reports whether euid/caps are sufficient to create network devices
// and rewrite routing or firewall state, and a human-readable reason when they
// are not.
//
// Kept as a pure function of its inputs so every outcome is testable from an
// ordinary unprivileged `go test`.
func Describe(euid int, caps State) (bool, string) {
	if euid == 0 {
		return true, "root"
	}
	switch {
	case caps.NetAdmin && caps.NetRaw:
		return true, "CAP_NET_ADMIN"
	case caps.NetAdmin:
		// Enough for `ip` and `nft`; only SO_BINDTODEVICE (tun2socks'
		// --interface) needs CAP_NET_RAW, so this is degraded, not broken.
		return true, "CAP_NET_ADMIN (no CAP_NET_RAW: interface binding will fail)"
	}
	detail := "not running as root and CAP_NET_ADMIN is not held; under systemd, grant it with " +
		"AmbientCapabilities=CAP_NET_ADMIN CAP_NET_RAW (see packaging/README.md)"
	if caps.Err != nil {
		detail += " (capability probe failed: " + caps.Err.Error() + ")"
	}
	return false, detail
}

// State is the slice of the effective capability set that matters here, plus a
// failed-probe marker.
type State struct {
	NetAdmin bool
	NetRaw   bool
	Err      error
}
