package adblock

import (
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestRealLists compiles real filter lists when ADBLOCK_LISTS points at
// them (comma-separated paths). It is a sizing check, skipped by default
// because the lists are downloaded, not vendored.
func TestRealLists(t *testing.T) {
	paths := os.Getenv("ADBLOCK_LISTS")
	if paths == "" {
		t.Skip("set ADBLOCK_LISTS=easylist.txt,easyprivacy.txt to run")
	}
	var before runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	start := time.Now()
	var p parsed
	for _, path := range strings.Split(paths, ",") {
		f, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := parseList(f, &p); err != nil {
			t.Fatal(err)
		}
		f.Close()
	}
	e := build(&p)
	elapsed := time.Since(start)
	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	t.Logf("compiled in %v: network=%d cosmetic=%d skipped=%d generic-net-block=%d heap=%.1f MB",
		elapsed, e.NetworkRules, e.CosmeticRules, e.Skipped, len(e.block.generic),
		float64(after.HeapAlloc-before.HeapAlloc)/(1<<20))

	for _, c := range []struct {
		r    Request
		want bool
	}{
		{req("https://www.google-analytics.com/analytics.js", "script", "news.test"), true},
		{req("https://securepubads.g.doubleclick.net/tag/js/gpt.js", "script", "news.test"), true},
		{req("https://news.test/static/app.js", "script", "news.test"), false},
		{req("https://www.wikipedia.org/", "document", ""), false},
	} {
		v := e.Match(c.r)
		t.Logf("%s -> %v (%s)", c.r.URL, v.Blocked, v.Rule)
		if v.Blocked != c.want {
			t.Errorf("%s: blocked=%v, want %v", c.r.URL, v.Blocked, c.want)
		}
	}
	start = time.Now()
	const n = 20000
	r := req("https://cdn.news.test/assets/js/vendor.bundle.min.js?v=1234", "script", "www.news.test")
	for i := 0; i < n; i++ {
		e.Match(r)
	}
	t.Logf("match: %v/op", time.Since(start)/n)
	page := []byte(`<div class="ad-banner adsbygoogle"><div id="ad_wrapper"></div></div>`)
	t.Logf("cosmetic for a sample page: %d selectors", len(e.CosmeticSelectors("www.news.test", "https://www.news.test/", page)))
}
