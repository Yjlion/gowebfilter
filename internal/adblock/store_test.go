package adblock

import (
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func listServer(t *testing.T, body *string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(*body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestStoreDownloadCompileAndStatus(t *testing.T) {
	body := "[Adblock Plus 2.0]\n||ads.example^\n##.ad\n"
	srv := listServer(t, &body)
	dir := t.TempDir()
	s := NewStore(dir)
	s.Configure(dir, []ListSource{{Name: "mine", URL: srv.URL + "/list.txt"}, {Name: "easylist", URL: "http://shadow"}, {Name: "Bad Name", URL: "x"}}, time.Hour)

	cat := s.Catalog()
	if len(cat) != len(Presets)+1 || cat[len(cat)-1].Name != "mine" {
		t.Fatalf("custom list catalog = %+v (presets must not be shadowed, invalid names dropped)", cat[len(Presets):])
	}
	meta, err := s.Download(context.Background(), "mine")
	if err != nil {
		t.Fatal(err)
	}
	if meta.Rules != 2 {
		t.Errorf("rules = %d, want 2", meta.Rules)
	}
	if _, err := os.Stat(filepath.Join(dir, "mine.txt.gz")); err != nil {
		t.Fatal(err)
	}
	eng, err := s.EngineSync([]string{"mine"})
	if err != nil || eng == nil {
		t.Fatalf("EngineSync: %v %v", eng, err)
	}
	if !eng.Match(req("https://ads.example/x.js", "script", "a.test")).Blocked {
		t.Error("downloaded list not applied")
	}
	var found bool
	for _, m := range s.Status() {
		if m.Name == "mine" {
			found = m.Installed && m.Rules == 2 && m.Updated != ""
		}
	}
	if !found {
		t.Errorf("Status missing installed list: %+v", s.Status())
	}

	// A bad download keeps the old copy and records the error.
	body = "<html>error page</html>"
	if _, err := s.Download(context.Background(), "mine"); err == nil {
		t.Fatal("expected an unusable list to fail")
	}
	f, _ := os.Open(filepath.Join(dir, "mine.txt.gz"))
	zr, _ := gzip.NewReader(f)
	buf := new(strings.Builder)
	_, _ = io_copy(buf, zr)
	f.Close()
	if !strings.Contains(buf.String(), "||ads.example^") {
		t.Error("failed download replaced the good copy")
	}
	for _, m := range s.Status() {
		if m.Name == "mine" && m.Error == "" {
			t.Error("download error not recorded")
		}
	}

	if err := s.Delete("mine"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "mine.txt.gz")); !os.IsNotExist(err) {
		t.Error("Delete left the file")
	}
}

func TestStoreEngineIsNonBlockingAndWantsMissing(t *testing.T) {
	body := "||ads.example^\n"
	srv := listServer(t, &body)
	dir := t.TempDir()
	s := NewStore(dir)
	s.Configure(dir, []ListSource{{Name: "mine", URL: srv.URL}}, time.Hour)

	if e := s.Engine([]string{"mine"}); e != nil {
		t.Fatal("engine for a missing list should be nil")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Run(ctx, nil)

	deadline := time.Now().Add(5 * time.Second)
	for {
		// Force a re-check each loop (normally rate-limited).
		s.invalidate()
		if e := s.Engine([]string{"mine"}); e != nil {
			if !e.Match(req("https://ads.example/", "script", "a.test")).Blocked {
				t.Fatal("background engine wrong")
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("Run never fetched the wanted list / engine never built")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func io_copy(dst *strings.Builder, src interface{ Read([]byte) (int, error) }) (int64, error) {
	var n int64
	buf := make([]byte, 4096)
	for {
		k, err := src.Read(buf)
		dst.Write(buf[:k])
		n += int64(k)
		if err != nil {
			return n, nil
		}
	}
}
