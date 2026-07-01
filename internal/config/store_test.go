package config_test

import (
	"path/filepath"
	"testing"

	"github.com/yjlion/gowebfilter/internal/config"
	"github.com/yjlion/gowebfilter/internal/models"
)

func TestLoadSettingsMissingFileReturnsDefaults(t *testing.T) {
	s, err := config.LoadSettings(filepath.Join(t.TempDir(), "settings.json"))
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if s.MgmtPort != 8000 {
		t.Errorf("MgmtPort = %d, want default 8000", s.MgmtPort)
	}
}

func TestSaveThenLoadSettingsRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config", "settings.json")
	s := models.NewGlobalSettings()
	s.MgmtPort = 9001
	s.AuthEnabled = true

	if err := config.SaveSettings(path, s); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}
	loaded, err := config.LoadSettings(path)
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if loaded.MgmtPort != 9001 || !loaded.AuthEnabled {
		t.Errorf("loaded = %+v, want MgmtPort=9001 AuthEnabled=true", loaded)
	}
}
