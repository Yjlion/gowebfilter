package state

import (
	"net"
	"strings"
	"time"

	"github.com/yjlion/gowebfilter/internal/models"
)

const (
	PolicyMatchNone     = "none"
	PolicyMatchMAC      = "mac"
	PolicyMatchExactIP  = "exact_ip"
	PolicyMatchCIDR     = "cidr"
	PolicyMatchCatchAll = "catch_all"
)

// PolicyMatch explains the same policy-selection decision Runtime.GetPolicy
// makes. It exists so diagnostic tools can show which tier matched instead of
// forcing operators to infer that from logs.
type PolicyMatch struct {
	PolicyIndex      int      `json:"policy_index"`
	PolicyName       string   `json:"policy_name,omitempty"`
	Tier             string   `json:"tier"`
	Source           string   `json:"source,omitempty"`
	ResolvedMAC      string   `json:"resolved_mac,omitempty"`
	InactivePolicies []string `json:"inactive_policies,omitempty"`
}

// MatchPolicy matches a client IP to a policy by specificity, most specific
// first: MAC, exact IP, CIDR, then catch-all. It mirrors Runtime.GetPolicy but
// takes an explicit time and MAC lookup function for tests and simulations.
func MatchPolicy(policies []models.Policy, clientIP string, now time.Time, lookupMAC func(string) string) PolicyMatch {
	result := PolicyMatch{PolicyIndex: -1, Tier: PolicyMatchNone}
	if idx := strings.IndexByte(clientIP, '%'); idx != -1 {
		clientIP = clientIP[:idx]
	}
	addr := net.ParseIP(strings.TrimSpace(clientIP))
	if addr == nil {
		return result
	}
	inactive := inactivePolicyNames(policies, now)

	hasMacPolicy := false
	for i := range policies {
		if len(policies[i].SourceMACs) > 0 {
			hasMacPolicy = true
			break
		}
	}
	if hasMacPolicy && lookupMAC != nil {
		if mac := lookupMAC(clientIP); mac != "" {
			result.ResolvedMAC = mac
			for i := range policies {
				p := &policies[i]
				if !p.Schedule.IsActiveAt(now) {
					continue
				}
				if containsString(p.SourceMACs, mac) {
					return matchedPolicy(i, *p, PolicyMatchMAC, mac, result.ResolvedMAC, inactive)
				}
			}
		}
	}

	for i := range policies {
		p := &policies[i]
		if !p.Schedule.IsActiveAt(now) {
			continue
		}
		for _, src := range p.SourceIPs {
			if strings.Contains(src, "/") {
				continue
			}
			target := net.ParseIP(strings.TrimSpace(src))
			if target == nil {
				continue
			}
			if addr.Equal(target) {
				return matchedPolicy(i, *p, PolicyMatchExactIP, src, result.ResolvedMAC, inactive)
			}
		}
	}

	bestIndex := -1
	bestPrefix := -1
	bestSource := ""
	for i := range policies {
		p := &policies[i]
		if !p.Schedule.IsActiveAt(now) {
			continue
		}
		for _, src := range p.SourceIPs {
			if !strings.Contains(src, "/") {
				continue
			}
			_, ipnet, err := net.ParseCIDR(strings.TrimSpace(src))
			if err != nil {
				continue
			}
			if !ipnet.Contains(addr) {
				continue
			}
			if ones, _ := ipnet.Mask.Size(); ones > bestPrefix {
				bestIndex = i
				bestPrefix = ones
				bestSource = src
			}
		}
	}
	if bestIndex >= 0 {
		return matchedPolicy(bestIndex, policies[bestIndex], PolicyMatchCIDR, bestSource, result.ResolvedMAC, inactive)
	}

	for i := range policies {
		p := &policies[i]
		if len(p.SourceIPs) == 0 && p.Schedule.IsActiveAt(now) {
			return matchedPolicy(i, *p, PolicyMatchCatchAll, "", result.ResolvedMAC, inactive)
		}
	}
	result.InactivePolicies = inactive
	return result
}

func matchedPolicy(index int, p models.Policy, tier, source, mac string, inactive []string) PolicyMatch {
	return PolicyMatch{PolicyIndex: index, PolicyName: p.Name, Tier: tier, Source: source, ResolvedMAC: mac, InactivePolicies: inactive}
}

func inactivePolicyNames(policies []models.Policy, now time.Time) []string {
	out := []string{}
	for i := range policies {
		if !policies[i].Schedule.IsActiveAt(now) {
			out = append(out, policies[i].Name)
		}
	}
	return out
}

func containsString(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}
