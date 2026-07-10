package mobile

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yjlion/gowebfilter/internal/categories"
)

func withCategoriesServer(t *testing.T, lists map[string]string) {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if len(parts) != 2 || parts[1] != "domains.txt" {
			http.NotFound(w, r)
			return
		}
		body, ok := lists[parts[0]]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	prev := categoriesListBaseURL
	categoriesListBaseURL = ts.URL + "/"
	t.Cleanup(func() {
		categoriesListBaseURL = prev
		ts.Close()
	})
}

func TestCategoriesListDownloadDeleteRoundtrip(t *testing.T) {
	withCategoriesServer(t, map[string]string{"ads": "tracker.example\nads.example\n"})
	dataDir := t.TempDir()

	out, err := ListCategoriesJson(dataDir)
	if err != nil {
		t.Fatalf("ListCategoriesJson() error = %v", err)
	}
	var listed struct {
		Available []string          `json:"available"`
		Installed []categories.Meta `json:"installed"`
	}
	if err := json.Unmarshal([]byte(out), &listed); err != nil {
		t.Fatalf("list not valid JSON: %v", err)
	}
	if len(listed.Available) != len(categories.KnownRemoteCategories) {
		t.Errorf("available = %v, want the known remote list", listed.Available)
	}
	if len(listed.Installed) != 0 {
		t.Errorf("installed = %v on a fresh dataDir, want empty", listed.Installed)
	}

	metaJSON, err := DownloadCategoryJson(dataDir, "ads")
	if err != nil {
		t.Fatalf("DownloadCategoryJson() error = %v", err)
	}
	var meta categories.Meta
	if err := json.Unmarshal([]byte(metaJSON), &meta); err != nil {
		t.Fatalf("meta not valid JSON: %v", err)
	}
	if meta.Name != "ads" || meta.Count != 2 {
		t.Errorf("meta = %+v, want ads with 2 domains", meta)
	}

	out, _ = ListCategoriesJson(dataDir)
	if !strings.Contains(out, `"ads"`) {
		t.Errorf("installed list missing ads after download: %s", out)
	}

	if err := DeleteCategory(dataDir, "ads"); err != nil {
		t.Fatalf("DeleteCategory() error = %v", err)
	}
	out, _ = ListCategoriesJson(dataDir)
	if err := json.Unmarshal([]byte(out), &listed); err != nil {
		t.Fatalf("list not valid JSON: %v", err)
	}
	if len(listed.Installed) != 0 {
		t.Errorf("installed = %v after delete, want empty", listed.Installed)
	}
}

func TestCategoriesMutationsAreLockGated(t *testing.T) {
	withCategoriesServer(t, map[string]string{"ads": "tracker.example\n"})
	dataDir := t.TempDir()

	if _, err := ApplyManagedConfigJson(dataDir, `{"settings_locked":true}`); err != nil {
		t.Fatalf("apply managed lock: %v", err)
	}

	if _, err := DownloadCategoryJson(dataDir, "ads"); err == nil {
		t.Error("DownloadCategoryJson must be rejected when locked")
	} else if !strings.Contains(err.Error(), "managed by your organization") {
		t.Errorf("unexpected lock error: %v", err)
	}
	if err := DeleteCategory(dataDir, "ads"); err == nil {
		t.Error("DeleteCategory must be rejected when locked")
	}

	// Reads stay available under the lock.
	if _, err := ListCategoriesJson(dataDir); err != nil {
		t.Errorf("ListCategoriesJson under lock: %v", err)
	}
}
