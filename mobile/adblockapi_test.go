package mobile

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yjlion/gowebfilter/internal/config"
	"github.com/yjlion/gowebfilter/internal/models"
)

func TestAdblockListDownloadDeleteRoundtrip(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("[Adblock Plus 2.0]\n||ads.example^\n"))
	}))
	defer ts.Close()

	dataDir := t.TempDir()
	settingsPath := settingsPathFor(dataDir)
	if err := ensureMobileSettings(settingsPath); err != nil {
		t.Fatal(err)
	}
	s, err := currentSettings(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(s.Adblock.Dir, dataDir) {
		t.Fatalf("adblock dir %q is not under the app data dir", s.Adblock.Dir)
	}
	s.Adblock.CustomLists = []models.AdblockListSource{{Name: "mine", URL: ts.URL}}
	if err := config.SaveSettings(settingsPath, s); err != nil {
		t.Fatal(err)
	}

	if _, err := DownloadAdblockListJson(dataDir, "mine"); err != nil {
		t.Fatalf("DownloadAdblockListJson: %v", err)
	}
	if _, err := os.Stat(filepath.Join(s.Adblock.Dir, "mine.txt.gz")); err != nil {
		t.Fatal(err)
	}
	out, err := ListAdblockListsJson(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	var listed struct {
		Lists []struct {
			Name      string `json:"name"`
			Installed bool   `json:"installed"`
			Rules     int    `json:"rules"`
		} `json:"lists"`
	}
	if err := json.Unmarshal([]byte(out), &listed); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, l := range listed.Lists {
		if l.Name == "mine" && l.Installed && l.Rules == 1 {
			found = true
		}
	}
	if !found {
		t.Fatalf("list not reported installed: %s", out)
	}
	if err := DeleteAdblockList(dataDir, "mine"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(s.Adblock.Dir, "mine.txt.gz")); !os.IsNotExist(err) {
		t.Fatal("list file survived delete")
	}
}
