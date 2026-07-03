package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"

	"github.com/yjlion/gowebfilter/internal/classify/image"
	"github.com/yjlion/gowebfilter/internal/classify/textbayes"
	"github.com/yjlion/gowebfilter/internal/config"
	"github.com/yjlion/gowebfilter/internal/mgmtapi"
	"github.com/yjlion/gowebfilter/internal/proxy"
	"github.com/yjlion/gowebfilter/internal/proxy/addons"
	"github.com/yjlion/gowebfilter/internal/proxy/state"
	tun "github.com/yjlion/gowebfilter/internal/tun2socks"
)

// errNotImplemented marks subcommands whose real implementation lands in a
// later phase of the port (see the project plan's phased build order); it
// keeps the full CLI surface visible and buildable from Phase 0 onward.
func errNotImplemented(what string) error {
	return fmt.Errorf("%s: not implemented yet", what)
}

// buildProxyEngine wires a state.Runtime and the full addon pipeline into
// a ready-to-run proxy.Engine, in the exact registration order
// proxy/main.py uses in the Python original: management access and proxy
// auth gate management/API traffic first, then policy routing and MITM
// control, then the request-side filters (URL, DOH, safesearch), then the
// response-side filters (QUIC-blocking, YouTube, text/image
// classification), with request logging last so it observes the final
// decision.
func buildProxyEngine(settingsPath string) (*proxy.Engine, *state.Runtime, error) {
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
		addons.TextClassifier{Scorer: loadTextScorer()},
		addons.ImageClassifier{Detector: loadImageDetector()},
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

// runProxy starts only the forward-proxy engine (no management server).
func runProxy(ctx context.Context, settingsPath string) error {
	eng, rt, err := buildProxyEngine(settingsPath)
	if err != nil {
		return fmt.Errorf("start proxy engine: %w", err)
	}
	defer rt.Logs.Close()
	return runEngineWithTun(ctx, eng, rt)
}

// runMgmt starts only the management HTTP server (API + embedded UI).
func runMgmt(ctx context.Context, settingsPath string) error {
	srv, err := mgmtapi.NewServer(settingsPath)
	if err != nil {
		return fmt.Errorf("start management server: %w", err)
	}
	defer srv.Logs.Close()
	return serveMgmt(ctx, srv)
}

func serveMgmt(ctx context.Context, srv *mgmtapi.Server) error {
	addr := net.JoinHostPort(srv.Settings().MgmtHost, itoa(srv.Settings().MgmtPort))
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

// runProxyAndMgmt is `webfilter run`: starts the proxy engine and the
// management server as two goroutines in one process (see HANDOFF.md's
// process-model note), sharing nothing but the filesystem except for one
// in-process wire-up: a CA re-import via the management API clears the
// proxy's leaf-certificate cache immediately (mgmtapi.Server.OnCARotated),
// rather than requiring a restart, since both run in the same address
// space here. If either component fails, the other is cancelled too so
// `run` doesn't limp along half-up. Takes a bare context (rather than a
// *cobra.Command) so the Windows service handler can drive it directly,
// cancelling ctx when the SCM delivers a stop/shutdown control.
func runProxyAndMgmt(ctx context.Context, settingsPath string) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	eng, rt, err := buildProxyEngine(settingsPath)
	if err != nil {
		return fmt.Errorf("start proxy engine: %w", err)
	}
	defer rt.Logs.Close()

	mgmtSrv, err := mgmtapi.NewServer(settingsPath)
	if err != nil {
		return fmt.Errorf("start management server: %w", err)
	}
	defer mgmtSrv.Logs.Close()
	mgmtSrv.OnCARotated = rt.LeafIssuer.Clear

	errCh := make(chan error, 2)
	go func() { errCh <- runEngineWithTun(ctx, eng, rt) }()
	go func() { errCh <- serveMgmt(ctx, mgmtSrv) }()

	var firstErr error
	for i := 0; i < 2; i++ {
		if err := <-errCh; err != nil && firstErr == nil {
			firstErr = err
			cancel()
		}
	}
	return firstErr
}

func runEngineWithTun(ctx context.Context, eng *proxy.Engine, rt *state.Runtime) error {
	ensureTunSocksListener(eng)
	listeners, err := eng.Listen()
	if err != nil {
		return err
	}
	if rt != nil {
		rt.Start(ctx)
	}
	tunMgr := tun.NewManager(eng.Settings)
	if err := tunMgr.Start(ctx); err != nil {
		if tun.IsStartupSkipped(err) {
			slog.Warn("tun2socks not started", "err", err)
			return eng.Serve(ctx, listeners)
		}
		for _, ln := range listeners {
			_ = ln.Close()
		}
		return err
	}
	return eng.Serve(ctx, listeners)
}

func ensureTunSocksListener(eng *proxy.Engine) {
	if eng == nil || !eng.Settings.Tun2Socks.Enabled || eng.Settings.PrimarySocks5Port() != 0 {
		return
	}
	eng.Settings.ProxyListen = append(eng.Settings.ProxyListen, "socks5@127.0.0.1:1080")
	slog.Info("tun2socks: added local SOCKS5 listener for TUN capture", "addr", "127.0.0.1:1080")
}

// loadTextScorer loads the embedded pure-Go Bayesian adult-text scorer. It
// has no external model directory or native runtime dependency; if the
// embedded asset is ever corrupt, startup falls back to keyword-only rather
// than aborting the proxy.
func loadTextScorer() addons.MLScorer {
	m, err := textbayes.New()
	if err != nil {
		slog.Warn("text_classifier: failed to load embedded Bayesian scorer, falling back to keyword-only", "err", err)
		return nil
	}
	slog.Info("text_classifier: loaded embedded Bayesian scorer")
	return m
}

// loadImageDetector loads the embedded NSFW image detector
// (internal/classify/image - GantMan/nsfw_model, MIT-licensed, no CGO, no
// model download needed). It can only fail on a corrupt build, in which
// case it logs a warning and falls back to passthrough rather than
// aborting startup.
func loadImageDetector() addons.ImageDetector {
	d, err := image.New()
	if err != nil {
		slog.Warn("image_classifier: failed to load embedded NSFW detector, falling back to passthrough", "err", err)
		return nil
	}
	return d
}

func itoa(i int) string {
	return fmt.Sprintf("%d", i)
}
