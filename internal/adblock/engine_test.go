package adblock

import (
	"strings"
	"testing"
)

func compile(t *testing.T, list string) *Engine {
	t.Helper()
	e, err := Compile(strings.NewReader(list))
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func req(url, typ, source string) Request {
	r := Request{URL: url, Type: typ}
	if i := strings.Index(url, "://"); i >= 0 {
		h := url[i+3:]
		if j := strings.IndexAny(h, "/?#:"); j >= 0 {
			h = h[:j]
		}
		r.Host = h
	}
	if source != "" {
		r.SourceURL = "https://" + source + "/"
		r.SourceHost = source
	}
	return r
}

func TestGlobMatch(t *testing.T) {
	cases := []struct {
		p, s          string
		anchored, end bool
		want          bool
	}{
		{"/ads/", "https://x.com/ads/1.js", false, false, true},
		{"/ads/", "https://x.com/adsx/1.js", false, false, false},
		{"/ad*.js", "https://x.com/advert/foo.js", false, false, true},
		{"example.com^", "example.com/", true, false, true},
		{"example.com^", "example.com", true, false, true}, // ^ matches end
		{"example.com^", "example.community/", true, false, false},
		{"banner.gif", "https://x.com/banner.gif", false, true, true},
		{"banner.gif", "https://x.com/banner.gif?x", false, true, false},
		{"a*b*c", "xxaxxbxxcxx", false, false, true},
		{"a*b*c", "xxaxxcxxbxx", false, false, false},
	}
	for _, c := range cases {
		if got := globMatch(c.p, c.s, c.anchored, c.end); got != c.want {
			t.Errorf("globMatch(%q, %q, %v, %v) = %v, want %v", c.p, c.s, c.anchored, c.end, got, c.want)
		}
	}
}

func TestHostRules(t *testing.T) {
	e := compile(t, "[Adblock Plus 2.0]\n! comment\n||ads.example^\n")
	if !e.Match(req("https://ads.example/x.js", "script", "news.test")).Blocked {
		t.Error("host rule did not block")
	}
	if !e.Match(req("https://cdn.ads.example/x.js", "script", "news.test")).Blocked {
		t.Error("host rule did not block a subdomain")
	}
	if e.Match(req("https://notads.example/x.js", "script", "news.test")).Blocked {
		t.Error("host rule matched a different domain")
	}
	// Pure host rules also block navigating to the host (uBO behaviour).
	if !e.Match(req("https://ads.example/", "document", "")).Blocked {
		t.Error("pure host rule did not block the document")
	}
}

func TestHostsAndDomainListFormats(t *testing.T) {
	hosts := compile(t, "# hosts\n127.0.0.1 localhost\n0.0.0.0 tracker.example # comment\n0.0.0.0 0.0.0.0\n")
	if !hosts.Match(req("http://tracker.example/p", "image", "a.test")).Blocked {
		t.Error("hosts entry not blocked")
	}
	if hosts.Match(req("http://localhost/p", "image", "a.test")).Blocked {
		t.Error("localhost must not become a rule")
	}
	plain := compile(t, "# domains\nmetrics.example\n")
	if !plain.Match(req("http://metrics.example/p", "xmlhttprequest", "a.test")).Blocked {
		t.Error("plain domain list entry not blocked")
	}
}

func TestPatternsAndExceptions(t *testing.T) {
	e := compile(t, `! list
/banner/*/ad.
||example.com/ads/
@@||example.com/ads/allowed.js
|https://track.
`)
	cases := []struct {
		url  string
		want bool
	}{
		{"https://site.test/banner/123/ad.png", true},
		{"https://example.com/ads/x.js", true},
		{"https://example.com/ads/allowed.js", false},
		{"https://track.site.test/p", true},
		{"https://nottrack.site.test/p", false},
		{"https://site.test/content.png", false},
	}
	for _, c := range cases {
		if got := e.Match(req(c.url, "script", "site.test")).Blocked; got != c.want {
			t.Errorf("%s: blocked=%v, want %v", c.url, got, c.want)
		}
	}
}

func TestOptions(t *testing.T) {
	e := compile(t, `||cdn.example^$third-party
||img.example^$image
||wide.example^$~image
/spy.js$domain=a.test|~b.a.test
||imp.example^$important
@@||imp.example^
||strict.example^$script,1p
`)
	type c struct {
		r    Request
		want bool
	}
	cases := []c{
		{req("https://cdn.example/x.js", "script", "other.test"), true},
		{req("https://cdn.example/x.js", "script", "www.cdn.example"), false}, // first party
		{req("https://img.example/a.png", "image", "x.test"), true},
		{req("https://img.example/a.js", "script", "x.test"), false},
		{req("https://wide.example/a.png", "image", "x.test"), false},
		{req("https://wide.example/a.js", "script", "x.test"), true},
		{req("https://s.test/spy.js", "script", "a.test"), true},
		{req("https://s.test/spy.js", "script", "b.a.test"), false},
		{req("https://s.test/spy.js", "script", "c.test"), false},
		{req("https://imp.example/x", "script", "x.test"), true}, // important beats exception
		{req("https://strict.example/x.js", "script", "strict.example"), true},
		{req("https://strict.example/x.js", "script", "other.test"), false},
	}
	for _, tc := range cases {
		if got := e.Match(tc.r).Blocked; got != tc.want {
			t.Errorf("%s (%s from %s): blocked=%v, want %v", tc.r.URL, tc.r.Type, tc.r.SourceHost, got, tc.want)
		}
	}
}

func TestNonHostPatternsSkipDocuments(t *testing.T) {
	e := compile(t, "/ads/\n")
	if e.Match(req("https://site.test/ads/", "document", "")).Blocked {
		t.Error("a pattern rule without $document blocked a navigation")
	}
}

func TestDocumentException(t *testing.T) {
	e := compile(t, "||ads.example^\n@@||trusted.test^$document\n")
	if e.Match(req("https://ads.example/x.js", "script", "trusted.test")).Blocked {
		t.Error("$document exception on the source page did not allow its requests")
	}
	if !e.Match(req("https://ads.example/x.js", "script", "other.test")).Blocked {
		t.Error("$document exception leaked to other pages")
	}
}

func TestUnsupportedRulesSkipped(t *testing.T) {
	var p parsed
	n, err := parseList(strings.NewReader(`! x
||a.example^$redirect=noop.js
||b.example^$csp=script-src 'none'
||c.example^$removeparam=utm
example.com##+js(nobab)
example.com#?#div:has-text(Sponsored)
example.com#$#body { color: red }
##.ad:-abp-contains(x)
##</style><script>alert(1)</script>
##div { color:red }
/(?<=x)y/
||ok.example^
##.ok-ad
`), &p)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("usable rules = %d, want 2 (net=%d cosmetic=%d)", n, len(p.net), len(p.cosmetic))
	}
	if p.skipped != 10 {
		t.Errorf("skipped = %d, want 10", p.skipped)
	}
}

