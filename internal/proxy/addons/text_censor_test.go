package addons_test

import (
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/yjlion/gowebfilter/internal/models"
	"github.com/yjlion/gowebfilter/internal/proxy"
	"github.com/yjlion/gowebfilter/internal/proxy/addons"
)

func censorFlow(t *testing.T, mode models.TextClassifierMode, body string, header http.Header) *proxy.FlowContext {
	t.Helper()
	rt := newTestRuntime(t)
	fc := newFlow(t, rt, "http://example.com/page")
	if header == nil {
		header = http.Header{}
	}
	header.Set("Content-Type", "text/html; charset=utf-8")
	fc.Response = &http.Response{StatusCode: http.StatusOK, Header: header}
	fc.ResponseBody = []byte(body)
	policy := models.NewPolicy()
	policy.TextClassifier = models.NewTextClassifierConfig()
	policy.TextClassifier.Enabled = true
	policy.TextClassifier.Mode = mode
	fc.Policy = &policy
	return fc
}

func TestCensorMasksTextOnly(t *testing.T) {
	body := `<html lang="en"><head><title>Oh shit</title><style>.shit{color:red}</style></head>` +
		`<body class="shit"><a href="/fuck">What the fuck &amp; more</a>` +
		`<script>var fuck = 1;</script><textarea>shit</textarea><code>shit()</code></body></html>`
	fc := censorFlow(t, models.TextModeCensor, body, nil)

	addons.TextClassifier{}.HandleResponse(fc)

	got := string(fc.ResponseBody)
	want := `<html lang="en"><head><title>Oh ****</title><style>.shit{color:red}</style></head>` +
		`<body class="shit"><a href="/fuck">What the **** &amp; more</a>` +
		`<script>var fuck = 1;</script><textarea>shit</textarea><code>shit()</code></body></html>`
	if got != want {
		t.Fatalf("censored body:\n got %s\nwant %s", got, want)
	}
	if fc.WFAction != "modified" || fc.WFComponent != "text_classifier" {
		t.Errorf("WFAction/WFComponent = %q/%q", fc.WFAction, fc.WFComponent)
	}
	if cl := fc.Response.Header.Get("Content-Length"); cl != strconv.Itoa(len(got)) {
		t.Errorf("Content-Length = %s, want %d", cl, len(got))
	}
}

func TestCensorLeavesCleanPageByteIdentical(t *testing.T) {
	body := "<html><body><p>Gardening &amp; compost.</p><p>Unclosed <b>tags</body>"
	fc := censorFlow(t, models.TextModeCensor, body, nil)
	addons.TextClassifier{}.HandleResponse(fc)
	if string(fc.ResponseBody) != body || fc.WFAction != "" {
		t.Fatalf("clean page changed: %q (action %q)", fc.ResponseBody, fc.WFAction)
	}
}

func TestCensorNeverBlocks(t *testing.T) {
	body := "<html>porn xxx hentai</html>"
	fc := censorFlow(t, models.TextModeCensor, body, nil)
	addons.TextClassifier{}.HandleResponse(fc)
	if strings.Contains(string(fc.ResponseBody), "Access Blocked") {
		t.Fatal("censor mode blocked a page")
	}
	if strings.Contains(string(fc.ResponseBody), "porn") {
		t.Fatalf("censor mode left an English list word: %s", fc.ResponseBody)
	}
}

func TestCensorUsesPageLanguage(t *testing.T) {
	de := `<html lang="de-DE"><body>Du Arschloch, I am a fan of the pot.</body></html>`
	fc := censorFlow(t, models.TextModeCensor, de, nil)
	addons.TextClassifier{}.HandleResponse(fc)
	if got := string(fc.ResponseBody); !strings.Contains(got, "Du *********,") || !strings.Contains(got, "I am a fan of the pot") {
		t.Fatalf("German page: %s", got)
	}

	// No declaration: English only, so German words pass.
	fc = censorFlow(t, models.TextModeCensor, `<html><body>Du Arschloch</body></html>`, nil)
	addons.TextClassifier{}.HandleResponse(fc)
	if strings.Contains(string(fc.ResponseBody), "*") {
		t.Fatalf("undeclared page used a non-English Latin list: %s", fc.ResponseBody)
	}

	// Content-Language is the fallback declaration.
	fc = censorFlow(t, models.TextModeCensor, `<html><body>Du Arschloch</body></html>`, http.Header{"Content-Language": []string{"de"}})
	addons.TextClassifier{}.HandleResponse(fc)
	if !strings.Contains(string(fc.ResponseBody), "*********") {
		t.Fatalf("Content-Language ignored: %s", fc.ResponseBody)
	}

	// Non-Latin scripts are always checked.
	fc = censorFlow(t, models.TextModeCensor, `<html lang="en"><body>看三级片吧</body></html>`, nil)
	addons.TextClassifier{}.HandleResponse(fc)
	if !strings.Contains(string(fc.ResponseBody), "看***吧") {
		t.Fatalf("CJK not censored: %s", fc.ResponseBody)
	}
}

func TestCensorPolicyLanguagesAndExtraWords(t *testing.T) {
	fc := censorFlow(t, models.TextModeCensor, `<html lang="en"><body>Du Arschloch, frak!</body></html>`, nil)
	fc.Policy.TextClassifier.CensorLanguages = []string{"de"}
	fc.Policy.TextClassifier.CensorWords = []string{"frak"}
	addons.TextClassifier{}.HandleResponse(fc)
	if got := string(fc.ResponseBody); got != `<html lang="en"><body>Du *********, ****!</body></html>` {
		t.Fatalf("got %s", got)
	}
}

func TestBothModeBlocksExplicitAndCensorsMild(t *testing.T) {
	fc := censorFlow(t, models.TextModeBoth, "<html>porn xxx hentai</html>", nil)
	addons.TextClassifier{}.HandleResponse(fc)
	if !strings.Contains(string(fc.ResponseBody), "Access Blocked") {
		t.Fatal("both mode did not block an explicit page")
	}

	fc = censorFlow(t, models.TextModeBoth, "<html><body>"+strings.Repeat("Ordinary words here. ", 6)+"This shit printer.</body></html>", nil)
	addons.TextClassifier{}.HandleResponse(fc)
	got := string(fc.ResponseBody)
	if strings.Contains(got, "Access Blocked") || !strings.Contains(got, "This **** printer.") {
		t.Fatalf("both mode on a mild page: %s", got)
	}
}

func TestBlockModeDoesNotCensor(t *testing.T) {
	body := "<html><body>This shit printer.</body></html>"
	fc := censorFlow(t, models.TextModeBlock, body, nil)
	addons.TextClassifier{}.HandleResponse(fc)
	if string(fc.ResponseBody) != body {
		t.Fatalf("block mode modified a page: %s", fc.ResponseBody)
	}
}

func TestStripHTMLDropsScriptAndStyle(t *testing.T) {
	got := addons.StripHTML(`<style>.porn{}</style><p>hello</p><script>var xxx</script>`)
	if strings.Contains(got, "porn") || strings.Contains(got, "xxx") || !strings.Contains(got, "hello") {
		t.Fatalf("StripHTML = %q", got)
	}
}
