package mobile

// Per-category blocklist management for the Android native UI. Same
// conventions as settingsapi.go: JSON strings in/out, disk-backed and
// self-bootstrapping (works with the engine stopped), MDM-lock-gated
// mutations. A running engine picks a downloaded/deleted list up through
// the categories store's mtime cache (≤60 s, immediately for lists it has
// never loaded) — no reload plumbing needed.

import (
	"context"
	"encoding/json"

	"github.com/yjlion/gowebfilter/internal/categories"
)

// categoriesListBaseURL is the per-category download root; a package var so
// tests can point it at an httptest server.
var categoriesListBaseURL = categories.DefaultListBaseURL

// ListCategoriesJson returns
// {"available":["ads",...],"installed":[{"name","count","updated"},...]} —
// the known downloadable category names plus what is currently on disk.
func ListCategoriesJson(dataDir string) (string, error) {
	settingsPath := settingsPathFor(dataDir)
	if err := ensureMobileSettings(settingsPath); err != nil {
		return "", err
	}
	settings, err := currentSettings(settingsPath)
	if err != nil {
		return "", err
	}
	installed := categories.NewStore(settings.CategoriesDir).List()
	out := struct {
		Available []string          `json:"available"`
		Installed []categories.Meta `json:"installed"`
	}{Available: categories.KnownRemoteCategories, Installed: installed}
	data, err := json.Marshal(out)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// DownloadCategoryJson fetches one ipfire category list onto the device
// (stored gzip-compressed) and returns its updated metadata JSON. Blocks
// until the download completes — call from a background thread.
func DownloadCategoryJson(dataDir string, name string) (string, error) {
	settingsPath := settingsPathFor(dataDir)
	if err := ensureMobileSettings(settingsPath); err != nil {
		return "", err
	}
	if err := checkUnlocked(settingsPath); err != nil {
		return "", err
	}
	settings, err := currentSettings(settingsPath)
	if err != nil {
		return "", err
	}
	meta, err := categories.DownloadCategory(context.Background(), settings.CategoriesDir, categoriesListBaseURL, name)
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(meta)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// DeleteCategory removes a downloaded category list from the device.
func DeleteCategory(dataDir string, name string) error {
	settingsPath := settingsPathFor(dataDir)
	if err := ensureMobileSettings(settingsPath); err != nil {
		return err
	}
	if err := checkUnlocked(settingsPath); err != nil {
		return err
	}
	settings, err := currentSettings(settingsPath)
	if err != nil {
		return err
	}
	return categories.DeleteCategory(settings.CategoriesDir, name)
}
