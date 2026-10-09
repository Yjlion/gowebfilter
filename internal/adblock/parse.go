// Package adblock implements an ad/tracker blocker driven by standard
// filter lists: Adblock Plus / uBlock Origin syntax (network rules and
// basic element hiding), hosts files, and plain domain lists.
//
// Scope is deliberately conservative. Anything this package does not fully
// understand is skipped rather than approximated, because an approximation
// of an ad filter means blocking things the list author never meant to
// block: a rule with an unsupported option ($redirect, $csp, $removeparam,
// ...) is dropped, as is every procedural or scriptlet cosmetic rule. A
// skipped rule can only let an ad through.
//
// Network matching never uses regexp for ordinary patterns: EasyList alone
// is tens of thousands of rules, and compiling each to a regexp costs far
// too much memory on Android. Patterns are matched with a small glob
// matcher (globMatch) and indexed by their longest literal token, the same
// scheme uBlock Origin and Brave use.
package adblock

import (
	"bufio"
	"io"
	"net"
	"regexp"
	"strings"
)

// Resource types, as a bit mask.
type resourceType uint16

const (
	typeDocument resourceType = 1 << iota
	typeSubdocument
	typeScript
	typeStylesheet
	typeImage
	typeFont
	typeMedia
	typeXHR
	typeObject
	typePing
	typeWebsocket
	typeOther

	typeAllButDocument = typeSubdocument | typeScript | typeStylesheet | typeImage | typeFont |
		typeMedia | typeXHR | typeObject | typePing | typeWebsocket | typeOther
	typeAll = typeAllButDocument | typeDocument
)

var typeNames = map[string]resourceType{
	"document": typeDocument, "doc": typeDocument,
	"subdocument": typeSubdocument, "frame": typeSubdocument,
	"script":     typeScript,
	"stylesheet": typeStylesheet, "css": typeStylesheet,
	"image":          typeImage,
	"font":           typeFont,
	"media":          typeMedia,
	"xmlhttprequest": typeXHR, "xhr": typeXHR,
	"object": typeObject, "object-subrequest": typeObject,
	"ping": typePing, "beacon": typePing,
	"websocket": typeWebsocket,
	"other":     typeOther,
}

// netRule is one parsed network filter.
type netRule struct {
	raw       string
	exception bool
	important bool
	matchCase bool

	// Exactly one matching form is set:
	//   - hostOnly: "||example.com^" (plus options) - matched by hostname
	//     lookup alone, the bulk of every list.
	//   - re: a /regex/ rule.
	//   - pattern: an ABP glob, with its anchors.
	hostOnly    string
	re          *regexp.Regexp
	pattern     string
	anchorHost  bool   // "||"
	anchorStart bool   // leading "|"
	anchorEnd   bool   // trailing "|"
	lit         string // pattern up to its first '*' or '^'

	types      resourceType // 0 = default set (see appliesToType)
	thirdParty int8         // 0 any, 1 third-party only, -1 first-party only
	domains    []string     // $domain= includes (first-party host)
	notDomains []string     // $domain= excludes

	// Exception-only page-level switches (@@...$elemhide / $generichide).
	elemHide    bool
	genericHide bool
}

// cosmeticRule is one element-hiding rule.
type cosmeticRule struct {
	selector   string
	domains    []string // empty = generic
	notDomains []string
	exception  bool // #@#
}

// parsed is the output of parsing one or more lists.
type parsed struct {
	net      []*netRule
	cosmetic []cosmeticRule
	skipped  int
}

var hostsLineRe = regexp.MustCompile(`^(?:0\.0\.0\.0|127\.0\.0\.1|::1?|::)\s+([^\s#]+)`)

