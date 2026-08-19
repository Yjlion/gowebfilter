package gateway

import (
	"fmt"
	"net"
	"strings"
)

// validateCIDRs rejects anything nft would choke on later. Bare addresses are
// accepted and normalised by the caller's ruleset rendering (nft takes both),
// so the check is deliberately the same one an operator would expect.
func validateCIDRs(field string, values []string) error {
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if _, _, err := net.ParseCIDR(v); err == nil {
			continue
		}
		if net.ParseIP(v) != nil {
			continue
		}
		return fmt.Errorf("gateway %s contains invalid address %q", field, v)
	}
	return nil
}
