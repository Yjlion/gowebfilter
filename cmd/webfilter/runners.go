package main

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/yjlion/gowebfilter/internal/app"
	"github.com/yjlion/gowebfilter/internal/mgmtapi"
	"github.com/yjlion/gowebfilter/internal/proxy"
	"github.com/yjlion/gowebfilter/internal/proxy/state"
	tun "github.com/yjlion/gowebfilter/internal/tun2socks"
)

// errNotImplemented marks subcommands whose real implementation lands in a
// later phase of the port (see the project plan's phased build order); it
// keeps the full CLI surface visible and buildable from Phase 0 onward.
func errNotImplemented(what string) error {
	return fmt.Errorf("%s: not implemented yet", what)
}

// runProxy starts only the forward-proxy engine (no management server).
func runProxy(ctx context.Context, settingsPath string) error {
	eng, rt, err := app.BuildProxyEngine(settingsPath)
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
	return app.ServeMgmt(ctx, srv)
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

	eng, rt, err := app.BuildProxyEngine(settingsPath)
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
	go func() { errCh <- app.ServeMgmt(ctx, mgmtSrv) }()

	var firstErr error
	for i := 0; i < 2; i++ {
		if err := <-errCh; err != nil && firstErr == nil {
			firstErr = err
			cancel()
		}
	}
	return firstErr
}

// runEngineWithTun serves the proxy listeners, with the desktop/server
// tun2socks manager (root + `ip`-command driven) layered on when enabled.
// The Android port does not use this path — mobile/ drives tun2socks
// directly from the VpnService file descriptor.
func runEngineWithTun(ctx context.Context, eng *proxy.Engine, rt *state.Runtime) error {
	app.EnsureTunSocksListener(eng)
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
