// Package app single-sources the wiring of a runnable webfilter engine so
// every front-end (the desktop/server CLI in cmd/webfilter and the gomobile
// bindings in mobile/) constructs the exact same addon pipeline. The
// registration order below is load-bearing and mirrors proxy/main.py in the
// Python original — do not fork it per platform.
package app

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/yjlion/gowebfilter/internal/certs"

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

// ServeMgmt runs the management server (API + embedded UI) until ctx is
// cancelled, over HTTPS when mgmt_tls is set and plain HTTP otherwise.
func ServeMgmt(ctx context.Context, srv *mgmtapi.Server) error {
	cfg := srv.Settings()
	addr := net.JoinHostPort(cfg.MgmtHost, strconv.Itoa(cfg.MgmtPort))

	tlsCfg, err := mgmtTLSConfig(srv)
	if err != nil {
		return err
	}
	scheme := "http"
	if tlsCfg != nil {
		scheme = "https"
	}
	slog.Info("management server listening", "addr", addr, "scheme", scheme)

	httpSrv := &http.Server{Addr: addr, Handler: srv.Router(), TLSConfig: tlsCfg}
	go func() {
		<-ctx.Done()
		// Give in-flight requests a moment to finish before dropping them.
		// This used to be an unconditional Close(); with TLS in play a hard
		// close also aborts handshakes in progress.
		shutdownCtx, cancel := context.WithTimeout(context.Background(), mgmtShutdownGrace)
		defer cancel()
		if err := httpSrv.Shutdown(shutdownCtx); err != nil {
			_ = httpSrv.Close()
		}
	}()

	if tlsCfg != nil {
		// Certificates come from TLSConfig (either a loaded key pair or the
		// CA-minted GetCertificate callback), so both path arguments are
		// empty by design.
		err = httpSrv.ListenAndServeTLS("", "")
	} else {
		err = httpSrv.ListenAndServe()
	}
	if err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// mgmtShutdownGrace bounds how long a management-server shutdown waits for
// in-flight requests. Short: nothing on this server is long-running except
// log exports, and `run` cancels the proxy engine at the same time.
const mgmtShutdownGrace = 5 * time.Second

// mgmtTLSConfig returns the management server's TLS config, or nil for plain
// HTTP.
//
// Two certificate sources. An explicit mgmt_cert_file/mgmt_key_file pair is
// used as-is - that is the path for a publicly trusted certificate, and it
// is the one that avoids the bootstrapping problem below. Otherwise leaves
// are minted on demand by the runtime CA, exactly as TLS-wrapped proxy
// listeners do (certs.ServerTLSConfig is shared with Engine.proxyTLSConfig).
//
// Worth stating plainly, because it surprises people: with a CA-minted
// certificate, GET /api/ca-cert is served over HTTPS signed by the very CA
// the client has not installed yet, so the first fetch warns; and WPAD
// clients will not fetch /proxy.pac from an endpoint they do not trust.
// Install the CA out of band, or use a real certificate, or leave mgmt_tls
// off if PAC distribution over this port matters more.
func mgmtTLSConfig(srv *mgmtapi.Server) (*tls.Config, error) {
	cfg := srv.Settings()
	if !cfg.MgmtTLS || srv.ForcePlaintext {
		return nil, nil
	}

	certFile := strings.TrimSpace(cfg.MgmtCertFile)
	keyFile := strings.TrimSpace(cfg.MgmtKeyFile)
	if certFile != "" && keyFile != "" {
		cert, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			return nil, fmt.Errorf("management TLS certificate: %w", err)
		}
		return &tls.Config{
			Certificates: []tls.Certificate{cert},
			NextProtos:   []string{"http/1.1"},
		}, nil
	}

	issuer, err := srv.TLSLeafIssuer()
	if err != nil {
		return nil, fmt.Errorf("management TLS: %w", err)
	}
	return certs.ServerTLSConfig(issuer, "webfilter-mgmt"), nil
}

// MgmtURL renders the base URL the management server is reachable at, so
// callers that hand a URL to a browser or an HTTP client (the tray, the
// native GUI, the Android bridge) agree on the scheme instead of each
// hardcoding "http://".
func MgmtURL(cfg models.GlobalSettings, host string, forcePlaintext bool) string {
	scheme := "http"
	if cfg.MgmtTLS && !forcePlaintext {
		scheme = "https"
	}
	return scheme + "://" + net.JoinHostPort(host, strconv.Itoa(cfg.MgmtPort))
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
