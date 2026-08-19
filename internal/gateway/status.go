package gateway

import (
	"fmt"
	"net"
	"runtime"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/yjlion/gowebfilter/internal/models"
)

// shutdownTimeout bounds the teardown commands. Shutdown runs while the service
// is stopping, so it must not be able to hang it.
const shutdownTimeout = 10 * time.Second

// Status is what GET /api/status and GET /api/gateway/status report. It
// separates the three questions the UI has to answer: is it configured, can it
// run here, and is it running.
type Status struct {
	Configured bool `json:"configured"`
	Enabled    bool `json:"enabled"`
	Active     bool `json:"active"`
	Supported  bool `json:"supported"`

	Platform       string `json:"platform"`
	Interface      string `json:"interface,omitempty"`
	InterceptPorts []int  `json:"intercept_ports"`
	DropQUIC       bool   `json:"drop_quic"`
	IPForward      bool   `json:"ip_forward"`

	Privilege   string `json:"privilege"`
	PrivilegeOK bool   `json:"privilege_ok"`
	NftPresent  bool   `json:"nft_present"`
	NftPath     string `json:"nft_path,omitempty"`

	// TransparentAddr is the engine-owned listener the redirect points at.
	// Read-only: it is bound on an OS-assigned port so it can never collide
	// with a user listener, and the rules are written from what it actually got.
	TransparentAddr string `json:"transparent_addr,omitempty"`
	TransparentPort int    `json:"transparent_port,omitempty"`

	SysctlsChanged []string `json:"sysctls_changed,omitempty"`
	LastError      string   `json:"last_error,omitempty"`
}

// Inspect reports what can be known from settings and the filesystem alone,
// with no running manager - which is what standalone `webfilter mgmt` answers
// from.
func Inspect(settings models.GlobalSettings) Status {
	cfg := settings.Gateway
	privOK, priv := HasNetAdmin()
	st := Status{
		Configured:     true,
		Enabled:        cfg.Enabled,
		Supported:      runtime.GOOS == "linux",
		Platform:       runtime.GOOS,
		Interface:      cfg.Interface,
		InterceptPorts: cfg.InterceptPorts,
		DropQUIC:       cfg.DropQUIC,
		IPForward:      cfg.IPForward,
		Privilege:      priv,
		PrivilegeOK:    privOK,
	}
	if p := nftPath(); p != "" {
		st.NftPresent, st.NftPath = true, p
	}

	// One actionable message, most fundamental first, rather than a list the
	// user has to triage.
	switch {
	case !st.Supported:
		st.LastError = "Gateway mode needs netfilter and is implemented for Linux only."
	case !st.NftPresent:
		st.LastError = "The nftables CLI (nft) is not installed. On Debian/Ubuntu: apt install nftables."
	case cfg.Enabled && !privOK:
		st.LastError = "root or CAP_NET_ADMIN is required to install the redirect rules. " +
			"Under systemd, add AmbientCapabilities=CAP_NET_ADMIN CAP_NET_RAW - see packaging/README.md."
	case cfg.Enabled:
		if err := ValidateConfig(cfg, settings.MgmtPort); err != nil {
			st.LastError = err.Error()
		}
	}
	return st
}

// Ref is a shareable slot for the live Manager, so the management server can
// report the running state of an engine that is constructed after it.
// The zero Ref reports the settings view, which is what `webfilter mgmt` - a
// process that manages nothing - wants.
type Ref struct{ p atomic.Pointer[Manager] }

func (r *Ref) Set(m *Manager) { r.p.Store(m) }

func (r *Ref) Status(settings models.GlobalSettings) Status {
	if r != nil {
		if m := r.p.Load(); m != nil {
			return m.Status()
		}
	}
	return Inspect(settings)
}

func portOfAddr(addr string) (int, error) {
	if addr == "" {
		return 0, fmt.Errorf("no transparent listener address was provided")
	}
	_, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return 0, fmt.Errorf("transparent listener address %q: %w", addr, err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 {
		return 0, fmt.Errorf("transparent listener address %q has no usable port", addr)
	}
	return port, nil
}
