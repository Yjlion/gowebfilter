package mobile

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/yjlion/gowebfilter/internal/config"
	"github.com/yjlion/gowebfilter/internal/models"
)

// ensureMobileSettings bootstraps settings.json under the app data dir and
// applies the mobile-specific overrides on first run only. It never
// overwrites an existing file, so a user's edits through the WebView mgmt UI
// survive restarts.
//
// The overrides shape the engine for on-device use:
//   - MgmtHost 127.0.0.1: the management server is only reachable from the
//     app's own WebView, never exposed on the network.
//   - ProxyListen is just the loopback SOCKS5 listener: tun2socks dials it
//     from inside the process; there is no LAN-facing HTTP proxy on a phone.
//   - Tun2Socks.Enabled: EnsureTunSocksListener adds the SOCKS5 entry if it
//     is ever missing, matching the desktop TUN path.
//
// config.NewBootstrapSettings already roots cert/policies/categories/logs
// dirs to absolute paths derived from settingsPath, so passing an absolute
// path under the app files dir is all that is needed for correct storage
// locations (the repo's relative-default gotcha does not bite here).
func ensureMobileSettings(settingsPath string) error {
	if _, err := os.Stat(settingsPath); err == nil {
		// Already provisioned — leave the user's settings untouched, but
		// still ensure the default policy + dirs exist.
		return config.BootstrapRuntimeFiles(settingsPath)
	} else if !os.IsNotExist(err) {
		return err
	}

	settings := mobileDefaults(settingsPath)
	if err := config.SaveSettings(settingsPath, settings); err != nil {
		return err
	}
	// Bootstrap the default policy + dirs against the freshly written file.
	return config.BootstrapRuntimeFiles(settingsPath)
}

// mobileDefaults returns the first-run settings for the Android app: the
// bootstrap defaults (absolute, rooted at the data dir) plus the mobile
// listener/host overrides.
func mobileDefaults(settingsPath string) models.GlobalSettings {
	settings := config.NewBootstrapSettings(settingsPath)
	settings.MgmtHost = "127.0.0.1"
	settings.ProxyListen = []string{"socks5@127.0.0.1:1080"}
	settings.Tun2Socks.Enabled = true
	return settings
}

func logMobile(format string, args ...any) {
	slog.Info("mobile: " + fmt.Sprintf(format, args...))
}
