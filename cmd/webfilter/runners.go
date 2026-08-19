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
	// No management server in this process, so nothing consumes TUN status.
	return runEngineWithTun(ctx, eng, rt, nil)
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
	mgmtSrv, err := mgmtapi.NewServer(settingsPath)
	if err != nil {
		return fmt.Errorf("start management server: %w", err)
	}
	return runProxyAndMgmtWith(ctx, settingsPath, mgmtSrv)
}

// runProxyAndMgmtWith is runProxyAndMgmt with a caller-constructed management
// server: the `gui` command needs the *mgmtapi.Server before serving starts
// (to mint its loopback session cookie via SessionCookie), so it builds the
// server itself and hands it in. Takes ownership of mgmtSrv, including
// closing its log store.
func runProxyAndMgmtWith(ctx context.Context, settingsPath string, mgmtSrv *mgmtapi.Server) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	eng, rt, err := app.BuildProxyEngine(settingsPath)
	if err != nil {
		mgmtSrv.Logs.Close()
		return fmt.Errorf("start proxy engine: %w", err)
	}
	defer rt.Logs.Close()

	defer mgmtSrv.Logs.Close()
	mgmtSrv.OnCARotated = rt.LeafIssuer.Clear
	// Both components run here, so /api/tun2socks/status can report the live
	// process rather than just what settings say. The engine publishes the
	// supervisor into this ref once its listeners are bound.
	var tunRef tun.Ref
	mgmtSrv.Tun2Socks = &tunRef

	errCh := make(chan error, 2)
	go func() { errCh <- runEngineWithTun(ctx, eng, rt, &tunRef) }()
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

// runEngineWithTun serves the proxy listeners, supervising the external
// tun2socks process (root + `ip`/`netsh` driven) when TUN capture is enabled.
// The Android port does not use this path — mobile/ drives the tun2socks
// library in-process from the VpnService file descriptor, which needs no
// elevation.
//
// Order matters: the dedicated SOCKS5 listener must be bound before tun2socks
// starts, both because the supervisor needs its actual (OS-assigned) address
// and because tun2socks would otherwise have nowhere to send captured traffic.
func runEngineWithTun(ctx context.Context, eng *proxy.Engine, rt *state.Runtime, tunRef *tun.Ref) error {
	app.EnsureTunSocksListener(eng)
	listeners, err := eng.Listen()
	if err != nil {
		return err
	}
	if rt != nil {
		rt.Start(ctx)
	}

	sup := tun.NewSupervisor(eng.Settings, proxy.FindPurpose(listeners, app.TunSocksListenerPurpose))
	if tunRef != nil {
		tunRef.Set(sup)
	}
	// Mark the engine's own outbound sockets before capture can route anything,
	// not after: the moment the TUN default route exists, an unmarked upstream
	// fetch is captured and handed straight back to this engine. The mark is
	// inert until tun2socks installs the matching `ip rule`, so setting it up
	// front costs nothing if capture then turns out to be skipped.
	proxy.SetUpstreamEgressMark(tun.EgressMark)
	defer proxy.SetUpstreamEgressMark(0)
	if err := sup.Start(ctx); err != nil {
		if tun.IsStartupSkipped(err) {
			// TUN capture is an add-on to the proxy, not a prerequisite: an
			// unelevated or binary-less run still filters configured clients.
			slog.Warn("tun2socks not started", "err", err)
			proxy.SetUpstreamEgressMark(0)
			return eng.Serve(ctx, listeners)
		}
		for _, ln := range listeners {
			_ = ln.Close()
		}
		return err
	}
	return eng.Serve(ctx, listeners)
}