func TestCosmetic(t *testing.T) {
	e := compile(t, `! x
##.ad-banner
###sidebar-ad
##div[data-ad]
news.test##.promo
news.test#@#.ad-banner
~shop.test##.sponsored
@@||clean.test^$elemhide
@@||nogeneric.test^$generichide
`)
	page := []byte(`<div class="content ad-banner"><div id="sidebar-ad"></div><p class='sponsored'>x</p></div>`)

	got := e.CosmeticSelectors("www.other.test", "https://www.other.test/", page)
	want := "#sidebar-ad|.ad-banner|.sponsored|div[data-ad]"
	if strings.Join(got, "|") != want {
		t.Errorf("generic page: %v, want %s", got, want)
	}
	// Selectors whose class/id is not on the page are not sent.
	got = e.CosmeticSelectors("x.test", "https://x.test/", []byte(`<p>nothing</p>`))
	if strings.Join(got, "|") != "div[data-ad]" {
		t.Errorf("unrelated page: %v", got)
	}
	got = e.CosmeticSelectors("news.test", "https://news.test/", page)
	if strings.Join(got, "|") != "#sidebar-ad|.promo|.sponsored|div[data-ad]" {
		t.Errorf("site page (exception + specific): %v", got)
	}
	got = e.CosmeticSelectors("shop.test", "https://shop.test/", page)
	for _, s := range got {
		if s == ".sponsored" {
			t.Error("~shop.test exclusion ignored")
		}
	}
	if got = e.CosmeticSelectors("clean.test", "https://clean.test/", page); len(got) != 0 {
		t.Errorf("$elemhide page got selectors %v", got)
	}
	if got = e.CosmeticSelectors("nogeneric.test", "https://nogeneric.test/", page); len(got) != 0 {
		t.Errorf("$generichide page got generic selectors %v", got)
	}
	css := CosmeticCSS([]string{".a", "#b"})
	if css != ".a{display:none!important}\n#b{display:none!important}\n" {
		t.Errorf("CosmeticCSS = %q", css)
	}
}

func TestBestToken(t *testing.T) {
	cases := map[string]string{
		"/banner/*/ad.":             "banner",
		"/advert":                   "",        // trailing run may extend
		"||track.example.com/pixel": "example", // longest bounded run
	}
	for raw, want := range cases {
		r := parseNetRule(raw)
		if r == nil {
			t.Fatalf("%s: not parsed", raw)
		}
		if r.hostOnly != "" {
			continue
		}
		if got := bestToken(r); got != want {
			t.Errorf("bestToken(%s) = %q, want %q", raw, got, want)
		}
	}
}

func BenchmarkMatch(b *testing.B) {
	var sb strings.Builder
	for i := 0; i < 20000; i++ {
		sb.WriteString("||ads")
		sb.WriteString(strings.Repeat("x", i%7))
		sb.WriteString(itoa(i))
		sb.WriteString(".example^\n/banner")
		sb.WriteString(itoa(i))
		sb.WriteString("/*/img.\n")
	}
	e, _ := Compile(strings.NewReader(sb.String()))
	r := req("https://www.site.test/static/js/app.bundle.js?v=123", "script", "www.site.test")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		e.Match(r)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var d []byte
	for ; i > 0; i /= 10 {
		d = append([]byte{byte('0' + i%10)}, d...)
	}
	return string(d)
}