// parseList reads one filter list. Format is detected per line: hosts-file
// entries ("0.0.0.0 ads.example") and, in lists that carry no Adblock
// markers, bare domain lines are host rules; everything else is ABP syntax.
func parseList(r io.Reader, into *parsed) (rules int, err error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	abp := false
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if lineNo <= 5 && strings.HasPrefix(line, "[") && strings.Contains(strings.ToLower(line), "adblock") {
			abp = true
			continue
		}
		if strings.HasPrefix(line, "!") {
			abp = true // ABP comment
			continue
		}
		if strings.HasPrefix(line, "#") && !strings.HasPrefix(line, "##") && !strings.HasPrefix(line, "#@#") {
			continue // hosts-file comment
		}
		if m := hostsLineRe.FindStringSubmatch(line); m != nil {
			if h := cleanHost(m[1]); h != "" && h != "localhost" && h != "0.0.0.0" {
				into.net = append(into.net, &netRule{raw: line, hostOnly: h})
				rules++
			}
			continue
		}
		if !abp && isBareDomain(line) {
			into.net = append(into.net, &netRule{raw: line, hostOnly: cleanHost(line)})
			rules++
			continue
		}
		if parseABPLine(line, into) {
			rules++
		} else {
			into.skipped++
		}
	}
	return rules, sc.Err()
}

