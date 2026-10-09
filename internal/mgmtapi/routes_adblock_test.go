package mgmtapi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yjlion/gowebfilter/internal/models"
)

func TestAdblockRoutes(t *testing.T) {
	list := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("[Adblock Plus 2.0]\n||ads.example^\n##.ad\n"))
	}))
	defer list.Close()

	s, ts := newTestServer(t)
	settings := s.Settings()
	settings.Adblock = models.NewAdblockSettings()
	settings.Adblock.Dir = t.TempDir()
	settings.Adblock.CustomLists = []models.AdblockListSource{{Name: "mine", URL: list.URL}}
	if err := s.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	client := ts.Client()

	resp, err := client.Post(ts.URL+"/api/adblock/lists/mine/update", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("update status = %d", resp.StatusCode)
	}

	resp, err = client.Get(ts.URL + "/api/adblock/lists")
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Lists []struct {
			Name      string `json:"name"`
			Installed bool   `json:"installed"`
			Rules     int    `json:"rules"`
			Builtin   bool   `json:"builtin"`
		} `json:"lists"`
	}
	json.NewDecoder(resp.Body).Decode(&body)
	resp.Body.Close()
	var mine bool
	for _, l := range body.Lists {
		if l.Name == "mine" {
			mine = l.Installed && l.Rules == 2 && !l.Builtin
		}
	}
	if !mine || len(body.Lists) < 3 {
		t.Fatalf("lists = %+v", body.Lists)
	}

	resp, err = client.Post(ts.URL+"/api/adblock/lists/nope/update", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		t.Error("unknown list update succeeded")
	}

	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/adblock/lists/mine", nil)
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	resp, _ = client.Get(ts.URL + "/api/adblock/lists")
	body.Lists = nil
	json.NewDecoder(resp.Body).Decode(&body)
	resp.Body.Close()
	for _, l := range body.Lists {
		if l.Name == "mine" && l.Installed {
			t.Error("list still installed after DELETE")
		}
	}
}
