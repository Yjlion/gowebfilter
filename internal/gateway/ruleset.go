package gateway

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/yjlion/gowebfilter/internal/models"
)

// This file has no build tag, and is named "ruleset" rather than
// "nftables_linux", on purpose: a _linux suffix is an implicit GOOS constraint.
// All it does is render an nftables ruleset from config, which is the part
// worth pinning with tests that run anywhere - rewriting a live host's
// firewall is not something to discover a typo in at runtime. The dispatch in
// gateway.go still gates on runtime.GOOS.

// TableName is the single nftables table everything lives in. One table means
// teardown is one `nft delete table`, and means the ruleset can never be
// half-removed.
const TableName = "webfilter"

// BuildRuleset renders the complete `nft -f -` input for gateway mode.
// socksPort is the transparent listener's bound port, which the caller reads
// off the listener after binding - so the rules can never point at a port
// nothing is serving.
func BuildRuleset(cfg models.GatewayConfig, transparentPort int) string {
	var b strings.Builder
	w := func(format string, args ...any) {
		fmt.Fprintf(&b, format+"\n", args...)
	}

	// Flushing first makes the whole file idempotent: re-applying replaces the
	// table wholesale instead of appending a second copy of every rule.
	w("table ip %s", TableName)
	w("delete table ip %s", TableName)
	w("table ip %s {", TableName)

	if len(cfg.BypassCIDRs) > 0 {
		w("  set bypass {")
		w("    type ipv4_addr")
		w("    flags interval")
		w("    elements = { %s }", strings.Join(cfg.BypassCIDRs, ", "))
		w("  }")
	}
	if len(cfg.ClientCIDRs) > 0 {
		w("  set clients {")
		w("    type ipv4_addr")
		w("    flags interval")
		w("    elements = { %s }", strings.Join(cfg.ClientCIDRs, ", "))
		w("  }")
	}

	// dstnat priority is where REDIRECT belongs; it runs before the routing
	// decision, so a packet addressed to some server on the internet is turned
	// into one addressed to this machine and never needs forwarding at all.
	w("  chain prerouting {")
	w("    type nat hook prerouting priority dstnat; policy accept;")
	// Locally generated traffic never traverses prerouting, so the engine's own
	// upstream connections cannot loop back in - but loopback-to-loopback does,
	// and there is nothing to intercept there.
	w(`    iif "lo" return`)
	if cfg.Interface != "" {
		w(`    iifname != "%s" return`, cfg.Interface)
	}
	if len(cfg.BypassCIDRs) > 0 {
		w("    ip saddr @bypass return")
		w("    ip daddr @bypass return")
	}
	if len(cfg.ClientCIDRs) > 0 {
		w("    ip saddr != @clients return")
	}
	w("    tcp dport { %s } redirect to :%d", portList(cfg.InterceptPorts), transparentPort)
	w("  }")

	if cfg.DropQUIC {
		// Transparent capture is TCP-only. Left alone, a browser negotiates
		// HTTP/3 over UDP/443 and every filter in the pipeline is bypassed, so
		// the drop is what makes the TCP interception meaningful rather than
		// decorative. Same reasoning for DNS-over-QUIC on 853.
		w("  chain forward {")
		w("    type filter hook forward priority filter; policy accept;")
		if len(cfg.BypassCIDRs) > 0 {
			w("    ip saddr @bypass return")
			w("    ip daddr @bypass return")
		}
		w("    udp dport { 443, 853 } drop")
		w("  }")
	}

	if cfg.Masquerade && cfg.WANInterface != "" {
		w("  chain postrouting {")
		w("    type nat hook postrouting priority srcnat; policy accept;")
		w(`    oifname "%s" masquerade`, cfg.WANInterface)
		w("  }")
	}

	w("}")
	return b.String()
}

// portList renders the intercept ports, de-duplicated and ordered so the
// generated ruleset is stable regardless of how the config was written.
func portList(ports []int) string {
	seen := make(map[int]bool, len(ports))
	uniq := make([]int, 0, len(ports))
	for _, p := range ports {
		if p > 0 && p < 65536 && !seen[p] {
			seen[p] = true
			uniq = append(uniq, p)
		}
	}
	sort.Ints(uniq)
	parts := make([]string, len(uniq))
	for i, p := range uniq {
		parts[i] = strconv.Itoa(p)
	}
	return strings.Join(parts, ", ")
}

// ValidateConfig rejects a gateway block that could not work, so a bad setting
// is caught at PUT /api/settings rather than at the next restart.
func ValidateConfig(cfg models.GatewayConfig, mgmtPort int) error {
	if !cfg.Enabled {
		return nil
	}
	if len(cfg.InterceptPorts) == 0 {
		return fmt.Errorf("gateway intercept_ports must list at least one port")
	}
	for _, p := range cfg.InterceptPorts {
		if p < 1 || p > 65535 {
			return fmt.Errorf("gateway intercept_ports contains invalid port %d", p)
		}
		// Redirecting the management port would capture the UI into the filter
		// and lock the operator out of the box they are configuring.
		if p == mgmtPort {
			return fmt.Errorf("gateway intercept_ports must not include the management port %d", p)
		}
	}
	if err := validateCIDRs("client_cidrs", cfg.ClientCIDRs); err != nil {
		return err
	}
	if err := validateCIDRs("bypass_cidrs", cfg.BypassCIDRs); err != nil {
		return err
	}
	if cfg.Masquerade && cfg.WANInterface == "" {
		return fmt.Errorf("gateway masquerade needs wan_interface set")
	}
	return nil
}
