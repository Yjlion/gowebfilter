package gateway

import (
	"strings"
	"testing"

	"github.com/yjlion/gowebfilter/internal/models"
)

func enabledCfg() models.GatewayConfig {
	cfg := models.NewGatewayConfig()
	cfg.Enabled = true
	return cfg
}

func mustContain(t *testing.T, ruleset string, lines ...string) {
	t.Helper()
	for _, want := range lines {
		if !strings.Contains(ruleset, want) {
			t.Errorf("ruleset is missing %q\n---\n%s", want, ruleset)
		}
	}
}

// The ruleset rewrites a live host's firewall, so its shape is pinned rather
// than eyeballed.
func TestBuildRulesetDefaults(t *testing.T) {
	rs := BuildRuleset(enabledCfg(), 40001)

	mustContain(t, rs,
		// Re-applying must replace the table, never append a second copy of
		// every rule - so the file deletes before it creates.
		"table ip webfilter\ndelete table ip webfilter\ntable ip webfilter {",
		"type nat hook prerouting priority dstnat; policy accept;",
		"tcp dport { 80, 443 } redirect to :40001",
		// QUIC is dropped or transparent TCP capture is decorative: a browser
		// just speaks HTTP/3 instead.
		"udp dport { 443, 853 } drop",
		"ip saddr @bypass return",
		"ip daddr @bypass return",
	)
	if strings.Contains(rs, "masquerade") {
		t.Error("masquerade appeared without being configured")
	}
	if strings.Contains(rs, "@clients") {
		t.Error("a client set was emitted for an empty client_cidrs")
	}
}

func TestBuildRulesetScopesToInterfaceAndClients(t *testing.T) {
	cfg := enabledCfg()
	cfg.Interface = "enp0s3"
	cfg.ClientCIDRs = []string{"192.168.12.0/24"}
	rs := BuildRuleset(cfg, 40001)

	mustContain(t, rs,
		`iifname != "enp0s3" return`,
		"elements = { 192.168.12.0/24 }",
		"ip saddr != @clients return",
	)
}

func TestBuildRulesetMasquerade(t *testing.T) {
	cfg := enabledCfg()
	cfg.Masquerade = true
	cfg.WANInterface = "enp0s8"
	rs := BuildRuleset(cfg, 1)
	mustContain(t, rs,
		"type nat hook postrouting priority srcnat; policy accept;",
		`oifname "enp0s8" masquerade`,
	)
}

func TestBuildRulesetDropQUICCanBeDisabled(t *testing.T) {
	cfg := enabledCfg()
	cfg.DropQUIC = false
	if rs := BuildRuleset(cfg, 1); strings.Contains(rs, "udp dport") {
		t.Errorf("QUIC drop emitted with drop_quic off:\n%s", rs)
	}
}

// Port rendering is deduplicated and ordered so the same config always yields
// byte-identical rules, whatever order the ports were written in.
func TestPortListIsStableAndDeduplicated(t *testing.T) {
	if got := portList([]int{443, 80, 443, 8080}); got != "80, 443, 8080" {
		t.Errorf("portList() = %q, want %q", got, "80, 443, 8080")
	}
	if got := portList([]int{0, 70000, 80}); got != "80" {
		t.Errorf("portList() kept an out-of-range port: %q", got)
	}
}

func TestValidateConfig(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*models.GatewayConfig)
		mgmt    int
		wantErr string
	}{
		{"defaults are valid", func(*models.GatewayConfig) {}, 8000, ""},
		{"disabled is never validated", func(c *models.GatewayConfig) {
			c.Enabled = false
			c.InterceptPorts = nil
			c.ClientCIDRs = []string{"nonsense"}
		}, 8000, ""},
		{"no ports", func(c *models.GatewayConfig) { c.InterceptPorts = nil }, 8000,
			"at least one port"},
		{"out-of-range port", func(c *models.GatewayConfig) { c.InterceptPorts = []int{70000} }, 8000,
			"invalid port"},
		// Redirecting the management port would pull the UI into the filter and
		// lock the operator out of the box they are configuring.
		{"management port", func(c *models.GatewayConfig) { c.InterceptPorts = []int{80, 8000} }, 8000,
			"management port"},
		{"bad client cidr", func(c *models.GatewayConfig) { c.ClientCIDRs = []string{"192.168.1.0/33"} }, 8000,
			"client_cidrs"},
		{"bad bypass cidr", func(c *models.GatewayConfig) { c.BypassCIDRs = []string{"not-an-ip"} }, 8000,
			"bypass_cidrs"},
		{"masquerade without wan", func(c *models.GatewayConfig) { c.Masquerade = true }, 8000,
			"wan_interface"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := enabledCfg()
			tc.mutate(&cfg)
			err := ValidateConfig(cfg, tc.mgmt)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("ValidateConfig() = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("ValidateConfig() = %v, want an error mentioning %q", err, tc.wantErr)
			}
		})
	}
}

// A bare IP is legal in both lists; nft accepts it and operators write it.
func TestValidateConfigAcceptsBareAddresses(t *testing.T) {
	cfg := enabledCfg()
	cfg.BypassCIDRs = []string{"10.0.0.5", "10.0.0.0/8"}
	if err := ValidateConfig(cfg, 8000); err != nil {
		t.Fatalf("ValidateConfig() = %v, want nil", err)
	}
}
