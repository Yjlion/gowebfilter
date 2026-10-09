package adblock

import (
	"io"
	"regexp"
	"sort"
	"strings"

	"golang.org/x/net/publicsuffix"
)

// Request describes one request to match.
type Request struct {
	// URL is the full request URL.
	URL string
	// Host is the request hostname (no port).
	Host string
	// SourceHost is the host of the page that issued the request (from
	// Referer / Origin). Empty for a top-level navigation or when unknown.
	SourceHost string
	// SourceURL is the issuing page's URL when known (Referer).
	SourceURL string
	// Type is the resource type ("document", "script", "image", ...).
	Type string
}

// Verdict is the outcome of Match.
type Verdict struct {
	Blocked bool
	// Rule is the matching filter (the blocking rule, or the exception
	// that overrode one).
	Rule string
}

// Engine is a compiled, immutable set of filter lists.
type Engine struct {
	// Network rules, split by action and indexed for lookup.
	block, allow ruleIndex
	// allowDocument are exceptions with $document/$elemhide/$generichide,
	// matched against the source page rather than the request.
	allowDocument ruleIndex

	// Cosmetic rules.
	genericByKey  map[string][]string // .class / #id key -> selectors
	genericNoKey  []string
	genericExcept map[string][]string // selector -> domains it is excepted on ("" = everywhere)
	specific      map[string][]string // domain -> selectors
	specificNot   map[string]map[string]bool
	exceptBySite  map[string]map[string]bool // domain -> excepted selectors

	NetworkRules  int
	CosmeticRules int
	Skipped       int
}

// ruleIndex buckets rules for fast lookup.
type ruleIndex struct {
	byHost  map[string][]*netRule // hostOnly rules, by exact host
	byToken map[string][]*netRule // pattern rules, by their best literal token
	generic []*netRule            // pattern/regex rules with no usable token
}

func (x *ruleIndex) add(r *netRule) {
	switch {
	case r.hostOnly != "":
		if x.byHost == nil {
			x.byHost = map[string][]*netRule{}
		}
		x.byHost[r.hostOnly] = append(x.byHost[r.hostOnly], r)
	case r.re != nil:
		x.generic = append(x.generic, r)
	default:
		tok := bestToken(r)
		if tok == "" {
			x.generic = append(x.generic, r)
			return
		}
		if x.byToken == nil {
			x.byToken = map[string][]*netRule{}
		}
		x.byToken[tok] = append(x.byToken[tok], r)
	}
}

// maxGenericSelectorsNoKey caps generic cosmetic selectors that can't be
// keyed to a class or id in the page (see CosmeticCSS).
const maxGenericSelectorsNoKey = 1500

// Compile builds an Engine from list readers.
func Compile(lists ...io.Reader) (*Engine, error) {
	var p parsed
	for _, r := range lists {
		if _, err := parseList(r, &p); err != nil {
			return nil, err
		}
	}
	return build(&p), nil
}

func build(p *parsed) *Engine {
	e := &Engine{
		genericByKey:  map[string][]string{},
		genericExcept: map[string][]string{},
		specific:      map[string][]string{},
		specificNot:   map[string]map[string]bool{},
		exceptBySite:  map[string]map[string]bool{},
		Skipped:       p.skipped,
	}
	for _, r := range p.net {
		e.NetworkRules++
		switch {
		case r.exception && (r.elemHide || r.genericHide || r.types&typeDocument != 0 && r.types&^typeDocument == 0):
			e.allowDocument.add(r)
		case r.exception:
			e.allow.add(r)
		default:
			e.block.add(r)
		}
	}
	for _, c := range p.cosmetic {
		e.CosmeticRules++
		switch {
		case c.exception && len(c.domains) == 0:
			e.genericExcept[c.selector] = append(e.genericExcept[c.selector], "")
		case c.exception:
			for _, d := range c.domains {
				if e.exceptBySite[d] == nil {
					e.exceptBySite[d] = map[string]bool{}
				}
				e.exceptBySite[d][c.selector] = true
			}
		case len(c.domains) == 0:
			if key := selectorKey(c.selector); key != "" {
				e.genericByKey[key] = append(e.genericByKey[key], c.selector)
			} else if len(e.genericNoKey) < maxGenericSelectorsNoKey {
				e.genericNoKey = append(e.genericNoKey, c.selector)
			}
			for _, d := range c.notDomains {
				e.genericExcept[c.selector] = append(e.genericExcept[c.selector], d)
			}
		default:
			for _, d := range c.domains {
				e.specific[d] = append(e.specific[d], c.selector)
			}
			if len(c.notDomains) > 0 {
				if e.specificNot[c.selector] == nil {
					e.specificNot[c.selector] = map[string]bool{}
				}
				for _, d := range c.notDomains {
					e.specificNot[c.selector][d] = true
				}
			}
		}
	}
	return e
}

