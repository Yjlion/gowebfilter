// Package gui is the native desktop management UI (gogpu/ui, pure Go, no
// CGO). It is strictly a front-end: every read and write goes through
// mgmtclient to the management HTTP API - never directly to disk - so the
// server-side coherence rules (MDM lock, audit log, validation, hot reload)
// apply identically to the web UI, the mobile app, and this window.
//
// This package lives under cmd/webfilter/internal deliberately: the Android
// build sweep compiles ./mobile ./internal/... and must never pull in the
// gogpu windowing stack.
package gui

import (
	"errors"
	"time"

	"github.com/gogpu/gogpu"
	uiapp "github.com/gogpu/ui/app"
	"github.com/gogpu/ui/core/tabview"
	"github.com/gogpu/ui/desktop"
	"github.com/gogpu/ui/primitives"
	"github.com/gogpu/ui/state"
	"github.com/gogpu/ui/theme/material3"
	"github.com/gogpu/ui/widget"

	"github.com/yjlion/gowebfilter/cmd/webfilter/internal/gui/mgmtclient"
	"github.com/yjlion/gowebfilter/cmd/webfilter/internal/gui/uimodel"
)

// Options wires the window to its environment; cmd_gui.go fills it in.
type Options struct {
	// Client talks to the management API (self-hosted or external).
	Client *mgmtclient.Client
	// MgmtURL is what "Open Web UI" opens in the browser.
	MgmtURL string
	// SelfHosted is true when this process hosts the engine; closing the
	// window then stops filtering, and the dashboard says so.
	SelfHosted bool
	// RestartEngine restarts the in-process engine (self-host only, nil
	// otherwise) - how settings changes take effect without relaunching.
	RestartEngine func() error
	// EngineErrors surfaces self-hosted engine failures on the dashboard
	// (nil when attached).
	EngineErrors <-chan string
	// OpenBrowser opens a URL in the system browser.
	OpenBrowser func(string) error
}

const (
	tabDashboard = 0
	tabPolicies  = 1
	tabLogs      = 2
	tabSettings  = 3
)

// scaleNeutralWindow wraps the gogpu App as the ui layer's WindowProvider
// but reports a DPI scale of 1.0. Size() stays logical (inherited), so the
// widget tree lays out and paints purely in logical coordinates and the
// single logical->physical scale is applied once, by the gg canvas.
type scaleNeutralWindow struct {
	*gogpu.App
}

func (w scaleNeutralWindow) ScaleFactor() float64 { return 1.0 }

type ui struct {
	opts     Options
	gogpuApp *gogpu.App
	uiApp    *uiapp.App
	m3       *material3.Theme

	activeTab     state.Signal[int]
	engineBanner  state.Signal[string]
	restartNeeded state.Signal[bool]

	dash  *dashboardScreen
	pols  *policiesScreen
	logs  *logsScreen
	sets  *settingsScreen
	login *loginController
}

// Run opens the window and blocks until it is closed. Must be called on the
// main goroutine (OS windowing requirement); the engine, when self-hosted,
// already runs on background goroutines.
func Run(o Options) error {
	u := &ui{
		opts:          o,
		m3:            material3.New(widget.Hex(0x2563EB)),
		activeTab:     state.NewSignal(tabDashboard),
		engineBanner:  state.NewSignal(""),
		restartNeeded: state.NewSignal(false),
	}

	u.gogpuApp = gogpu.NewApp(gogpu.DefaultConfig().
		WithTitle("WebFilter").
		WithSize(1100, 780))
	u.uiApp = uiapp.New(
		// scaleNeutralWindow: report scale 1.0 to the widget layer. The gg
		// canvas underneath already maps logical->physical by the real
		// DPI scale; letting the ui layer see it too makes it scale its
		// scenes a second time (1.5 * 1.5 on a 150% display), drawing the
		// UI far past the window edges.
		uiapp.WithWindowProvider(scaleNeutralWindow{u.gogpuApp}),
		uiapp.WithPlatformProvider(u.gogpuApp),
		uiapp.WithEventSource(u.gogpuApp.EventSource()),
		uiapp.WithTheme(u.m3.AsTheme()),
	)

	u.dash = newDashboardScreen(u)
	u.pols = newPoliciesScreen(u)
	u.logs = newLogsScreen(u)
	u.sets = newSettingsScreen(u)
	u.login = newLoginController(u)

	u.uiApp.SetRoot(u.buildRoot())
	u.startBackground()
	return desktop.Run(u.gogpuApp, u.uiApp)
}

