//go:build linux

package tun2socks

import (
	"context"
	"os"

	"github.com/yjlion/gowebfilter/internal/models"
)

func hasRoutePrivileges() (bool, string) {
	if os.Geteuid() != 0 {
		return false, "current process is not running as root"
	}
	return true, "root"
}

func configureLinux(ctx context.Context, cfg models.Tun2SocksConfig, runner commandRunner) error {
	_ = runner.Run(ctx, "ip", "tuntap", "add", "mode", "tun", "dev", cfg.DeviceName)
	if err := runner.Run(ctx, "ip", "addr", "replace", cfg.TunAddress+"/15", "dev", cfg.DeviceName); err != nil {
		return err
	}
	if err := runner.Run(ctx, "ip", "link", "set", "dev", cfg.DeviceName, "up"); err != nil {
		return err
	}
	if err := runner.Run(ctx, "ip", "route", "replace", "default", "via", cfg.TunGateway, "dev", cfg.DeviceName, "metric", "1"); err != nil {
		return err
	}
	return nil
}