// Match decides one request.
func (e *Engine) Match(req Request) Verdict {
	if e == nil {
		return Verdict{}
	}
	ctx := newMatchCtx(req)

	// A $document exception on the source page (or on the navigation
	// itself) switches blocking off for everything the page loads.
	if r := e.documentException(ctx, false); r != nil {
		return Verdict{Rule: r.raw}
	}

	blocking := e.block.find(ctx)
	if blocking == nil {
		return Verdict{}
	}
	if !blocking.important {
		if exc := e.allow.find(ctx); exc != nil {
			return Verdict{Rule: exc.raw}
		}
	}
	return Verdict{Blocked: true, Rule: blocking.raw}
}

// pageCtx is the match context for the page a request belongs to: the
// request itself for a navigation, else its source page.
func (c *matchCtx) pageCtx() *matchCtx {
	if c.typ == typeDocument || c.sourceURL == "" {
		if c.typ == typeDocument {
			return c
		}
		return nil
	}
	if c.page == nil {
		c.page = newMatchCtx(Request{URL: c.sourceURL, Host: c.sourceHost, Type: "document"})
	}
	return c.page
}

// documentException returns a page-level exception for the request's page:
// $document only (cosmetic=false), or $document/$elemhide (cosmetic=true).
func (e *Engine) documentException(ctx *matchCtx, cosmetic bool) *netRule {
	pctx := ctx.pageCtx()
	if pctx == nil {
		return nil
	}
	return e.allowDocument.first(pctx, func(r *netRule) bool {
		isDoc := r.types&typeDocument != 0 && !r.elemHide && !r.genericHide
		return isDoc || cosmetic && r.elemHide
	})
}

func (e *Engine) genericHidden(host, pageURL string) bool {
	pctx := newMatchCtx(Request{URL: pageURL, Host: host, Type: "document"})
	return e.allowDocument.first(pctx, func(r *netRule) bool { return r.genericHide }) != nil
}

// first returns the first rule accepted by keep that matches at page level.
func (x *ruleIndex) first(ctx *matchCtx, keep func(*netRule) bool) *netRule {
	var found *netRule
	x.each(ctx, func(r *netRule) bool {
		if keep(r) && r.matches(ctx, true) {
			found = r
			return true
		}
		return false
	})
	return found
}

// each calls fn for every candidate rule for ctx until fn returns true.
func (x *ruleIndex) each(ctx *matchCtx, fn func(*netRule) bool) {
	visit := func(rules []*netRule) bool {
		for _, r := range rules {
			if fn(r) {
				return true
			}
		}
		return false
	}
	if x.byHost != nil {
		for h := ctx.host; h != ""; h = parentDomain(h) {
			if visit(x.byHost[h]) {
				return
			}
		}
	}
	if x.byToken != nil {
		for _, tok := range ctx.tokens() {
			if visit(x.byToken[tok]) {
				return
			}
		}
	}
	visit(x.generic)
}