func (u *ui) buildRoot() widget.Widget {
	header := primitives.HBox(
		primitives.Text("WebFilter").FontSize(20).Bold(),
		primitives.TextFn(func() string {
			if u.opts.SelfHosted {
				return "engine hosted by this window — closing it stops filtering"
			}
			return "attached to running instance at " + u.opts.MgmtURL
		}).FontSize(12).Color(widget.RGBA8(90, 90, 100, 255)),
		primitives.Expanded(primitives.Box()),
		u.btnOutlined("Open Web UI", func() { _ = u.opts.OpenBrowser(u.opts.MgmtURL) }),
	).Padding(12).Gap(16).CrossAlign(primitives.CrossAxisCenter)

	tabs := tabview.New(
		[]tabview.Tab{
			{Label: "Dashboard", Content: u.dash.build()},
			{Label: "Policies", Content: u.pols.build()},
			{Label: "Logs", Content: u.logs.build()},
			{Label: "Settings", Content: u.sets.build()},
		},
		tabview.SelectedSignalOpt(u.activeTab),
		tabview.OnSelect(u.onTabSelected),
		tabview.PainterOpt(material3.TabViewPainter{Theme: u.m3}),
	)

	return primitives.VBox(
		header,
		errorText(u.engineBanner.Get),
		primitives.Expanded(tabs),
	)
}

func (u *ui) onTabSelected(idx int) {
	switch idx {
	case tabDashboard:
		go u.dash.refresh()
	case tabPolicies:
		go u.pols.refresh()
	case tabLogs:
		go u.logs.poll()
	case tabSettings:
		go u.sets.reload()
	}
}

// redraw asks the windowing layer for a frame; every background data change
// funnels through here (signals alone don't wake the demand-driven loop).
//
// It also forces a full relayout: async data arrivals (listview rows, a
// swapped policy editor, refilled settings fields) change widget content
// without any widget marking the window needsLayout, so the demand-driven
// Frame() would redraw the stale layout - e.g. a listview whose row cache
// was invalidated but never rebuilt shows nothing. Marking the root
// needs-layout is cheap for an on-demand management UI and keeps every
// screen coherent without per-widget bookkeeping.
//
// gogpuApp is nil under the offscreen snapshot tests, which drive layout
// directly and have no window to invalidate.
func (u *ui) redraw() {
	if u.gogpuApp == nil {
		return
	}
	if root := u.uiApp.Window().Root(); root != nil {
		if m, ok := root.(interface{ MarkNeedsLayout() }); ok {
			m.MarkNeedsLayout()
		}
	}
	u.gogpuApp.RequestRedraw()
}

// wctx returns the widget context dialogs need for overlay display.
func (u *ui) wctx() widget.Context { return u.uiApp.Window().Context() }

// handleAuthErr routes ErrUnauthorized to the login dialog. Returns true if
// the error was an auth error (so callers skip their own error display).
func (u *ui) handleAuthErr(err error) bool {
	if errors.Is(err, mgmtclient.ErrUnauthorized) {
		u.login.show()
		return true
	}
	return false
}

// startBackground runs the poll loop: an immediate status fetch, then a 2s
// ticker that only fetches for the visible tab (status every other tick),
// plus the self-hosted engine error channel.
func (u *ui) startBackground() {
	go func() {
		// First contact: find out whether we need the login dialog at all.
		if st, err := u.opts.Client.AuthStatus(); err == nil && st.Enabled && !st.Authenticated {
			// Give the window a beat to mount before showing an overlay.
			time.Sleep(300 * time.Millisecond)
			u.login.show()
		}
		u.dash.refresh()
		u.pols.refresh()

		tick := time.NewTicker(2 * time.Second)
		defer tick.Stop()
		n := 0
		for {
			select {
			case msg, ok := <-u.opts.EngineErrors:
				if !ok {
					return
				}
				u.engineBanner.Set(msg)
				u.redraw()
			case <-tick.C:
				n++
				switch u.activeTab.Get() {
				case tabDashboard:
					if n%2 == 0 { // every 4s
						u.dash.refresh()
					}
				case tabLogs:
					u.logs.poll()
				}
			}
		}
	}()
}

var _ = uimodel.LogRow{} // keep the uimodel import pinned while screens evolve
