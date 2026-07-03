//go:build windows

package tun2socks

import (
	"testing"

	"github.com/yjlion/gowebfilter/internal/models"
)

func TestDeviceNameUsesTunSchemeOnWindows(t *testing.T) {
	cfg := models.NewTun2SocksConfig()
	cfg.DeviceName = "webfilter-tun"
	if got := deviceName(cfg); got != "tun://webfilter-tun" {
		t.Fatalf("deviceName = %q", got)
	}
}
