package tun2socks

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net"
	"os/exec"
	"strings"

	"github.com/yjlion/gowebfilter/internal/models"
)

// This file has no build tag on purpose, even though everything in it is about
// Linux - and it is named "linuxroutes" rather than "route_linux" because a
// _linux filename suffix IS an implicit build constraint, which would defeat
// the point. All it does is build `ip` argument lists - there is no syscall, no
// unix package, nothing that needs a Linux toolchain - and these are the
// riskiest commands in the repository: they rewrite a live host's routing
// table. Keeping them buildable everywhere means their tests run in ordinary
// CI on any platform rather than only on a Linux runner. The dispatch in
// commands.go still only calls them when GOOS is linux.

// Capture is installed with policy routing rather than by rewriting the main
// table, which is what the first implementation did (`ip route replace default
// ... metric 1`). That had two failure modes that only show up on a live host:
//
//  1. The engine's own upstream sockets followed the new default straight back
//     into the TUN, so every fetch looped engine -> tun2socks -> engine.
//  2. Nothing restored the displaced default route, so stopping the service -
//     or crashing - left the host with no network until it was rebooted.
//
// Keeping the main table untouched fixes both. The capture default lives in a
// private table selected by a low-priority rule, so the host's real default
// route is never disturbed; if this process dies without cleaning up, the
// device disappears with it and the rules fall through to main.
const (
	// tunTable is the private routing table holding the capture default route.
	// 8888 is well clear of the reserved ids (main 254, default 253, local 255)
	// and of the low numbers other tools tend to claim.
	tunTable = "8888"

	// Rule priorities. All three sit below the stock main/default rules
	// (32766/32767) so they are consulted first, and above the local table
	// (priority 0) so loopback is never affected.
	rulePrefEgress  = "9000" // engine's own traffic -> main
	rulePrefCapture = "9100" // everything else -> tunTable
)

// tunPrefixLen turns the configured dotted-quad netmask into a prefix length.
// The original code hardcoded /15 and ignored tun_netmask entirely; 15 stays
// the fallback so an unparseable or empty mask behaves exactly as before.
func tunPrefixLen(netmask string) int {
	ip := net.ParseIP(strings.TrimSpace(netmask))
	if ip == nil {
		return 15
	}
	v4 := ip.To4()
	if v4 == nil {
		return 15
	}
	ones, bits := net.IPMask(v4).Size()
	if bits == 0 { // non-contiguous mask: Size reports 0, 0
		return 15
	}
	return ones
}

func configureLinux(ctx context.Context, cfg models.Tun2SocksConfig, runner commandRunner) error {
	// A previous run that was killed rather than stopped can leave the device
	// and the rules behind. Reclaim them first: `ip rule add` is additive, so
	// without this every restart would stack another copy.
	unconfigureLinux(ctx, cfg, runner)

	if err := runner.Run(ctx, "ip", "tuntap", "add", "mode", "tun", "dev", cfg.DeviceName); err != nil {
		return err
	}
	addr := fmt.Sprintf("%s/%d", cfg.TunAddress, tunPrefixLen(cfg.TunNetmask))
	if err := runner.Run(ctx, "ip", "addr", "replace", addr, "dev", cfg.DeviceName); err != nil {
		return err
	}
	if err := runner.Run(ctx, "ip", "link", "set", "dev", cfg.DeviceName, "up"); err != nil {
		return err
	}
	if err := runner.Run(ctx, "ip", "route", "replace", "default", "via", cfg.TunGateway,
		"dev", cfg.DeviceName, "table", tunTable); err != nil {
		return err
	}
	// bypass_cidrs was validated but never applied by any platform before this.
	// A `throw` route ends the lookup in this table without matching, so the
	// kernel carries on with the next rule and the destination is routed
	// normally - which is what "bypass" has to mean for LAN and loopback
	// ranges that must not be tunnelled.
	for _, cidr := range cfg.BypassCIDRs {
		if err := runner.Run(ctx, "ip", "route", "replace", "throw", cidr, "table", tunTable); err != nil {
			return err
		}
	}
	if err := runner.Run(ctx, "ip", "rule", "add", "pref", rulePrefEgress,
		"fwmark", fmt.Sprintf("%#x", EgressMark), "lookup", "main"); err != nil {
		return err
	}
	if err := runner.Run(ctx, "ip", "rule", "add", "pref", rulePrefCapture,
		"lookup", tunTable); err != nil {
		return err
	}
	return nil
}

// unconfigureLinux is configureLinux's inverse, and is deliberately
// best-effort: it runs during shutdown and as a pre-clean, where most of these
// commands are expected to fail because the thing they remove is already gone.
// Order matters - drop the rules before the routes they select, so no traffic
// is ever pointed at a table being emptied.
func unconfigureLinux(ctx context.Context, cfg models.Tun2SocksConfig, runner commandRunner) {
	_ = runner.Run(ctx, "ip", "rule", "del", "pref", rulePrefCapture)
	_ = runner.Run(ctx, "ip", "rule", "del", "pref", rulePrefEgress)
	_ = runner.Run(ctx, "ip", "route", "flush", "table", tunTable)
	_ = runner.Run(ctx, "ip", "link", "del", cfg.DeviceName)
}

// warnIfIPv6DefaultRoute reports a v6 default route at capture start. The TUN
// device carries IPv4 only, so with one present every dual-stack connection
// leaves the host over IPv6 and is never filtered - a silent bypass that is
// much better surfaced as a log line than discovered later.
func warnIfIPv6DefaultRoute(ctx context.Context) {
	out, err := exec.CommandContext(ctx, "ip", "-6", "route", "show", "default").Output()
	if err != nil || len(bytes.TrimSpace(out)) == 0 {
		return
	}
	slog.Warn("tun2socks: an IPv6 default route is present; IPv6 traffic bypasses TUN capture " +
		"(capture is IPv4-only). Disable IPv6 or accept that v6 destinations are unfiltered.")
}
