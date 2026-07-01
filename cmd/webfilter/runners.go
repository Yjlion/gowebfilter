package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"

	"github.com/spf13/cobra"

	"github.com/yjlion/gowebfilter/internal/mgmtapi"
	"github.com/yjlion/gowebfilter/internal/proxy"
	"github.com/yjlion/gowebfilter/internal/proxy/addons"
	"github.com/yjlion/gowebfilter/internal/proxy/state"
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
		addons.TextClassifier{},  // ML scorer (Phase 8) not wired in yet: keyword-only
		addons.ImageClassifier{}, // NSFW detector (Phase 7) not wired in yet: passthrough
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
	return eng.Run(ctx)
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
// `run` doesn't limp along half-up.
func runProxyAndMgmt(cmd *cobra.Command, settingsPath string) error {
	ctx, cancel := context.WithCancel(cmd.Context())
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
	go func() { errCh <- eng.Run(ctx) }()
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

func itoa(i int) string {
	return fmt.Sprintf("%d", i)
}
