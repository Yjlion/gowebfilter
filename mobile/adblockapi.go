package mobile

// Adblock list management for the native Android UI. Lists are shared by
// every policy (a policy only names which lists it uses, in its "adblock"
// block); these calls download, refresh and remove them. A running engine
// picks up a changed list by file mtime, so nothing needs restarting.

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/yjlion/gowebfilter/internal/adblock"
	"github.com/yjlion/gowebfilter/internal/proxy"
)

func adblockStore(dataDir string, mutating bool) (*adblock.Store, error) {
	settingsPath := settingsPathFor(dataDir)
	if err := ensureMobileSettings(settingsPath); err != nil {
		return nil, err
	}
	if mutating {
		if err := checkUnlocked(settingsPath); err != nil {
			return nil, err
		}
	}
	settings, err := currentSettings(settingsPath)
	if err != nil {
		return nil, err
	}
	st := adblock.NewStoreFromSettings(settings.Adblock)
	// The engine's egress dialer: with the VpnService up, a plain dial
	// from this process is fine (the app excludes itself), but sharing the
	// engine's transport keeps desktop and mobile on one code path.
	st.Client = &http.Client{Transport: proxy.NewTransport(), Timeout: 5 * time.Minute}
	return st, nil
}

// ListAdblockListsJson returns {"lists":[{name,title,url,builtin,installed,
// rules,updated,error}...],"default_lists":[...]}.
func ListAdblockListsJson(dataDir string) (string, error) {
	st, err := adblockStore(dataDir, false)
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(map[string]any{"lists": st.Status(), "default_lists": adblock.DefaultLists})
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// DownloadAdblockListJson downloads (or refreshes) one list and returns its
// status JSON. Blocks until done - call from a background thread.
func DownloadAdblockListJson(dataDir string, name string) (string, error) {
	st, err := adblockStore(dataDir, true)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	meta, err := st.Download(ctx, name)
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(meta)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// DeleteAdblockList removes a downloaded list from the device.
func DeleteAdblockList(dataDir string, name string) error {
	st, err := adblockStore(dataDir, true)
	if err != nil {
		return err
	}
	return st.Delete(name)
}
