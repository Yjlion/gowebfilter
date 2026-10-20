package mobile

import (
	"errors"
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
	// First provisioning only: seed the protective default policy before
	// BootstrapRuntimeFiles, which then finds it and leaves it alone. An
	// existing default.json (a reinstall that kept data, or an MDM
	// policy_json applied earlier) is never overwritten.
	store := &config.PolicyStore{Dir: settings.PoliciesDir}
	if err := os.MkdirAll(settings.PoliciesDir, 0o755); err != nil {
		return err
	}
	if err := store.Create(mobileDefaultPolicy()); err != nil && !errors.Is(err, config.ErrExists) {
		return fmt.Errorf("seed default policy: %w", err)
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

// mobileDefaultPolicy is the "family baseline" a fresh Android install
// starts with, rather than the desktop's everything-off default: a phone
// user who installs a filter expects it to filter something on first start.
// SafeSearch (every engine - an absent engine entry means enabled), ad and
// tracker blocking with the default lists, and DNS through Cloudflare's
// family resolver, with QUIC blocked so browsers can't skip the pipeline.
// The NSFW classifiers and URL categories stay off: the former have real
// false-positive cost, the latter need list downloads first.
func mobileDefaultPolicy() models.Policy {
	p := models.NewPolicy()
	p.Name = "default"
	p.SafeSearch.Enabled = true
	p.Adblock.Enabled = true
	p.Doh.Enabled = true
	p.Doh.Server = "https://1.1.1.3/dns-query"
	p.UrlFilter.BlockQuic = true
	return p
}

func logMobile(format string, args ...any) {
	slog.Info("mobile: " + fmt.Sprintf(format, args...))
}
