// Package app single-sources the wiring of a runnable webfilter engine so
// every front-end (the desktop/server CLI in cmd/webfilter and the gomobile
// bindings in mobile/) constructs the exact same addon pipeline. The
// registration order below is load-bearing and mirrors proxy/main.py in the
// Python original — do not fork it per platform.
package app

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"strconv"

	"github.com/yjlion/gowebfilter/internal/classify/image"
	"github.com/yjlion/gowebfilter/internal/classify/textbayes"
	"github.com/yjlion/gowebfilter/internal/config"
	"github.com/yjlion/gowebfilter/internal/mgmtapi"
	"github.com/yjlion/gowebfilter/internal/models"
	"github.com/yjlion/gowebfilter/internal/proxy"
	"github.com/yjlion/gowebfilter/internal/proxy/addons"
	"github.com/yjlion/gowebfilter/internal/proxy/state"
)

// BuildProxyEngine wires a state.Runtime and the full addon pipeline into
// a ready-to-run proxy.Engine, in the exact registration order
// proxy/main.py uses in the Python original: management access and proxy
// auth gate management/API traffic first, then policy routing and MITM
// control, then the request-side filters (URL, DOH, safesearch), then the
// response-side filters (QUIC-blocking, YouTube, text/image
// classification), with request logging last so it observes the final
// decision.
func BuildProxyEngine(settingsPath string) (*proxy.Engine, *state.Runtime, error) {
	if err := config.BootstrapRuntimeFiles(settingsPath); err != nil {
		return nil, nil, err
	}
	rt, err := state.New(settingsPath)
	if err != nil {
		return nil, nil, err
	}

	authGate := addons.NewProxyAuthGate(rt)
	pipeline := proxy.NewPipeline([]proxy.Addon{
		addons.ManagementAccess{},
		authGate,
		addons.PolicyRouter{},
		addons.MitmControl{},
		addons.UrlFilter{},
		addons.QuicBlocker{},
		addons.DohFilter{},
		addons.SafeSearch{},
		addons.YouTubeFilter{},
		addons.TextClassifier{Scorer: LoadTextScorer()},
		addons.ImageClassifier{Detector: LoadImageDetector()},
		addons.RequestLogger{},
	})

	eng := &proxy.Engine{
		SettingsPath: settingsPath,
		Settings:     rt.Settings,
		Runtime:      rt,
		Pipeline:     pipeline,
		Transport:    proxy.NewTransport(),
	}
	return eng, rt, nil
}

// TunSocksListenerPurpose tags the engine-owned SOCKS5 listener that TUN
// capture funnels traffic into.
const TunSocksListenerPurpose = "tun2socks"

// EnsureTunSocksListener registers a dedicated loopback SOCKS5 listener when
// tun2socks is enabled, giving TUN-captured traffic an entry point into the
// MITM path.
//
// It is deliberately not a proxy_listen entry and not user-configurable.
// tun2socks needs a SOCKS5 endpoint (it relies on UDP ASSOCIATE for DNS), so
// every other choice a user could make here - an HTTP listener, a port nothing
// is bound to, a remote address - is simply wrong, and the old free-text
// `proxy_target` setting made those the easy mistakes to make. Binding port 0
// lets the OS pick a free port, so the dedicated listener can never collide
// with a user-configured one; the caller reads the real address back off the
// bound listener (proxy.FindPurpose) and hands it to the supervisor.
func EnsureTunSocksListener(eng *proxy.Engine) {
	if eng == nil || !eng.Settings.Tun2Socks.Enabled {
		return
	}
	for _, internal := range eng.InternalListen {
		if internal.Purpose == TunSocksListenerPurpose {
			return
		}
	}
	eng.InternalListen = append(eng.InternalListen, proxy.InternalListener{
		Purpose: TunSocksListenerPurpose,
		Spec:    "socks5@127.0.0.1:0",
	})
}

