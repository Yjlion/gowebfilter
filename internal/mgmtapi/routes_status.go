package mgmtapi

import (
	"net"
	"net/http"
	"strconv"
	"time"

	tun "github.com/yjlion/gowebfilter/internal/tun2socks"
)

type statusResponse struct {
	ProxyRunning   bool             `json:"proxy_running"`
	ProxyPort      int              `json:"proxy_port"`
	ProxyListen    []string         `json:"proxy_listen"`
	MgmtPort       int              `json:"mgmt_port"`
	RecentBlocks   []map[string]any `json:"recent_blocks"`
	RecentRequests []map[string]any `json:"recent_requests"`
	Tun2Socks      tun.Status       `json:"tun2socks"`
}

const recentActivityLimit = 50

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	cfg := s.Settings()
	port := cfg.PrimaryProxyPort()

	writeJSON(w, http.StatusOK, statusResponse{
		ProxyRunning:   isPortOpen(port),
		ProxyPort:      port,
		ProxyListen:    cfg.ProxyListen,
		MgmtPort:       cfg.MgmtPort,
		RecentBlocks:   s.Logs.Tail("blocks", recentActivityLimit),
		RecentRequests: s.Logs.Tail("requests", recentActivityLimit),
		Tun2Socks:      s.tunStatus(),
	})
}

func (s *Server) handleTun2SocksStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.tunStatus())
}

// tunStatus reports live TUN-capture state when the proxy engine shares this
// process, and the settings/filesystem view otherwise. A nil Ref handles the
// standalone-`mgmt` case itself.
func (s *Server) tunStatus() tun.Status {
	return s.Tun2Socks.Status(s.Settings())
}

// handleTun2SocksDownload installs the official tun2socks binary beside the
// webfilter executable. It writes an executable fetched over the network, so it
// is registered behind requireUnlocked with the other config mutations.
func (s *Server) handleTun2SocksDownload(w http.ResponseWriter, r *http.Request) {
	dir, err := tun.InstallDir()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "cannot locate the install directory: "+err.Error())
		return
	}
	meta, err := tun.Download(r.Context(), dir, "")
	if err != nil {
		// Upstream availability, rate limits and checksum mismatches are all
		// outside this server's control, so report them as a bad gateway with
		// the underlying reason rather than a generic 500.
		writeJSONError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok",
		"binary": meta,
	})
}

// isPortOpen checks whether something is already listening on 127.0.0.1 (or
// ::1) at port - the proxy_running signal, matching the Python original's
// approach of probing the port rather than tracking a PID (works whether
// the proxy runs in this same process via `run` or as a separate `proxy`
// process).
func isPortOpen(port int) bool {
	portStr := strconv.Itoa(port)
	for _, host := range []string{"127.0.0.1", "::1"} {
		conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, portStr), 300*time.Millisecond)
		if err == nil {
			conn.Close()
			return true
		}
	}
	return false
}
