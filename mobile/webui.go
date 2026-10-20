package mobile

import "sync/atomic"

// webUIEnabled is the Android "Web interface" switch (Settings > Advanced,
// or the MDM web_ui_enabled restriction). Off by default: the app's native
// screens drive the engine through this package's exported functions, so
// the loopback management web UI and its REST API are only served when the
// user opts in. While off, the mgmt server keeps answering the PAC file and
// the CA certificate (see mgmtapi.webUIOffPaths), which proxy-only mode and
// CA install depend on.
//
// It is process state, not settings.json: the Kotlin layer owns the
// preference and pushes it in at process start and on every change, and the
// mgmt server reads it per request, so a toggle needs no engine restart.
var webUIEnabled atomic.Bool

// SetWebUiEnabled turns the management web UI on or off. Safe to call at
// any time, with the engine running or not.
func SetWebUiEnabled(enabled bool) {
	webUIEnabled.Store(enabled)
}

// WebUiEnabled reports the current web UI switch.
func WebUiEnabled() bool {
	return webUIEnabled.Load()
}

func webUIDisabled() bool {
	return !webUIEnabled.Load()
}
