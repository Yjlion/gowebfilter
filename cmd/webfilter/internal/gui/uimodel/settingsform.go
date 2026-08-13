// Package uimodel holds the native GUI's headless view-model logic - form
// parsing/validation, list formatting, poll dedup - kept free of gogpu
// imports so it is fully unit-testable without a window or GPU.
package uimodel

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/yjlion/gowebfilter/internal/models"
)

// SettingsForm is the string/bool-typed shape of the settings screen.
// Numeric fields travel as strings because text fields produce strings; the
// parse happens in Apply so a typo surfaces as a form error rather than a
// silent zero.
type SettingsForm struct {
	ProxyListen      string // one listener per line ("host:port", "regular@", "socks5@")
	MgmtHost         string
	MgmtPort         string
	UILanguage       string
	LogBlocks        bool
	LogRequests      bool
	LogRetentionDays string
	AuthEnabled      bool
	NewPassword      string // sent as new_password only when non-empty
	UpstreamProxy    string
	UpstreamAuth     string
	PacProxyHost     string
	PacDirectHosts   string // one per line
	PacDirectIPs     string // one per line
	DisableTray      bool

	Tun2SocksEnabled     bool
	Tun2SocksProxyTarget string
	Tun2SocksDNSServers  string // one per line
	Tun2SocksAutoRoutes  bool
	Tun2SocksBypassCIDRs string // one per line
}

// LoadSettingsForm converts fetched settings into editable form fields.
// List fields are joined with ", " because the native text fields are
// single-line; SplitLines accepts the same separator back.
func LoadSettingsForm(s models.GlobalSettings) SettingsForm {
	return SettingsForm{
		ProxyListen:      strings.Join(s.ProxyListen, ", "),
		MgmtHost:         s.MgmtHost,
		MgmtPort:         strconv.Itoa(s.MgmtPort),
		UILanguage:       s.UILanguage,
		LogBlocks:        s.LogBlocks,
		LogRequests:      s.LogRequests,
		LogRetentionDays: strconv.Itoa(s.LogRetentionDays),
		AuthEnabled:      s.AuthEnabled,
		UpstreamProxy:    s.UpstreamProxy,
		UpstreamAuth:     s.UpstreamAuth,
		PacProxyHost:     s.PacProxyHost,
		PacDirectHosts:   strings.Join(s.PacDirectHosts, ", "),
		PacDirectIPs:     strings.Join(s.PacDirectIPs, ", "),
		DisableTray:      s.DisableTray,

		Tun2SocksEnabled:     s.Tun2Socks.Enabled,
		Tun2SocksProxyTarget: s.Tun2Socks.ProxyTarget,
		Tun2SocksDNSServers:  strings.Join(s.Tun2Socks.DNSServers, ", "),
		Tun2SocksAutoRoutes:  s.Tun2Socks.AutoRoutes,
		Tun2SocksBypassCIDRs: strings.Join(s.Tun2Socks.BypassCIDRs, ", "),
	}
}

// Apply overlays the form onto base (the most recently fetched settings, so
// fields the form doesn't expose - cert_dir, secrets, hostname mappings -
// pass through untouched) and validates the parseable fields. Server-side
// validation still runs on PUT; this catches the obvious cases with
// field-specific messages before a round trip.
func (f SettingsForm) Apply(base models.GlobalSettings) (models.GlobalSettings, error) {
	out := base

	port, err := strconv.Atoi(strings.TrimSpace(f.MgmtPort))
	if err != nil || port < 1 || port > 65535 {
		return models.GlobalSettings{}, fmt.Errorf("management port must be a number between 1 and 65535")
	}
	retention, err := strconv.Atoi(strings.TrimSpace(f.LogRetentionDays))
	if err != nil || retention < 0 {
		return models.GlobalSettings{}, fmt.Errorf("log retention days must be a non-negative number")
	}
	listeners := SplitLines(f.ProxyListen)
	if len(listeners) == 0 {
		return models.GlobalSettings{}, fmt.Errorf("at least one proxy listener is required")
	}

	out.ProxyListen = listeners
	out.MgmtHost = strings.TrimSpace(f.MgmtHost)
	out.MgmtPort = port
	out.UILanguage = strings.TrimSpace(f.UILanguage)
	out.LogBlocks = f.LogBlocks
	out.LogRequests = f.LogRequests
	out.LogRetentionDays = retention
	out.AuthEnabled = f.AuthEnabled
	out.UpstreamProxy = strings.TrimSpace(f.UpstreamProxy)
	out.UpstreamAuth = strings.TrimSpace(f.UpstreamAuth)
	out.PacProxyHost = strings.TrimSpace(f.PacProxyHost)
	out.PacDirectHosts = SplitLines(f.PacDirectHosts)
	out.PacDirectIPs = SplitLines(f.PacDirectIPs)
	out.DisableTray = f.DisableTray

	out.Tun2Socks.Enabled = f.Tun2SocksEnabled
	out.Tun2Socks.ProxyTarget = strings.TrimSpace(f.Tun2SocksProxyTarget)
	out.Tun2Socks.DNSServers = SplitLines(f.Tun2SocksDNSServers)
	out.Tun2Socks.AutoRoutes = f.Tun2SocksAutoRoutes
	out.Tun2Socks.BypassCIDRs = SplitLines(f.Tun2SocksBypassCIDRs)

	return out, nil
}

// SplitLines turns a line- or comma-separated text-field value into a
// trimmed, empty-free slice (the inverse of strings.Join(..., "\n")).
func SplitLines(s string) []string {
	out := []string{}
	for _, line := range strings.FieldsFunc(s, func(r rune) bool { return r == '\n' || r == ',' }) {
		if v := strings.TrimSpace(line); v != "" {
			out = append(out, v)
		}
	}
	return out
}