var bareDomainRe = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?)+\.?$`)

func isBareDomain(s string) bool { return bareDomainRe.MatchString(s) && net.ParseIP(s) == nil }

func cleanHost(h string) string {
	h = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(h), "."))
	if h == "" || strings.ContainsAny(h, "/*^|$ ") {
		return ""
	}
	return h
}

// Cosmetic markers this package does not implement: procedural (#?#), CSS
// injection (#$#), scriptlets (##+js, #%#), HTML filtering (##^).
var unsupportedCosmeticMarkers = []string{"#?#", "#$#", "#%#", "#@?#", "#@$#", "#@%#"}

// Procedural pseudo-classes that are not real CSS; a selector containing
// one would make the browser drop the whole rule (or, worse, be read
// differently), so such rules are skipped.
var proceduralPseudo = []string{
	":has-text(", ":-abp-", ":matches-css", ":xpath(", ":upward(", ":remove(",
	":style(", ":min-text-length(", ":watch-attr(", ":matches-path(",
	":matches-attr(", ":matches-prop(", ":others(", ":if(", ":if-not(",
	":nth-ancestor(", ":remove-attr(", ":remove-class(", ":contains(",
}

func parseABPLine(line string, into *parsed) bool {
	for _, m := range unsupportedCosmeticMarkers {
		if strings.Contains(line, m) {
			return false
		}
	}
	if i := strings.Index(line, "#@#"); i >= 0 {
		return parseCosmetic(line[:i], line[i+3:], true, into)
	}
	if i := strings.Index(line, "##"); i >= 0 {
		return parseCosmetic(line[:i], line[i+2:], false, into)
	}
	r := parseNetRule(line)
	if r == nil {
		return false
	}
	into.net = append(into.net, r)
	return true
}

func parseCosmetic(domainPart, selector string, exception bool, into *parsed) bool {
	selector = strings.TrimSpace(selector)
	if selector == "" || strings.HasPrefix(selector, "+js(") || strings.HasPrefix(selector, "^") {
		return false
	}
	// The selector is injected verbatim into a <style> element, so anything
	// that could end the element or open a declaration block is refused -
	// a list must never be able to inject markup or arbitrary CSS.
	if strings.ContainsAny(selector, "<{}\\") || strings.Contains(strings.ToLower(selector), "</style") {
		return false
	}
	low := strings.ToLower(selector)
	for _, p := range proceduralPseudo {
		if strings.Contains(low, p) {
			return false
		}
	}
	rule := cosmeticRule{selector: selector, exception: exception}
	if domainPart != "" {
		for _, d := range strings.Split(domainPart, ",") {
			d = strings.ToLower(strings.TrimSpace(d))
			switch {
			case d == "":
			case strings.HasPrefix(d, "~"):
				rule.notDomains = append(rule.notDomains, strings.TrimPrefix(d, "~"))
			case strings.ContainsAny(d, "*/"):
				return false // entity (example.*) and regex domains are uBO extensions
			default:
				rule.domains = append(rule.domains, d)
			}
		}
	}
	into.cosmetic = append(into.cosmetic, rule)
	return true
}

// parseNetRule parses one ABP network filter, or returns nil when the
// line uses anything this package does not support.
func parseNetRule(line string) *netRule {
	r := &netRule{raw: line}
	if strings.HasPrefix(line, "@@") {
		r.exception = true
		line = line[2:]
	}

	pattern, opts := line, ""
	// Options follow the last '$' - unless the pattern is a /regex/ whose
	// body contains '$' and no options follow it.
	if i := strings.LastIndexByte(line, '$'); i >= 0 {
		if !(strings.HasPrefix(line, "/") && strings.HasSuffix(line, "/") && len(line) > 1) {
			pattern, opts = line[:i], line[i+1:]
		}
	}
	if opts != "" && !r.parseOptions(opts) {
		return nil
	}

	if len(pattern) > 2 && strings.HasPrefix(pattern, "/") && strings.HasSuffix(pattern, "/") {
		expr := pattern[1 : len(pattern)-1]
		if !r.matchCase {
			expr = "(?i)" + expr
		}
		re, err := regexp.Compile(expr)
		if err != nil {
			return nil // RE2 can't compile lookarounds/backreferences
		}
		r.re = re
		return r
	}

	if strings.HasPrefix(pattern, "||") {
		r.anchorHost = true
		pattern = pattern[2:]
	} else if strings.HasPrefix(pattern, "|") {
		r.anchorStart = true
		pattern = pattern[1:]
	}
	if strings.HasSuffix(pattern, "|") {
		r.anchorEnd = true
		pattern = pattern[:len(pattern)-1]
	}
	if !r.matchCase {
		pattern = strings.ToLower(pattern)
	}
	// A pattern of only wildcards would match every URL; only acceptable
	// with $domain or type restrictions that narrow it, as lists use it.
	if strings.Trim(pattern, "*") == "" && len(r.domains) == 0 && r.types == 0 && !r.exception {
		return nil
	}

	// "||host^" and "||host" with nothing else is a pure hostname rule.
	if r.anchorHost && !r.anchorEnd {
		h := strings.TrimSuffix(pattern, "^")
		if h != "" && !strings.ContainsAny(h, "/*^|?=&:") && strings.Contains(h, ".") {
			r.hostOnly = h
			return r
		}
	}
	r.pattern = pattern
	r.lit = pattern
	if i := strings.IndexAny(pattern, "*^"); i >= 0 {
		r.lit = pattern[:i]
	}
	return r
}

// parseOptions applies a rule's $options; false means the rule uses
// something unsupported and must be skipped.
func (r *netRule) parseOptions(opts string) bool {
	var include, exclude resourceType
	for _, opt := range strings.Split(opts, ",") {
		opt = strings.TrimSpace(opt)
		if opt == "" {
			continue
		}
		name, value, _ := strings.Cut(opt, "=")
		name = strings.ToLower(name)
		neg := strings.HasPrefix(name, "~")
		name = strings.TrimPrefix(name, "~")
		if t, ok := typeNames[name]; ok {
			if neg {
				exclude |= t
			} else {
				include |= t
			}
			continue
		}
		switch name {
		case "third-party", "3p":
			r.thirdParty = 1
			if neg {
				r.thirdParty = -1
			}
		case "first-party", "1p":
			r.thirdParty = -1
			if neg {
				r.thirdParty = 1
			}
		case "domain", "from":
			for _, d := range strings.Split(value, "|") {
				d = strings.ToLower(strings.TrimSpace(d))
				if d == "" {
					continue
				}
				if strings.ContainsAny(d, "*/") {
					return false // entity/regex domains
				}
				if strings.HasPrefix(d, "~") {
					r.notDomains = append(r.notDomains, d[1:])
				} else {
					r.domains = append(r.domains, d)
				}
			}
		case "important":
			r.important = true
		case "match-case":
			r.matchCase = true
		case "all":
			include |= typeAll
		case "elemhide", "ehide":
			if !r.exception {
				return false
			}
			r.elemHide = true
		case "generichide", "ghide":
			if !r.exception {
				return false
			}
			r.genericHide = true
		default:
			// redirect, csp, removeparam, popup, badfilter, header, rewrite,
			// denyallow, to, strict1p/3p, ... are not implemented.
			return false
		}
	}
	switch {
	case include != 0:
		r.types = include &^ exclude
	case exclude != 0:
		r.types = typeAllButDocument &^ exclude
	}
	if (include != 0 || exclude != 0) && r.types == 0 {
		return false
	}
	// A cosmetic-only switch with no network meaning applies to documents.
	if (r.elemHide || r.genericHide) && r.types == 0 {
		r.types = typeDocument
	}
	return true
}