// find returns the first rule in the index matching ctx, preferring an
// $important rule: it stops at the first important match, otherwise
// returns the first ordinary one.
func (x *ruleIndex) find(ctx *matchCtx) *netRule {
	var first *netRule
	x.each(ctx, func(r *netRule) bool {
		if (first == nil || r.important) && r.matches(ctx, false) {
			first = r
			return r.important
		}
		return false
	})
	return first
}

// matchCtx carries the per-request values every rule check needs.
type matchCtx struct {
	url, urlLower string
	host          string
	sourceURL     string
	sourceHost    string
	typ           resourceType
	thirdParty    bool
	hostStarts    []int // offsets in url where a host label starts
	toks          []string
	page          *matchCtx // lazily built by pageCtx
}

func newMatchCtx(req Request) *matchCtx {
	c := &matchCtx{
		url:        req.URL,
		urlLower:   strings.ToLower(req.URL),
		host:       strings.ToLower(strings.TrimSuffix(req.Host, ".")),
		sourceURL:  req.SourceURL,
		sourceHost: strings.ToLower(req.SourceHost),
		typ:        parseType(req.Type),
	}
	if c.sourceHost != "" && c.host != "" {
		c.thirdParty = registrable(c.host) != registrable(c.sourceHost)
	}
	// Host label starts, for "||" anchoring.
	if i := strings.Index(c.urlLower, "://"); i >= 0 {
		start := i + 3
		end := start
		for end < len(c.urlLower) && !strings.ContainsRune("/?#", rune(c.urlLower[end])) {
			end++
		}
		c.hostStarts = append(c.hostStarts, start)
		for j := start; j < end; j++ {
			if c.urlLower[j] == '.' || c.urlLower[j] == '@' {
				c.hostStarts = append(c.hostStarts, j+1)
			}
		}
	}
	return c
}

func (c *matchCtx) tokens() []string {
	if c.toks == nil {
		seen := map[string]bool{}
		c.toks = []string{}
		u := c.urlLower
		for i := 0; i < len(u); {
			if !isTokenByte(u[i]) {
				i++
				continue
			}
			j := i
			for j < len(u) && isTokenByte(u[j]) {
				j++
			}
			if t := u[i:j]; len(t) >= minTokenLen && !seen[t] && !stopTokens[t] {
				seen[t] = true
				c.toks = append(c.toks, t)
			}
			i = j
		}
	}
	return c.toks
}

func parseType(s string) resourceType {
	if t, ok := typeNames[strings.ToLower(s)]; ok {
		return t
	}
	return typeOther
}

func registrable(host string) string {
	if d, err := publicsuffix.EffectiveTLDPlusOne(host); err == nil {
		return d
	}
	return host
}

func parentDomain(h string) string {
	i := strings.IndexByte(h, '.')
	if i < 0 {
		return ""
	}
	return h[i+1:]
}

// domainMatches reports whether host equals d or is a subdomain of it.
func domainMatches(host, d string) bool {
	if len(host) == len(d) {
		return host == d
	}
	return len(host) > len(d) && host[len(host)-len(d)-1] == '.' && host[len(host)-len(d):] == d
}

