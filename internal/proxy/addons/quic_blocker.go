package addons

import (
	"log/slog"

	"github.com/yjlion/gowebfilter/internal/proxy"
)

// QuicBlocker strips the Alt-Svc response header to prevent QUIC (HTTP/3)
// bypass, ported from proxy/addons/quic_blocker.py.
//
// Browsers (especially Chrome) use Alt-Svc to discover that a server
// supports HTTP/3 over QUIC (UDP/443); once discovered, Chrome speaks QUIC
// directly to that server, bypassing the TCP/TLS proxy entirely -
// defeating URL filtering, SafeSearch, and YouTube channel blocking.
// Removing Alt-Svc forces the browser to stay on TCP/TLS where the proxy
// can intercept it. Enabled per-policy via url_filter.block_quic.
type QuicBlocker struct{}

func (QuicBlocker) Name() string { return "quic_blocker" }

func (QuicBlocker) HandleResponse(fc *proxy.FlowContext) {
	if fc.URLAllowed || fc.MitmPassthrough {
		return
	}
	policy := fc.Policy
	if policy == nil || !policy.UrlFilter.BlockQuic {
		return
	}
	if fc.Response == nil {
		return
	}
	if fc.Response.Header.Get("Alt-Svc") != "" {
		fc.Response.Header.Del("Alt-Svc")
		slog.Debug("quic_blocker: stripped Alt-Svc", "host", fc.Request.URL.Hostname())
	}
}
