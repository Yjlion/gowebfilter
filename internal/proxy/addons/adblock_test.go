package addons_test

import (
	"bytes"
	"compress/gzip"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yjlion/gowebfilter/internal/adblock"
	"github.com/yjlion/gowebfilter/internal/models"
	"github.com/yjlion/gowebfilter/internal/proxy"
	"github.com/yjlion/gowebfilter/internal/proxy/addons"
	"github.com/yjlion/gowebfilter/internal/proxy/state"
)

const testAdList = `[Adblock Plus 2.0]
||ads.example^
/banner/ad.
##.ad-slot
news.test##.promo
`

// adblockRuntime installs testAdList as the "easylist" preset and primes
// the compiled engine, so the addon's non-blocking Engine lookup hits.
func adblockRuntime(t *testing.T) *state.Runtime {
	t.Helper()
	rt := newTestRuntime(t)
	dir := t.TempDir()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	zw.Write([]byte(testAdList))
	zw.Close()
	must(t, os.WriteFile(filepath.Join(dir, "easylist.txt.gz"), buf.Bytes(), 0o644))
	rt.Adblock = adblock.NewStore(dir)
	if _, err := rt.Adblock.EngineSync([]string{"easylist"}); err != nil {
		t.Fatal(err)
	}
	return rt
}

func adblockPolicy() *models.Policy {
	p := models.NewPolicy()
	p.Adblock.Enabled = true
	p.Adblock.Lists = []string{"easylist"}
	return &p
}

func adFlow(t *testing.T, rt *state.Runtime, url string, hdr map[string]string) *proxy.FlowContext {
	fc := newFlow(t, rt, url)
	for k, v := range hdr {
		fc.Request.Header.Set(k, v)
	}
	fc.Policy = adblockPolicy()
	return fc
}

func TestAdblockBlocksSubresourcesWithEmptyResponses(t *testing.T) {
	rt := adblockRuntime(t)
	cases := []struct {
		dest, wantType string
		wantStatus     int
	}{
		{"image", "image/gif", http.StatusOK},
		{"script", "application/javascript", http.StatusOK},
		{"style", "text/css", http.StatusOK},
		{"empty", "", http.StatusNoContent},
	}
	for _, c := range cases {
		fc := adFlow(t, rt, "https://ads.example/x", map[string]string{"Sec-Fetch-Dest": c.dest, "Referer": "https://news.test/"})
		addons.Adblock{}.HandleRequest(fc)
		if fc.Response == nil {
			t.Fatalf("%s: not blocked", c.dest)
		}
		if fc.Response.StatusCode != c.wantStatus || fc.Response.Header.Get("Content-Type") != c.wantType {
			t.Errorf("%s: status=%d type=%q", c.dest, fc.Response.StatusCode, fc.Response.Header.Get("Content-Type"))
		}
		if fc.WFAction != "blocked" || fc.WFComponent != "adblock" {
			t.Errorf("%s: WFAction/WFComponent = %q/%q", c.dest, fc.WFAction, fc.WFComponent)
		}
	}
}

func TestAdblockNavigationGetsBlockPage(t *testing.T) {
	rt := adblockRuntime(t)
	fc := adFlow(t, rt, "https://ads.example/", map[string]string{"Sec-Fetch-Dest": "document"})
	addons.Adblock{}.HandleRequest(fc)
	if fc.Response == nil || !strings.Contains(string(fc.ResponseBody), "Access Blocked") {
		t.Fatal("navigation to an ad host did not get the block page")
	}
}

func TestAdblockLeavesOtherRequests(t *testing.T) {
	rt := adblockRuntime(t)
	for name, fc := range map[string]*proxy.FlowContext{
		"clean": adFlow(t, rt, "https://news.test/app.js", map[string]string{"Sec-Fetch-Dest": "script"}),
		"disabled": func() *proxy.FlowContext {
			f := adFlow(t, rt, "https://ads.example/x", nil)
			f.Policy.Adblock.Enabled = false
			return f
		}(),
		"allowed": func() *proxy.FlowContext {
			f := adFlow(t, rt, "https://ads.example/x", nil)
			f.URLAllowed = true
			return f
		}(),
		"site-exempt": func() *proxy.FlowContext {
			f := adFlow(t, rt, "https://ads.example/x", map[string]string{"Referer": "https://www.news.test/"})
			f.Policy.Adblock.Allow = []string{"*.news.test"}
			return f
		}(),
	} {
		addons.Adblock{}.HandleRequest(fc)
		if fc.Response != nil {
			t.Errorf("%s: unexpectedly blocked", name)
		}
	}
}

func TestAdblockInjectsCosmeticCSS(t *testing.T) {
	rt := adblockRuntime(t)
	fc := adFlow(t, rt, "https://news.test/", nil)
	fc.Response = &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/html"}}}
	fc.ResponseBody = []byte(`<html><head><title>x</title></head><body><div class="ad-slot"></div></body></html>`)
	addons.Adblock{}.HandleResponse(fc)
	got := string(fc.ResponseBody)
	if !strings.Contains(got, `.ad-slot{display:none!important}`) || !strings.Contains(got, `.promo{display:none!important}`) {
		t.Fatalf("cosmetic CSS missing: %s", got)
	}
	if !strings.Contains(got, "</style></head>") {
		t.Errorf("style not injected before </head>: %s", got)
	}

	fc = adFlow(t, rt, "https://news.test/", nil)
	fc.Policy.Adblock.Cosmetic = false
	fc.Response = &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/html"}}}
	fc.ResponseBody = []byte(`<html><head></head><body class="ad-slot"></body></html>`)
	addons.Adblock{}.HandleResponse(fc)
	if strings.Contains(string(fc.ResponseBody), "<style") {
		t.Error("cosmetic=false still injected CSS")
	}
}

func TestAdblockFailsOpenWithoutLists(t *testing.T) {
	rt := newTestRuntime(t)
	rt.Adblock = adblock.NewStore(t.TempDir())
	fc := adFlow(t, rt, "https://ads.example/x", nil)
	addons.Adblock{}.HandleRequest(fc)
	if fc.Response != nil {
		t.Fatal("blocked with no lists installed")
	}
}
