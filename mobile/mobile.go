// Package mobile is the gomobile-bound entry point for the on-device
// Android port. It embeds the same pure-Go proxy engine the desktop build
// uses (via internal/app, so the addon pipeline order is single-sourced)
// and drives xjasonlyu/tun2socks directly from the VpnService TUN file
// descriptor — no external proxy, no root, no `ip` commands.
//
// gomobile only binds simple types (string/int/bool/error and structs of
// them), so the exported surface here is deliberately tiny. Build the AAR
// with:
//
//	gomobile bind -target=android/arm64,android/arm -androidapi 26 \
//	    -o android/app/libs/webfilter.aar ./mobile
package mobile

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sync"

	"github.com/yjlion/gowebfilter/internal/app"
	"github.com/yjlion/gowebfilter/internal/certs"
	"github.com/yjlion/gowebfilter/internal/config"
	"github.com/yjlion/gowebfilter/internal/mgmtapi"
	"github.com/yjlion/gowebfilter/internal/proxy"
	"github.com/yjlion/gowebfilter/internal/proxy/state"
)

// controller holds the single running engine instance. gomobile can't bind
// generics or channels, so the lifecycle is managed through this
// package-level singleton guarded by a mutex; Start/Stop are idempotent so
// the Kotlin VpnService can call them across revoke/reconnect cycles
// without leaking goroutines or double-binding listeners.
type controller struct {
	mu       sync.Mutex
	running  bool
	cancel   context.CancelFunc
	dataDir  string
	mgmtURL  string
	settings string // absolute settings.json path
	rt       *state.Runtime
	eng      *proxy.Engine
	mgmtSrv  *mgmtapi.Server
}

var ctl = &controller{}

// Start brings up the full on-device filter: it bootstraps runtime files
// under dataDir, wires the engine + management server, binds the local
// SOCKS5 listener, and funnels the VpnService TUN (tunFd) into it via
// tun2socks. It returns once everything is listening; the engine keeps
// running in background goroutines until Stop.
//
// dataDir should be the app's private files dir (context.getFilesDir()).
// tunFd must be a detached VpnService descriptor (ParcelFileDescriptor
// .detachFd()); ownership transfers to Go, which closes it on Stop.
func Start(dataDir string, tunFd int) error {
	ctl.mu.Lock()
	defer ctl.mu.Unlock()

	if ctl.running {
		return fmt.Errorf("already running")
	}
	if dataDir == "" {
		return fmt.Errorf("dataDir must not be empty")
	}
	if tunFd < 0 {
		return fmt.Errorf("invalid tun fd %d", tunFd)
	}

	settingsPath := filepath.Join(dataDir, "config", "settings.json")
	if err := ensureMobileSettings(settingsPath); err != nil {
		return fmt.Errorf("bootstrap settings: %w", err)
	}

	eng, rt, err := app.BuildProxyEngine(settingsPath)
	if err != nil {
		return fmt.Errorf("build engine: %w", err)
	}
	app.EnsureTunSocksListener(eng)

	listeners, err := eng.Listen()
	if err != nil {
		rt.Logs.Close()
		return fmt.Errorf("bind listeners: %w", err)
	}

	mgmtSrv, err := mgmtapi.NewServer(settingsPath)
	if err != nil {
		for _, ln := range listeners {
			_ = ln.Close()
		}
		rt.Logs.Close()
		return fmt.Errorf("build mgmt server: %w", err)
	}
	mgmtSrv.OnCARotated = rt.LeafIssuer.Clear

	ctx, cancel := context.WithCancel(context.Background())
	rt.Start(ctx)

	go func() {
		if err := eng.Serve(ctx, listeners); err != nil {
			logMobile("proxy engine stopped: %v", err)
		}
	}()
	go func() {
		if err := app.ServeMgmt(ctx, mgmtSrv); err != nil {
			logMobile("mgmt server stopped: %v", err)
		}
	}()

	// Feed the VpnService TUN into the in-process SOCKS5 listener.
	if err := startTun(tunFd); err != nil {
		cancel()
		for _, ln := range listeners {
			_ = ln.Close()
		}
		rt.Logs.Close()
		mgmtSrv.Logs.Close()
		return fmt.Errorf("start tun2socks: %w", err)
	}

	ctl.running = true
	ctl.cancel = cancel
	ctl.dataDir = dataDir
	ctl.settings = settingsPath
	ctl.rt = rt
	ctl.eng = eng
	ctl.mgmtSrv = mgmtSrv
	ctl.mgmtURL = fmt.Sprintf("http://127.0.0.1:%d/", rt.Settings.MgmtPort)
	logMobile("webfilter started, mgmt at %s", ctl.mgmtURL)
	return nil
}

