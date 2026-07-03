package tun2socks

import (
	"context"
	"testing"

	"github.com/yjlion/gowebfilter/internal/models"
)

func TestValidateConfigRejectsInvalidCIDR(t *testing.T) {
	cfg := models.NewTun2SocksConfig()
	cfg.Enabled = true
	cfg.BypassCIDRs = []string{"not-a-cidr"}
	if err := ValidateConfig(cfg); err == nil {
		t.Fatal("expected invalid CIDR to be rejected")
	}
}

func TestValidateConfigAllowsInvalidFieldsWhenDisabled(t *testing.T) {
	cfg := models.NewTun2SocksConfig()
	cfg.Enabled = false
	cfg.DeviceName = ""
	cfg.TunAddress = "not-an-ip"
	cfg.BypassCIDRs = []string{"not-a-cidr"}
	if err := ValidateConfig(cfg); err != nil {
		t.Fatalf("disabled tun2socks config should not block saving: %v", err)
	}
}

func TestIsStartupSkipped(t *testing.T) {
	err := StartupSkippedError{Reason: "not elevated"}
	if !IsStartupSkipped(err) {
		t.Fatal("expected startup skipped error to be recognized")
	}
}

func TestProxyURLAcceptsExplicitNonLoopbackTarget(t *testing.T) {
	settings := models.NewGlobalSettings()
	settings.Tun2Socks.ProxyTarget = "192.0.2.10:1080"
	got, err := proxyURL(settings)
	if err != nil {
		t.Fatalf("proxyURL: %v", err)
	}
	if got != "socks5://192.0.2.10:1080" {
		t.Fatalf("proxyURL = %q", got)
	}
}

func TestProxyURLPrefersLocalSocks5Listener(t *testing.T) {
	settings := models.NewGlobalSettings()
	got, err := proxyURL(settings)
	if err != nil {
		t.Fatalf("proxyURL: %v", err)
	}
	if got != "socks5://127.0.0.1:1080" {
		t.Fatalf("proxyURL = %q", got)
	}
}

func TestProxyURLAcceptsExplicitLoopbackSocks5Target(t *testing.T) {
	settings := models.NewGlobalSettings()
	settings.Tun2Socks.ProxyTarget = "socks5://127.0.0.1:1080"
	settings.Tun2Socks.InterfaceName = "definitely-not-an-interface"
	got, err := proxyURL(settings)
	if err != nil {
		t.Fatalf("proxyURL: %v", err)
	}
	if got != "socks5://127.0.0.1:1080" {
		t.Fatalf("proxyURL = %q", got)
	}
}

func TestLooksVirtualInterface(t *testing.T) {
	for _, name := range []string{"VirtualBox Host-Only Ethernet Adapter", "vEthernet (WSL)", "DockerNAT", "Bluetooth Network Connection"} {
		if !looksVirtualInterface(name) {
			t.Fatalf("expected %q to be treated as virtual/host-only", name)
		}
	}
	if looksVirtualInterface("Intel(R) Wi-Fi 6E AX211 160MHz") {
		t.Fatal("expected Wi-Fi adapter to be treated as a normal interface")
	}
}

type recordingRunner struct {
	calls []string
}

func (r *recordingRunner) Run(ctx context.Context, name string, args ...string) error {
	r.calls = append(r.calls, name+" "+joinArgs(args))
	return nil
}

func joinArgs(args []string) string {
	out := ""
	for i, arg := range args {
		if i > 0 {
			out += " "
		}
		out += arg
	}
	return out
}