// matches applies a rule's options and pattern. pageLevel skips the type
// defaulting for $document exception lookups.
func (r *netRule) matches(c *matchCtx, pageLevel bool) bool {
	// Type: an explicit mask must include the request's type. With no type
	// options, a rule applies to everything except documents - except a pure
	// hostname rule, which (as in uBlock Origin) also blocks navigating to
	// the host, since "||ads.example^" leaves nothing on that host worth
	// loading.
	switch {
	case r.types != 0:
		if r.types&c.typ == 0 {
			return false
		}
	case c.typ == typeDocument && r.hostOnly == "" && !pageLevel:
		return false
	}
	if r.thirdParty == 1 && !c.thirdParty || r.thirdParty == -1 && c.thirdParty {
		return false
	}
	if len(r.domains) > 0 || len(r.notDomains) > 0 {
		src := c.sourceHost
		if src == "" || c.typ == typeDocument {
			src = c.host
		}
		for _, d := range r.notDomains {
			if domainMatches(src, d) {
				return false
			}
		}
		if len(r.domains) > 0 {
			ok := false
			for _, d := range r.domains {
				if domainMatches(src, d) {
					ok = true
					break
				}
			}
			if !ok {
				return false
			}
		}
	}

	switch {
	case r.hostOnly != "":
		return domainMatches(c.host, r.hostOnly)
	case r.re != nil:
		return r.re.MatchString(c.url)
	}
	subject := c.urlLower
	if r.matchCase {
		subject = c.url
	}
	switch {
	case r.anchorHost:
		for _, s := range c.hostStarts {
			if globMatch(r.pattern, subject[s:], true, r.anchorEnd) {
				return true
			}
		}
		return false
	case r.anchorStart:
		return globMatch(r.pattern, subject, true, r.anchorEnd)
	default:
		// Unanchored: try only where the pattern's leading literal occurs,
		// rather than backtracking from every offset of the URL.
		if lit := r.literalPrefix(); lit != "" {
			for off := 0; ; {
				i := strings.Index(subject[off:], lit)
				if i < 0 {
					return false
				}
				if globMatch(r.pattern, subject[off+i:], true, r.anchorEnd) {
					return true
				}
				off += i + 1
			}
		}
		return globMatch(r.pattern, subject, false, r.anchorEnd)
	}
}

// literalPrefix is the pattern up to its first wildcard or separator
// (precomputed by parseNetRule).
func (r *netRule) literalPrefix() string { return r.lit }

// isSeparator is ABP's '^': anything but a letter, digit, or _ - . %.
func isSeparator(b byte) bool {
	switch {
	case 'a' <= b && b <= 'z', 'A' <= b && b <= 'Z', '0' <= b && b <= '9':
		return false
	case b == '_' || b == '-' || b == '.' || b == '%':
		return false
	}
	return true
}

// globMatch matches an ABP pattern ('*' = any run, '^' = one separator or
// the end of input) against s. anchored pins the match to the start of s,
// anchorEnd to its end; otherwise the pattern may match anywhere. The
// unanchored ends are virtual '*'s, so nothing is allocated per call.
func globMatch(p, s string, anchored, anchorEnd bool) bool {
	pi, si := 0, 0
	hasStar, resume, mark := !anchored, 0, 0
	for {
		if pi == len(p) && (!anchorEnd || si == len(s)) {
			return true
		}
		if si == len(s) {
			break
		}
		if pi < len(p) {
			switch c := p[pi]; {
			case c == '*':
				hasStar, resume, mark = true, pi+1, si
				pi++
				continue
			case c == '^' && isSeparator(s[si]):
				pi++
				si++
				continue
			case c != '^' && c == s[si]:
				pi++
				si++
				continue
			}
		}
		if !hasStar {
			return false
		}
		mark++
		pi, si = resume, mark
	}
	// End of input: the rest of the pattern may only be '*' and '^'
	// (a trailing separator matches the end of the URL).
	for pi < len(p) && (p[pi] == '*' || p[pi] == '^') {
		pi++
	}
	return pi == len(p)
}

var tokenRe = regexp.MustCompile(`[a-z0-9%]+`)

func isTokenByte(b byte) bool {
	return 'a' <= b && b <= 'z' || '0' <= b && b <= '9' || b == '%'
}

const minTokenLen = 3

// stopTokens appear in nearly every URL; indexing a rule under one would
// make its bucket a linear scan of thousands of rules for every request.
var stopTokens = map[string]bool{
	"http": true, "https": true, "www": true, "com": true, "net": true,
	"org": true, "html": true, "php": true, "static": true, "assets": true,
	"cdn": true, "min": true, "png": true, "jpg": true, "gif": true,
	"css": true, "img": true, "images": true,
}