// GatewayListenerPurpose tags the engine-owned transparent listener that
// gateway mode redirects other machines' traffic into.
const GatewayListenerPurpose = "gateway"

// EnsureGatewayListener registers the transparent listener gateway mode needs.
//
// It binds 0.0.0.0 because netfilter REDIRECT rewrites the destination to an
// address of the *incoming* interface, not to loopback - a listener bound to
// 127.0.0.1 would never be reached. Port 0 lets the OS choose, so the listener
// can never collide with a user-configured one; the caller reads the real
// address back with proxy.FindPurpose and writes the nftables rules from that,
// which is what stops the rules ever naming a port nothing is serving.
//
// Like the TUN capture listener this is deliberately not a proxy_listen entry:
// it never appears in settings.json or the UI's listener editor, and there is
// nothing useful a user could change about it.
func EnsureGatewayListener(eng *proxy.Engine) {
	if eng == nil || !eng.Settings.Gateway.Enabled {
		return
	}
	for _, internal := range eng.InternalListen {
		if internal.Purpose == GatewayListenerPurpose {
			return
		}
	}
	eng.InternalListen = append(eng.InternalListen, proxy.InternalListener{
		Purpose: GatewayListenerPurpose,
		Spec:    "transparent@0.0.0.0:0",
	})
}

// EnsureLocalHTTPProxyListener appends a loopback HTTP ("regular") proxy
// listener when none is configured, so a PAC file has an HTTP proxy to
// point at. The 8080 fallback deliberately matches
// GlobalSettings.PrimaryRegularProxyPort, keeping the advertised PAC port
// and the bound listener in agreement. Session-only: the injected entry is
// never persisted to settings.json.
func EnsureLocalHTTPProxyListener(eng *proxy.Engine) {
	if eng == nil {
		return
	}
	for _, entry := range eng.Settings.ProxyListen {
		if spec := models.ParseListenSpec(entry); spec.Mode == "regular" && !spec.TLS {
			return // PAC advertises this plaintext HTTP listener's port
		}
	}
	eng.Settings.ProxyListen = append(eng.Settings.ProxyListen, "regular@127.0.0.1:8080")
	slog.Info("proxy-only: added local HTTP proxy listener for PAC clients", "addr", "127.0.0.1:8080")
}

// ServeMgmt runs the management HTTP server (API + embedded UI) until ctx is
// cancelled.
func ServeMgmt(ctx context.Context, srv *mgmtapi.Server) error {
	addr := net.JoinHostPort(srv.Settings().MgmtHost, strconv.Itoa(srv.Settings().MgmtPort))
	slog.Info("management server listening", "addr", addr)

	httpSrv := &http.Server{Addr: addr, Handler: srv.Router()}
	go func() {
		<-ctx.Done()
		_ = httpSrv.Close()
	}()
	if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// LoadTextScorer loads the embedded pure-Go Bayesian adult-text scorer. It
// has no external model directory or native runtime dependency; if the
// embedded asset is ever corrupt, startup falls back to keyword-only rather
// than aborting the proxy.
func LoadTextScorer() addons.MLScorer {
	m, err := textbayes.New()
	if err != nil {
		slog.Warn("text_classifier: failed to load embedded Bayesian scorer, falling back to keyword-only", "err", err)
		return nil
	}
	slog.Info("text_classifier: loaded embedded Bayesian scorer")
	return m
}

// LoadImageDetector loads the embedded NSFW image detector
// (internal/classify/image - GantMan/nsfw_model, MIT-licensed, no CGO, no
// model download needed). It can only fail on a corrupt build, in which
// case it logs a warning and falls back to passthrough rather than
// aborting startup.
func LoadImageDetector() addons.ImageDetector {
	d, err := image.New()
	if err != nil {
		slog.Warn("image_classifier: failed to load embedded NSFW detector, falling back to passthrough", "err", err)
		return nil
	}
	return d
}
