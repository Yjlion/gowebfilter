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

// voidElements never have an end tag, so they are not pushed on the
// element stack censorHTML keeps for lang inheritance.
var voidElements = map[string]bool{
	"area": true, "base": true, "br": true, "col": true, "embed": true, "hr": true,
	"img": true, "input": true, "link": true, "meta": true, "param": true,
	"source": true, "track": true, "wbr": true,
}

// censorFrame is one open element: its name and the matcher its text uses.
type censorFrame struct {
	name string
	m    *profanity.Matcher
	skip bool // inside a censorSkipTags element
}

// censorHTML masks matched words in the text content of an HTML document
// and returns the new body and the number of masked matches. Everything
// other than text nodes (tags, attributes, comments, and the contents of
// censorSkipTags) is copied through byte-for-byte, so an unmatched page
// comes back unchanged and a matched one differs only inside its text.
// If the document can't be tokenized to the end, the original body is
// returned untouched.
//
// Text is matched with the page's matcher, except inside an element that
// declares its own language (<blockquote lang="de">): when forLang is not
// nil, that subtree uses forLang(lang) instead, as HTML's lang inheritance
// says it should. A quoted German sentence on an English page is German.
func censorHTML(body []byte, page *profanity.Matcher, forLang func(string) *profanity.Matcher) ([]byte, int) {
	z := html.NewTokenizer(bytes.NewReader(body))
	var out bytes.Buffer
	out.Grow(len(body))
	total := 0
	stack := []censorFrame{{m: page}}
	top := func() censorFrame { return stack[len(stack)-1] }
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
			name, hasAttr := z.TagName()
			tag := string(name)
			if voidElements[tag] {
				break
			}
			f := censorFrame{name: tag, m: top().m, skip: top().skip || censorSkipTags[tag]}
			for hasAttr && forLang != nil {
				var k, v []byte
				k, v, hasAttr = z.TagAttr()
				if string(k) == "lang" && len(v) > 0 {
					f.m = forLang(string(v))
				}
			}
			stack = append(stack, f)
		case html.EndTagToken:
			name, _ := z.TagName()
			// Pop to the matching element; real-world HTML leaves <p> and
			// <li> unclosed, and an end tag with no open match is ignored.
			for i := len(stack) - 1; i > 0; i-- {
				if stack[i].name == string(name) {
					stack = stack[:i]
					break
				}
			}
		case html.TextToken:
			if f := top(); !f.skip {
				// Mask the raw source text: entity references (&amp;) are
				// split off as punctuation by the tokenizer, so they never
				// land inside a match and survive intact.
				masked, n := f.m.Censor(string(raw))
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
