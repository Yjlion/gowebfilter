package mobile

import (
	"github.com/yjlion/gowebfilter/internal/settingsvc"
)

// ApplyManagedConfigJson applies the canonical managed-configuration
// document the Kotlin layer builds from the EMM's RestrictionsManager
// bundle (see android/ ManagedConfig.kt for the builder and
// internal/settingsvc/managed.go for the schema). It deliberately bypasses
// the settings lock - this call IS the manager.
//
// Returns true when settings, the default policy, or the lock state
// actually changed, so the caller knows whether a running engine must be
// restarted for settings to take effect (policy changes hot-reload and are
// pushed to the runtime here directly). Re-applying an identical document
// is a cheap no-op (hash short-circuit), so calling this on every app/
// service start is the intended usage.
//
// An empty document ("" or "{}") clears managed state: the device is no
// longer managed, previously applied values remain but become editable.
func ApplyManagedConfigJson(dataDir string, doc string) (bool, error) {
	settingsPath := settingsPathFor(dataDir)
	if err := ensureMobileSettings(settingsPath); err != nil {
		return false, err
	}

	res, err := settingsvc.ApplyManagedConfig(settingsPath, []byte(doc))
	if err != nil {
		return false, err
	}
	if !res.Changed {
		return false, nil
	}

	ctl.mu.Lock()
	if ctl.running && ctl.settings == settingsPath {
		if res.SettingsChanged && ctl.mgmtSrv != nil {
			// Refresh the mgmt server's in-memory cache from the merged
			// value so a WebView PUT between now and the engine restart
			// can't merge from (and re-persist) the stale pre-managed
			// snapshot.
			_ = ctl.mgmtSrv.SaveSettings(res.Settings)
		}
		if res.PolicyChanged && ctl.rt != nil {
			ctl.rt.ReloadPolicies()
		}
	}
	ctl.mu.Unlock()

	return true, nil
}
