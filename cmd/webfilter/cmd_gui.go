package main

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	// Registers gg's GPU accelerator for the desktop window. Deliberately
	// imported by the command, not the gui package itself: the offscreen
	// snapshot tests under internal/gui need gg's CPU rasterizer, which this
	// global registration would otherwise hijack (rendering blank).
	_ "github.com/gogpu/gg/gpu"
	"github.com/spf13/cobra"

	"github.com/yjlion/gowebfilter/cmd/webfilter/internal/gui"
	"github.com/yjlion/gowebfilter/cmd/webfilter/internal/gui/mgmtclient"
	"github.com/yjlion/gowebfilter/internal/config"
	"github.com/yjlion/gowebfilter/internal/mgmtapi"
)

func newGuiCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "gui",
		Short: "Open the native desktop management UI",
		Long: "Opens a native desktop window for managing WebFilter. If no proxy/\n" +
			"management server is already running on the configured management port,\n" +
			"the GUI hosts one in-process (like `webfilter tray`); closing the window\n" +
			"then stops it. If a server is already running (service, `run`, or tray),\n" +
			"the GUI attaches to it over loopback HTTP and closing the window leaves\n" +
			"it untouched.",
	}
	f := addConfigFlags(cmd)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		return runGui(f.settingsPath)
	}
	return cmd
}

func runGui(settingsPath string) error {
	hideOwnConsoleWindow()

	if err := config.BootstrapRuntimeFiles(settingsPath); err != nil {
		return err
	}
	settings, err := config.LoadSettings(settingsPath)
	if err != nil {
		return err
	}
	mgmtAddr := net.JoinHostPort(loopbackHost(settings.MgmtHost), fmt.Sprint(settings.MgmtPort))
	mgmtURL := "http://" + mgmtAddr

	client, err := mgmtclient.New(mgmtURL)
	if err != nil {
		return err
	}

	opts := gui.Options{
		Client:      client,
		MgmtURL:     mgmtURL,
		OpenBrowser: openTarget,
	}

	// Same decision the tray makes: if nothing is serving the mgmt port,
	// host the proxy + management server in this process; otherwise attach
	// to whatever is already there (service, `run`, tray).
	if !mgmtReachable(mgmtAddr) {
		sup := &engineSupervisor{settingsPath: settingsPath, client: client, mgmtAddr: mgmtAddr}
		if err := sup.Start(); err != nil {
			return err
		}
		if !waitForMgmt(mgmtAddr, 10*time.Second) {
			// The engine goroutine reported (or will report) the real error
			// on the supervisor channel; the dashboard renders it. Still
			// open the window rather than dying silently on double-click.
			sup.reportf("management server did not come up on %s", mgmtAddr)
		}
		opts.SelfHosted = true
		opts.RestartEngine = sup.Restart
		opts.EngineErrors = sup.Errors()
	}

	return gui.Run(opts)
}

// waitForMgmt polls until the management port accepts connections so the
// GUI's first fetch doesn't race engine startup.
func waitForMgmt(addr string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if mgmtReachable(addr) {
			return true
		}
		time.Sleep(150 * time.Millisecond)
	}
	return false
}

// engineSupervisor owns the self-hosted proxy + management server for the
// lifetime of the GUI window and supports the settings screen's "restart
// engine" action. Front-end lifecycle glue only - the actual wiring stays in
// runProxyAndMgmtWith / internal/app.
type engineSupervisor struct {
	settingsPath string
	client       *mgmtclient.Client
	mgmtAddr     string

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
	errs   chan string
}

// Errors returns the channel on which engine failures are reported to the
// dashboard.
func (s *engineSupervisor) Errors() <-chan string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.errs == nil {
		s.errs = make(chan string, 8)
	}
	return s.errs
}

func (s *engineSupervisor) reportf(format string, args ...any) {
	s.mu.Lock()
	if s.errs == nil {
		s.errs = make(chan string, 8)
	}
	errs := s.errs
	s.mu.Unlock()
	select {
	case errs <- fmt.Sprintf(format, args...):
	default: // dashboard is behind; drop rather than block the engine path
	}
}

// Start builds the management server first (so the GUI's loopback client can
// be seeded with a valid session cookie before any request), then runs the
// combined proxy + mgmt process body on a background goroutine.
func (s *engineSupervisor) Start() error {
	mgmtSrv, err := mgmtapi.NewServer(s.settingsPath)
	if err != nil {
		return err
	}
	_ = s.client.SetSessionCookie(mgmtSrv.SessionCookie())

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	s.mu.Lock()
	s.cancel = cancel
	s.done = done
	s.mu.Unlock()

	go func() {
		defer close(done)
		if err := runProxyAndMgmtWith(ctx, s.settingsPath, mgmtSrv); err != nil && ctx.Err() == nil {
			s.reportf("engine stopped: %v", err)
		}
	}()
	return nil
}

// Restart stops the current engine, waits for the ports to be released, and
// starts a fresh one - the way self-hosted settings changes take effect.
func (s *engineSupervisor) Restart() error {
	s.mu.Lock()
	cancel, done := s.cancel, s.done
	s.mu.Unlock()
	if cancel != nil {
		cancel()
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			return fmt.Errorf("engine did not stop within 15s")
		}
	}
	// The mgmt listener closes asynchronously with Serve returning; wait for
	// the port to actually free up before rebinding.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && mgmtReachable(s.mgmtAddr) {
		time.Sleep(100 * time.Millisecond)
	}
	if err := s.Start(); err != nil {
		return err
	}
	if !waitForMgmt(s.mgmtAddr, 10*time.Second) {
		return fmt.Errorf("management server did not come back on %s", s.mgmtAddr)
	}
	return nil
}
