//go:build windows

package tun2socks

import (
	"context"
	"fmt"
	"net"
	"os"
	"time"

	"github.com/yjlion/gowebfilter/internal/models"
	"golang.org/x/sys/windows"
)

func hasRoutePrivileges() (bool, string) {
	var sid *windows.SID
	if err := windows.AllocateAndInitializeSid(&windows.SECURITY_NT_AUTHORITY, 2,
		windows.SECURITY_BUILTIN_DOMAIN_RID,
		windows.DOMAIN_ALIAS_RID_ADMINS,
		0, 0, 0, 0, 0, 0, &sid); err != nil {
		return false, err.Error()
	}
	defer windows.FreeSid(sid)
	token := windows.Token(0)
	member, err := token.IsMember(sid)
	if err != nil {
		return false, err.Error()
	}
	if !member {
		return false, "current process is not elevated as Administrator"
	}
	return true, "administrator"
}

func configureWindows(ctx context.Context, cfg models.Tun2SocksConfig, runner commandRunner) error {
	if err := waitForWindowsInterface(ctx, cfg.DeviceName, 10*time.Second); err != nil {
		return err
	}
	if err := runner.Run(ctx, "netsh", "interface", "ipv4", "set", "address",
		"name="+cfg.DeviceName, "static", cfg.TunAddress, cfg.TunNetmask); err != nil {
		return err
	}
	_ = runner.Run(ctx, "netsh", "interface", "ipv4", "delete", "dnsservers", "name="+cfg.DeviceName, "all")
	for i, dns := range cfg.DNSServers {
		if i == 0 {
			if err := runner.Run(ctx, "netsh", "interface", "ipv4", "set", "dnsservers",
				"name="+cfg.DeviceName, "static", dns, "primary", "validate=no"); err != nil {
				return err
			}
			continue
		}
		if err := runner.Run(ctx, "netsh", "interface", "ipv4", "add", "dnsservers",
			"name="+cfg.DeviceName, "address="+dns, fmt.Sprintf("index=%d", i+1), "validate=no"); err != nil {
			return err
		}
	}
	if err := runner.Run(ctx, "netsh", "interface", "ipv4", "add", "route",
		"prefix=0.0.0.0/0", "interface="+cfg.DeviceName, "nexthop="+cfg.TunGateway, "metric=1", "store=active"); err != nil {
		return err
	}
	return nil
}

func waitForWindowsInterface(ctx context.Context, name string, timeout time.Duration) error {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	for {
		if _, err := net.InterfaceByName(name); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("tun2socks adapter %q did not appear within %s", name, timeout)
		case <-tick.C:
		}
	}
}

func openPath(path string) error {
	return windows.ShellExecute(0, windows.StringToUTF16Ptr("open"), windows.StringToUTF16Ptr(path), nil, nil, windows.SW_SHOWNORMAL)
}

func defaultOpenPath(path string) string {
	if path == "" {
		wd, _ := os.Getwd()
		return wd
	}
	return path
}
