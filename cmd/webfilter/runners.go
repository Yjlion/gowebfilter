package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"

	"github.com/spf13/cobra"

	"github.com/yjlion/gowebfilter/internal/mgmtapi"
)

// errNotImplemented marks subcommands whose real implementation lands in a
// later phase of the port (see the project plan's phased build order); it
// keeps the full CLI surface visible and buildable from Phase 0 onward.
func errNotImplemented(what string) error {
	return fmt.Errorf("%s: not implemented yet", what)
}

// runMgmt starts only the management HTTP server (API + embedded UI).
func runMgmt(ctx context.Context, settingsPath string) error {
	srv, err := mgmtapi.NewServer(settingsPath)
	if err != nil {
		return fmt.Errorf("start management server: %w", err)
	}
	defer srv.Logs.Close()

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

// runProxyAndMgmt is `webfilter run`. The proxy engine lands in Phase 4/5
// of the project plan; until then this starts only the management server
// so the UI is usable end-to-end for Phase 1-3 verification.
func runProxyAndMgmt(cmd *cobra.Command, settingsPath string) error {
	slog.Warn("proxy engine not implemented yet - starting management server only")
	return runMgmt(cmd.Context(), settingsPath)
}

func itoa(i int) string {
	return fmt.Sprintf("%d", i)
}