// bestToken picks the longest literal run of a pattern that must appear as
// a whole URL token: bounded on both sides by a non-token character or an
// anchor, never by a '*' (which could extend it).
func bestToken(r *netRule) string {
	p := r.pattern
	if r.matchCase {
		p = strings.ToLower(p)
	}
	best := ""
	for _, loc := range tokenRe.FindAllStringIndex(p, -1) {
		s, e := loc[0], loc[1]
		leftOK := s > 0 && p[s-1] != '*' || s == 0 && (r.anchorHost || r.anchorStart)
		rightOK := e < len(p) && p[e] != '*' || e == len(p) && r.anchorEnd
		if leftOK && rightOK && e-s >= minTokenLen && e-s > len(best) && !stopTokens[p[s:e]] {
			best = p[s:e]
		}
	}
	return best
}

var selectorKeyRe = regexp.MustCompile(`^[#.]([A-Za-z0-9_-]+)`)

// selectorKey is the class or id a generic selector starts with
// ("#ad-banner" -> "ad-banner"), used to include the selector only on
// pages that mention that name.
func selectorKey(sel string) string {
	if m := selectorKeyRe.FindStringSubmatch(sel); m != nil {
		return m[1]
	}
	return ""
}

var (
	classAttrRe = regexp.MustCompile(`(?i)\b(?:class|id)\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s>]+))`)
)

// pageNames collects every class and id name in an HTML document.
func pageNames(html []byte) map[string]bool {
	names := map[string]bool{}
	for _, m := range classAttrRe.FindAllSubmatch(html, -1) {
		v := m[1]
		if v == nil {
			v = m[2]
		}
		if v == nil {
			v = m[3]
		}
		for _, n := range strings.Fields(string(v)) {
			names[n] = true
		}
	}
	return names
}

// CosmeticSelectors returns the element-hiding selectors for a page:
// every site-specific selector for its host, plus generic selectors whose
// class/id appears in the page (sending EasyList's full generic set to
// every page would add hundreds of kilobytes). Exceptions are applied.
func (e *Engine) CosmeticSelectors(host, pageURL string, html []byte) []string {
	if e == nil {
		return nil
	}
	host = strings.ToLower(host)
	ctx := newMatchCtx(Request{URL: pageURL, Host: host, Type: "document"})
	if r := e.documentException(ctx, true); r != nil {
		return nil
	}
	excepted := func(sel string) bool {
		for h := host; h != ""; h = parentDomain(h) {
			if e.exceptBySite[h][sel] {
				return true
			}
		}
		for _, d := range e.genericExcept[sel] {
			if d == "" || domainMatches(host, d) {
				return true
			}
		}
		return false
	}

	seen := map[string]bool{}
	var out []string
	add := func(sel string) {
		if !seen[sel] && !excepted(sel) {
			seen[sel] = true
			out = append(out, sel)
		}
	}
	for h := host; h != ""; h = parentDomain(h) {
		for _, sel := range e.specific[h] {
			skip := false
			for d := range e.specificNot[sel] {
				if domainMatches(host, d) {
					skip = true
					break
				}
			}
			if !skip {
				add(sel)
			}
		}
	}
	if !e.genericHidden(host, pageURL) {
		for name := range pageNames(html) {
			for _, sel := range e.genericByKey[name] {
				add(sel)
			}
		}
		for _, sel := range e.genericNoKey {
			add(sel)
		}
	}
	sort.Strings(out)
	return out
}

// CosmeticCSS renders selectors as a stylesheet. Each selector gets its own
// rule: a browser drops a whole rule when one selector in its list is
// invalid, so grouping would let one bad selector unhide many.
func CosmeticCSS(selectors []string) string {
	if len(selectors) == 0 {
		return ""
	}
	var b strings.Builder
	for _, s := range selectors {
		b.WriteString(s)
		b.WriteString("{display:none!important}\n")
	}
	return b.String()
}
