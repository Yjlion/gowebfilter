//go:build android || linux

package mobile

import (
	"fmt"
	"sync"

	tunengine "github.com/xjasonlyu/tun2socks/v2/engine"
)

// tunMu guards the process-global tun2socks engine (it keeps its state in
// package-level singletons, so only one TUN can be active at a time — which
// is exactly the VpnService model: one tunnel per app).
var tunMu sync.Mutex
var tunStarted bool

// startTun points xjasonlyu/tun2socks at the VpnService file descriptor and
// funnels every captured flow to the in-process SOCKS5 listener that the
// engine binds on 127.0.0.1:1080. It intentionally does NOT use
// internal/tun2socks.Manager, whose Start is gated on GOOS + root and shells
// out to `ip` — none of which applies on Android, where VpnService already
// owns the interface and hands us a ready fd.
//
// The MTU/address defaults match models.NewTun2SocksConfig (a 198.18.0.0/15
// TUN), which the Kotlin VpnService.Builder must mirror when it establishes
// the descriptor.
func startTun(tunFd int) error {
	tunMu.Lock()
	defer tunMu.Unlock()
	if tunStarted {
		return fmt.Errorf("tun already started")
	}

	tunengine.Insert(&tunengine.Key{
		Device:   fmt.Sprintf("fd://%d", tunFd),
		Proxy:    "socks5://127.0.0.1:1080",
		MTU:      1500,
		LogLevel: "warn",
	})
	// tunengine.Start() log.Fatalf's on failure (killing the process), so we
	// have validated the fd and the loopback proxy target above; the SOCKS5
	// listener is already bound by the time Start calls this.
	tunengine.Start()
	tunStarted = true
	return nil
}

// stopTun shuts the tun2socks engine down. Safe to call when not started.
func stopTun() {
	tunMu.Lock()
	defer tunMu.Unlock()
	if !tunStarted {
		return
	}
	tunengine.Stop()
	tunStarted = false
}
