package uimodel

import (
	"fmt"
	"strings"
	"sync"

	"github.com/yjlion/gowebfilter/cmd/webfilter/internal/gui/mgmtclient"
)

// StatusModel holds the dashboard's last-known engine status. Written by the
// poll goroutine, read by draw callbacks.
type StatusModel struct {
	mu     sync.Mutex
	st     mgmtclient.Status
	err    string
	loaded bool
}

// Set stores a successful status fetch.
func (m *StatusModel) Set(st mgmtclient.Status) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.st = st
	m.err = ""
	m.loaded = true
}

// SetError records a failed status fetch (previous data stays visible).
func (m *StatusModel) SetError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.err = err.Error()
}

// Get returns the last status, the last fetch error (empty when the last
// fetch succeeded), and whether any fetch has succeeded yet.
func (m *StatusModel) Get() (mgmtclient.Status, string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.st, m.err, m.loaded
}

// RunningLabel renders the proxy state line.
func (m *StatusModel) RunningLabel() string {
	st, _, loaded := m.Get()
	if !loaded {
		return "Connecting..."
	}
	if st.ProxyRunning {
		return "Proxy running"
	}
	return "Proxy not running"
}

// ListenersLabel renders the configured listener list.
func (m *StatusModel) ListenersLabel() string {
	st, _, loaded := m.Get()
	if !loaded || len(st.ProxyListen) == 0 {
		return ""
	}
	return "Listeners: " + strings.Join(st.ProxyListen, ", ")
}

// MgmtLabel renders the management port line.
func (m *StatusModel) MgmtLabel() string {
	st, _, loaded := m.Get()
	if !loaded {
		return ""
	}
	return fmt.Sprintf("Management API on port %d", st.MgmtPort)
}

// Tun2SocksLabel summarizes the tun2socks block of /api/status.
func (m *StatusModel) Tun2SocksLabel() string {
	st, _, loaded := m.Get()
	if !loaded || st.Tun2Socks == nil {
		return ""
	}
	enabled, _ := st.Tun2Socks["enabled"].(bool)
	if !enabled {
		return "tun2socks: disabled"
	}
	if running, _ := st.Tun2Socks["running"].(bool); running {
		return "tun2socks: running"
	}
	return "tun2socks: enabled (not running)"
}

// ErrorLabel renders the last fetch error, or "" when healthy.
func (m *StatusModel) ErrorLabel() string {
	_, errMsg, _ := m.Get()
	if errMsg == "" {
		return ""
	}
	return "Status fetch failed: " + errMsg
}

// RecentBlockRows converts the status payload's recent blocks to display
// rows (same normalization as the logs screen).
func (m *StatusModel) RecentBlockRows() []LogRow {
	st, _, _ := m.Get()
	rows := make([]LogRow, len(st.RecentBlocks))
	for i, e := range st.RecentBlocks {
		rows[i] = FormatLogRow("blocks", e)
	}
	return rows
}