// Stop tears down the engine and tun2socks. It is safe to call when not
// running (no-op) and safe to call repeatedly.
func Stop() {
	ctl.mu.Lock()
	defer ctl.mu.Unlock()
	if !ctl.running {
		return
	}
	stopTun()
	if ctl.cancel != nil {
		ctl.cancel()
	}
	// Both the runtime and the mgmt server hold their own sqlite write
	// connection on the same DB file (logstore.Configure opens a fresh one
	// per caller), so both must be closed or a VpnService revoke/reconnect
	// cycle leaks a connection each time.
	if ctl.rt != nil {
		ctl.rt.Logs.Close()
	}
	if ctl.mgmtSrv != nil {
		ctl.mgmtSrv.Logs.Close()
	}
	ctl.running = false
	ctl.cancel = nil
	ctl.rt = nil
	ctl.eng = nil
	ctl.mgmtSrv = nil
	logMobile("webfilter stopped")
}

// IsRunning reports whether the engine is currently up.
func IsRunning() bool {
	ctl.mu.Lock()
	defer ctl.mu.Unlock()
	return ctl.running
}

// MgmtUrl returns the loopback URL of the embedded management UI, for the
// Android WebView to load. Empty when not running.
func MgmtUrl() string {
	ctl.mu.Lock()
	defer ctl.mu.Unlock()
	return ctl.mgmtURL
}

// ReloadPolicies re-reads policies/*.json immediately (the fsnotify watcher
// also does this, but Android's scoped storage can make inotify unreliable,
// so expose an explicit trigger for the Kotlin layer to call after an edit).
func ReloadPolicies() error {
	ctl.mu.Lock()
	defer ctl.mu.Unlock()
	if !ctl.running || ctl.rt == nil {
		return fmt.Errorf("not running")
	}
	ctl.rt.ReloadPolicies()
	return nil
}

// Status returns a small JSON blob {running, mgmtUrl, listeners} for the UI
// to render without a second round trip.
func Status() string {
	ctl.mu.Lock()
	defer ctl.mu.Unlock()

	st := struct {
		Running   bool     `json:"running"`
		MgmtURL   string   `json:"mgmtUrl"`
		Listeners []string `json:"listeners"`
	}{Running: ctl.running, MgmtURL: ctl.mgmtURL}
	if ctl.running && ctl.eng != nil {
		st.Listeners = append(st.Listeners, ctl.eng.Settings.ProxyListen...)
	}
	b, err := json.Marshal(st)
	if err != nil {
		return `{"running":false}`
	}
	return string(b)
}

// CaCertPem returns the PEM-encoded public CA certificate the user must
// install to trust the on-device proxy. It reads the CA directly from disk
// (creating it on first call if needed) so the Android CA-install flow does
// not depend on the mgmt HTTP server being up. Returns "" on error.
//
// The private key is never exposed here — only the public certificate, the
// same bytes GET /api/ca-cert serves.
func CaCertPem(dataDir string) string {
	settingsPath := filepath.Join(dataDir, "config", "settings.json")
	if err := ensureMobileSettings(settingsPath); err != nil {
		logMobile("CaCertPem: bootstrap settings: %v", err)
		return ""
	}
	settings, err := config.LoadSettings(settingsPath)
	if err != nil {
		logMobile("CaCertPem: load settings: %v", err)
		return ""
	}
	ca, err := certs.LoadOrCreateCA(settings.CertDir)
	if err != nil {
		logMobile("CaCertPem: load CA: %v", err)
		return ""
	}
	return string(ca.CertPEM)
}
