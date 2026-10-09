package addons

import (
	"bytes"
	"errors"
	"io"
	"regexp"
	"strings"

	"golang.org/x/net/html"

	"github.com/yjlion/gowebfilter/internal/classify/profanity"
)

// censorSkipTags are elements whose text content is code or form data
// rather than prose. Masking inside them would break scripts and
// stylesheets, or silently alter what a user submits from a <textarea>.
var censorSkipTags = map[string]bool{
	"script": true, "style": true, "textarea": true, "noscript": true,
	"xmp": true, "plaintext": true, "iframe": true, "noembed": true,
	"noframes": true, "template": true, "code": true, "pre": true,
}

var htmlLangRe = regexp.MustCompile(`(?is)<html\b[^>]*?\blang\s*=\s*["']?([A-Za-z]{2,3}(?:[-_][A-Za-z0-9]+)*)`)

// censorLanguages picks the languages whose Latin-script word lists apply:
// the policy's explicit list, else the page's own declaration
// (<html lang>, then Content-Language), else English (ForLanguages'
// default for an empty list).
func censorLanguages(configured []string, body []byte, contentLanguage string) []string {
	if len(configured) > 0 {
		return configured
	}
	head := body
	if len(head) > 8192 {
		head = head[:8192]
	}
	if m := htmlLangRe.FindSubmatch(head); m != nil {
		return []string{string(m[1])}
	}
	var langs []string
	for _, l := range strings.Split(contentLanguage, ",") {
		if l = strings.TrimSpace(l); l != "" {
			langs = append(langs, l)
		}
	}
	return langs
}

// censorHTML masks matched words in the text content of an HTML document
// and returns the new body and the number of masked matches. Everything
// other than text nodes (tags, attributes, comments, and the contents of
// censorSkipTags) is copied through byte-for-byte, so an unmatched page
// comes back unchanged and a matched one differs only inside its text.
// If the document can't be tokenized to the end, the original body is
// returned untouched.
func censorHTML(body []byte, m *profanity.Matcher) ([]byte, int) {
	z := html.NewTokenizer(bytes.NewReader(body))
	var out bytes.Buffer
	out.Grow(len(body))
	total := 0
	skipDepth := 0
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			if !errors.Is(z.Err(), io.EOF) {
				return body, 0
			}
			break
		}
		raw := z.Raw()
		switch tt {
		case html.StartTagToken:
			name, _ := z.TagName()
			if censorSkipTags[string(name)] {
				skipDepth++
			}
		case html.EndTagToken:
			name, _ := z.TagName()
			if censorSkipTags[string(name)] && skipDepth > 0 {
				skipDepth--
			}
		case html.TextToken:
			if skipDepth == 0 {
				// Mask the raw source text: entity references (&amp;) are
				// split off as punctuation by the tokenizer, so they never
				// land inside a match and survive intact.
				masked, n := m.Censor(string(raw))
				if n > 0 {
					total += n
					out.WriteString(masked)
					continue
				}
			}
		}
		out.Write(raw)
	}
	if total == 0 {
		return body, 0
	}
	return out.Bytes(), total
}
