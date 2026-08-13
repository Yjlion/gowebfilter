package gui

import (
	"encoding/json"
	"image/png"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gogpu/ui/offscreen"
	"github.com/gogpu/ui/state"
	"github.com/gogpu/ui/theme/material3"
	"github.com/gogpu/ui/widget"

	"github.com/yjlion/gowebfilter/cmd/webfilter/internal/gui/mgmtclient"
	"github.com/yjlion/gowebfilter/internal/logstore"
	"github.com/yjlion/gowebfilter/internal/mgmtapi"
	"github.com/yjlion/gowebfilter/internal/models"
)

// newSnapshotUI builds the ui against a real mgmtapi server seeded with
// data, without opening a window (gogpuApp stays nil; redraw is a no-op).
func newSnapshotUI(t *testing.T) *ui {
	t.Helper()
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "config", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatal(err)
	}
	seed := `{"cert_dir":` + jsonStr(filepath.Join(dir, "certs")) +
		`,"policies_dir":` + jsonStr(filepath.Join(dir, "policies")) +
		`,"logs_dir":` + jsonStr(filepath.Join(dir, "logs")) + `}`
	if err := os.WriteFile(settingsPath, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	srv, err := mgmtapi.NewServer(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Logs.Close() })
	ts := httptest.NewServer(srv.Router())
	t.Cleanup(ts.Close)

	// Seed a second policy and some log rows so lists have content.
	kids := models.NewPolicy()
	kids.Name = "kids"
	kids.SourceIPs = []string{"192.168.1.50"}
	kids.Schedule.Enabled = true
	kids.Schedule.ActiveWindows = []models.TimeWindow{{Days: []int{0, 1, 2, 3, 4}, Start: "20:00", End: "07:00"}}
	if err := srv.Policies.Create(kids); err != nil {
		t.Fatal(err)
	}
	for i, dom := range []string{"ads.example", "tracker.example", "nsfw.example"} {
		_ = srv.Logs.LogBlock(logstore.BlockEntry{
			TS: int64(1783860000 + i*60), Domain: dom, URL: "https://" + dom + "/",
			Reason: "URL blocked by policy", Component: "url_filter", Policy: "default",
			ClientIP: "192.168.1.50",
		})
	}

	client, err := mgmtclient.New(ts.URL)
	if err != nil {
		t.Fatal(err)
	}

	u := &ui{
		opts: Options{
			Client:      client,
			MgmtURL:     ts.URL,
			SelfHosted:  true,
			OpenBrowser: func(string) error { return nil },
			RestartEngine: func() error { return nil },
		},
		m3:            material3.New(widget.Hex(0x2563EB)),
		activeTab:     state.NewSignal(tabDashboard),
		engineBanner:  state.NewSignal(""),
		restartNeeded: state.NewSignal(false),
	}
	u.dash = newDashboardScreen(u)
	u.pols = newPoliciesScreen(u)
	u.logs = newLogsScreen(u)
	u.sets = newSettingsScreen(u)
	u.adv = newAdvancedScreen(u, u.sets)
	u.login = newLoginController(u)
	return u
}

func jsonStr(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func sleepMs(ms int) { time.Sleep(time.Duration(ms) * time.Millisecond) }

// TestRenderSnapshots renders each screen to PNG for visual layout review.
// Set GUI_SNAPSHOT_DIR to enable; skipped otherwise (it is a dev tool, not
// an assertion).
func TestRenderSnapshots(t *testing.T) {
	outDir := os.Getenv("GUI_SNAPSHOT_DIR")
	if outDir == "" {
		t.Skip("set GUI_SNAPSHOT_DIR to render screen snapshots")
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		tab  int
	}{
		{"dashboard", tabDashboard},
		{"policies", tabPolicies},
		{"logs", tabLogs},
		{"settings", tabSettings},
		{"advanced", tabAdvanced},
	} {
		name, tab := tc.name, tc.tab
		// Fresh ui per tab: only the selected tab's content is laid out, and
		// the layout cache would otherwise skip re-layout on tab switches
		// that don't come through the real event path.
		u := newSnapshotUI(t)
		root := u.buildRoot()
		u.dash.refresh()
		u.pols.refresh()
		u.logs.poll()
		u.sets.reload()
		if tab == tabPolicies {
			u.pols.open("kids")
			waitFor(t, func() bool { return u.pols.editorGen.Load() > 0 })
		}
		// Select the tab the way a click would: the custom tabBar sets the
		// signal and buildRoot's contentSwap holds the active content.
		u.activeTab.Set(tab)
		u.contentSwap.SetChild(u.tabContents[tab])
		r := offscreen.NewRenderer(1100, 780,
			offscreen.WithBackground(widget.RGBA8(250, 250, 252, 255)),
		)
		r.Render(root)
		f, err := os.Create(filepath.Join(outDir, name+".png"))
		if err != nil {
			t.Fatal(err)
		}
		if err := png.Encode(f, r.Image()); err != nil {
			t.Fatal(err)
		}
		f.Close()
		t.Logf("wrote %s.png", name)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	for i := 0; i < 100; i++ {
		if cond() {
			return
		}
		sleepMs(50)
	}
	t.Fatalf("condition not met in time")
}
