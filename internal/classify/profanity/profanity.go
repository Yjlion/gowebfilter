// Package profanity finds and masks offensive words in arbitrary text,
// using the multilingual LDNOOBW word lists embedded under words/ (see
// NOTICE). It is shared by the text classifier's censor mode and by the
// textbayes tokenizer, so "what counts as a word" can't drift between
// detection and masking.
//
// Two matching strategies, chosen per entry by script:
//
//   - Space-delimited scripts (Latin, Cyrillic, Arabic, Devanagari, Hangul,
//     ...) match whole tokens only: "class" never matches "ass". Multi-word
//     entries match as a token sequence.
//   - Scripts written without spaces between words (Han, Hiragana,
//     Katakana, Thai, Lao, Khmer, Myanmar) can't be tokenized without a
//     dictionary, so their entries match as substrings. Short entries are
//     dropped there (minUnspacedRunes) because a single Han character is a
//     component of countless neutral words.
//
// Latin-script entries are language-scoped: the same short string is
// profane in one language and an everyday word in another (Turkish "am",
// Dutch "pot", Swedish "fan"), so a Matcher only loads Latin entries for the
// languages it is built for. Entries in any other script can't collide with
// another language's prose in the same way and are always loaded.
package profanity

import (
	"bufio"
	"embed"
	"path"
	"sort"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
)

//go:embed words/*.txt
var wordFS embed.FS

// Unspaced-script entries shorter than these (in letters; Thai vowel and
// tone marks don't count) are dropped. Single Han characters (乳, 性) and
// short Thai syllables (ขี้, กู) are components of everyday words, so
// substring-matching them would mask "dairy" and "lazy".
const (
	minUnspacedHanLetters  = 2 // Han, Hiragana, Katakana
	minUnspacedThaiLetters = 3 // Thai, Lao, Khmer, Myanmar
)

// longEnoughUnspaced applies the minimum-length rule above.
func longEnoughUnspaced(s string) bool {
	han, other := 0, 0
	for _, r := range s {
		switch {
		case !unicode.IsLetter(r):
		case unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana):
			han++
		default:
			other++
		}
	}
	if han > 0 && han+other >= minUnspacedHanLetters {
		return true
	}
	return han+other >= minUnspacedThaiLetters
}

// skipped lists entries that are ordinary, non-offensive words in the
// language they are filed under; masking them would only damage normal
// pages. Keyed "lang:entry" (lowercased).
var skipped = map[string]bool{
	"es:asesinato": true, "es:asno": true, "es:trio": true, "es:nazi": true,
	"nl:del": true, "nl:gat": true, "nl:hol": true, "nl:pot": true,
	"nl:paal": true, "nl:poot": true, "nl:knor": true,
	"sv:sås": true, "sv:hård": true,
	"pt:mama": true, "pt:saco": true,
	"fi:pano": true, "fi:muna": true,
	"it:fava": true, "it:biga": true, "it:mona": true, "it:topa": true,
	"tr:am": true, "tr:amı": true,
	"ru:gol": true, "ru:byk": true,
	"hu:fing": true,
}

// Span is a byte range [Start, End) within the text passed to Find.
type Span struct{ Start, End int }

// Token is one lowercased word of the input and its byte range in the
// original text.
type Token struct {
	Text       string
	Start, End int
}

// IsUnspacedRune reports whether r belongs to a script written without
// spaces between words.
func IsUnspacedRune(r rune) bool {
	if r < 0x0e00 { // below Thai: no unspaced script starts earlier
		return false
	}
	return unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana,
		unicode.Thai, unicode.Lao, unicode.Khmer, unicode.Myanmar)
}

// HasUnspaced reports whether s contains any unspaced-script rune.
func HasUnspaced(s string) bool {
	for _, r := range s {
		if IsUnspacedRune(r) {
			return true
		}
	}
	return false
}

func isWordRune(r rune) bool {
	if r < 0x80 {
		return 'a' <= r && r <= 'z' || 'A' <= r && r <= 'Z' || '0' <= r && r <= '9'
	}
	return unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Mc, r)
}

// Tokenize splits text into lowercased runs of letters, numbers and
// combining marks. Unspaced-script runes act as separators; they are
// matched by substring instead.
func Tokenize(text string) []Token {
	toks := make([]Token, 0, len(text)/8)
	start := -1
	lower := true // token is already lowercase as written: slice, don't copy
	flush := func(end int) {
		if start < 0 {
			return
		}
		tok := text[start:end]
		if !lower {
			tok = strings.ToLower(tok)
		}
		toks = append(toks, Token{Text: tok, Start: start, End: end})
		start = -1
	}
	for i, r := range text {
		if isWordRune(r) && !IsUnspacedRune(r) {
			if start < 0 {
				start, lower = i, true
			}
			if lower && (r >= 0x80 || ('A' <= r && r <= 'Z')) && unicode.ToLower(r) != r {
				lower = false
			}
			continue
		}
		flush(i)
	}
	flush(len(text))
	return toks
}

// isLatinOnly reports whether every letter in s is Latin script (digits and
// punctuation are neutral).
func isLatinOnly(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) && !unicode.Is(unicode.Latin, r) {
			return false
		}
	}
	return true
}

// Entry is one usable word-list entry.
type Entry struct {
	Lang string
	Text string // as written in the list, lowercased
	// Unspaced entries match by substring; others by token sequence.
	Unspaced bool
	// Latin entries are scoped to their language.
	Latin bool
}

var (
	entriesOnce sync.Once
	allEntries  []Entry
	allLangs    []string
)

// Entries returns every usable embedded entry, after the skip list,
// numeric-only and minimum-length filters.
func Entries() []Entry {
	entriesOnce.Do(loadEntries)
	return allEntries
}

// Languages returns the language codes of the embedded lists (file names
// under words/, e.g. "en", "de", "fr-CA-u-sd-caqc").
func Languages() []string {
	entriesOnce.Do(loadEntries)
	return append([]string(nil), allLangs...)
}

func loadEntries() {
	files, err := wordFS.ReadDir("words")
	if err != nil {
		return
	}
	for _, f := range files {
		lang := strings.TrimSuffix(f.Name(), ".txt")
		allLangs = append(allLangs, lang)
		fh, err := wordFS.Open(path.Join("words", f.Name()))
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(fh)
		for sc.Scan() {
			if e, ok := makeEntry(lang, sc.Text()); ok {
				allEntries = append(allEntries, e)
			}
		}
		fh.Close()
	}
	sort.Strings(allLangs)
}

func makeEntry(lang, line string) (Entry, bool) {
	text := strings.ToLower(strings.TrimSpace(line))
	if text == "" || strings.HasPrefix(text, "#") || skipped[lang+":"+text] {
		return Entry{}, false
	}
	e := Entry{Lang: lang, Text: text}
	if HasUnspaced(text) {
		if !longEnoughUnspaced(text) {
			return Entry{}, false
		}
		e.Unspaced = true
		return e, true
	}
	toks := Tokenize(text)
	if len(toks) == 0 {
		return Entry{}, false
	}
	allDigits := true
	for _, t := range toks {
		for _, r := range t.Text {
			if !unicode.IsNumber(r) {
				allDigits = false
			}
		}
	}
	if allDigits {
		return Entry{}, false // "13." would mask every 13 on the page
	}
	e.Latin = isLatinOnly(text)
	return e, true
}

// PhraseKey is the token-sequence form of a spaced entry: its tokens
// joined by single spaces.
func PhraseKey(s string) string {
	toks := Tokenize(s)
	parts := make([]string, len(toks))
	for i, t := range toks {
		parts[i] = t.Text
	}
	return strings.Join(parts, " ")
}

// Matcher finds entries in text. Build with ForLanguages; a Matcher is
// immutable and safe for concurrent use.
type Matcher struct {
	phrases  map[string]struct{}
	firstTok map[string]int // first token -> longest phrase (in tokens) starting with it
	unspaced *SubstringIndex
	empty    bool
}

// SubstringIndex finds fixed strings anywhere in text, case-insensitively,
// preferring the longest entry at each position. It is the unspaced-script
// half of Matcher, exported so textbayes can match its unspaced features
// the same way.
type SubstringIndex struct {
	byFirst map[rune][][]rune
	// asciiFirst marks ASCII runes that start some entry ("sm女王"), so
	// the scan can skip other ASCII cheaply.
	asciiFirst [128]bool
	sorted     bool
}

// NewSubstringIndex builds an index over entries (lowercased here).
func NewSubstringIndex(entries []string) *SubstringIndex {
	x := &SubstringIndex{byFirst: map[rune][][]rune{}}
	for _, e := range entries {
		x.add(e)
	}
	x.finish()
	return x
}

func (x *SubstringIndex) add(entry string) {
	rs := []rune(strings.ToLower(entry))
	if len(rs) == 0 {
		return
	}
	x.byFirst[rs[0]] = append(x.byFirst[rs[0]], rs)
	if rs[0] < 0x80 {
		x.asciiFirst[rs[0]] = true
	}
}

func (x *SubstringIndex) finish() {
	for r, list := range x.byFirst {
		sort.Slice(list, func(i, j int) bool { return len(list[i]) > len(list[j]) })
		x.byFirst[r] = list
	}
}

// Len is the number of first runes indexed (0 means empty).
func (x *SubstringIndex) Len() int { return len(x.byFirst) }

// Scan calls fn for each non-overlapping match, left to right, with the
// matched entry and its byte range in text.
func (x *SubstringIndex) Scan(text string, fn func(entry string, start, end int)) {
	if x == nil || len(x.byFirst) == 0 {
		return
	}
	for i := 0; i < len(text); {
		r, size := utf8.DecodeRuneInString(text[i:])
		lr := lowerFast(r)
		if lr < 0x80 && !x.asciiFirst[lr] {
			i += size
			continue
		}
		matched := false
		for _, cand := range x.byFirst[lr] {
			if e, ok := hasRunePrefix(text, i, cand); ok {
				fn(string(cand), i, e)
				i, matched = e, true
				break
			}
		}
		if !matched {
			i += size
		}
	}
}

// languageFiles maps a BCP-47 tag to the list files it selects: the exact
// file if present, plus the primary subtag ("fr-CA" -> "fr" and
// "fr-CA-u-sd-caqc"; "pt-BR" -> "pt"; "nb"/"nn" -> "no").
func languageFiles(tag string) []string {
	tag = strings.ToLower(strings.TrimSpace(tag))
	if tag == "" {
		return nil
	}
	primary, _, _ := strings.Cut(strings.ReplaceAll(tag, "_", "-"), "-")
	switch primary {
	case "nb", "nn":
		primary = "no"
	case "tl":
		primary = "fil"
	}
	out := []string{primary}
	if primary == "fr" && strings.HasPrefix(tag, "fr-ca") {
		out = append(out, "fr-ca-u-sd-caqc")
	}
	return out
}

var (
	cacheMu sync.Mutex
	cache   = map[string]*Matcher{}
)

// ForLanguages returns a Matcher for the given language tags plus extra
// words. Latin-script entries are loaded only for those languages (English
// when langs is empty); every non-Latin entry is always loaded. extra
// entries (per-policy additions) apply regardless of language. Matchers are
// cached by their inputs.
func ForLanguages(langs []string, extra []string) *Matcher {
	sel := map[string]bool{}
	for _, l := range langs {
		for _, f := range languageFiles(l) {
			sel[f] = true
		}
	}
	if len(sel) == 0 {
		sel["en"] = true
	}
	keys := make([]string, 0, len(sel))
	for k := range sel {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	ex := make([]string, 0, len(extra))
	for _, w := range extra {
		if w = strings.ToLower(strings.TrimSpace(w)); w != "" {
			ex = append(ex, w)
		}
	}
	sort.Strings(ex)
	key := strings.Join(keys, ",") + "\x00" + strings.Join(ex, "\x00")

	cacheMu.Lock()
	defer cacheMu.Unlock()
	if m, ok := cache[key]; ok {
		return m
	}
	m := &Matcher{
		phrases:  map[string]struct{}{},
		firstTok: map[string]int{},
		unspaced: &SubstringIndex{byFirst: map[rune][][]rune{}},
	}
	for _, e := range Entries() {
		if e.Latin && !sel[strings.ToLower(e.Lang)] {
			continue
		}
		m.add(e.Text, e.Unspaced)
	}
	for _, w := range ex {
		m.add(w, HasUnspaced(w))
	}
	m.unspaced.finish()
	m.empty = len(m.phrases) == 0 && m.unspaced.Len() == 0
	if len(cache) > 256 { // bounded: extra word lists come from policies
		cache = map[string]*Matcher{}
	}
	cache[key] = m
	return m
}

func (m *Matcher) add(text string, unspaced bool) {
	if unspaced {
		m.unspaced.add(text)
		return
	}
	toks := Tokenize(text)
	if len(toks) == 0 {
		return
	}
	m.phrases[PhraseKey(text)] = struct{}{}
	if n := len(toks); n > m.firstTok[toks[0].Text] {
		m.firstTok[toks[0].Text] = n
	}
}

// Find returns the byte spans of every match in text, sorted and
// non-overlapping.
func (m *Matcher) Find(text string) []Span {
	if m == nil || m.empty || text == "" {
		return nil
	}
	var spans []Span

	if len(m.phrases) > 0 {
		toks := Tokenize(text)
		for i := 0; i < len(toks); i++ {
			maxN, ok := m.firstTok[toks[i].Text]
			if !ok {
				continue
			}
			if rem := len(toks) - i; rem < maxN {
				maxN = rem
			}
			for n := maxN; n >= 1; n-- {
				if _, hit := m.phrases[joinTokens(toks[i:i+n])]; hit {
					spans = append(spans, Span{toks[i].Start, toks[i+n-1].End})
					i += n - 1
					break
				}
			}
		}
	}

	m.unspaced.Scan(text, func(_ string, start, end int) {
		spans = append(spans, Span{start, end})
	})
	return mergeSpans(spans)
}

// Contains reports whether text has at least one match.
func (m *Matcher) Contains(text string) bool { return len(m.Find(text)) > 0 }

func joinTokens(toks []Token) string {
	if len(toks) == 1 {
		return toks[0].Text
	}
	parts := make([]string, len(toks))
	for i, t := range toks {
		parts[i] = t.Text
	}
	return strings.Join(parts, " ")
}

// lowerFast is unicode.ToLower with shortcuts for ASCII and for the
// caseless CJK/Thai ranges, which dominate the unspaced pass.
func lowerFast(r rune) rune {
	switch {
	case r < 0x80:
		if 'A' <= r && r <= 'Z' {
			return r + ('a' - 'A')
		}
		return r
	case r >= 0x0e00 && r < 0x0f00, r >= 0x3000 && r < 0xa000, r >= 0xac00 && r < 0xd7b0:
		return r
	}
	return unicode.ToLower(r)
}

// hasRunePrefix reports whether text at byte offset i starts with cand
// (compared case-insensitively) and returns the end offset of the match.
func hasRunePrefix(text string, i int, cand []rune) (int, bool) {
	for _, c := range cand {
		if i >= len(text) {
			return 0, false
		}
		r, size := utf8.DecodeRuneInString(text[i:])
		if lowerFast(r) != c {
			return 0, false
		}
		i += size
	}
	return i, true
}

func mergeSpans(spans []Span) []Span {
	if len(spans) < 2 {
		return spans
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].Start < spans[j].Start })
	out := spans[:1]
	for _, s := range spans[1:] {
		last := &out[len(out)-1]
		if s.Start <= last.End {
			if s.End > last.End {
				last.End = s.End
			}
			continue
		}
		out = append(out, s)
	}
	return out
}

// Mask replaces every letter and number inside spans with '*' (one per
// rune, so layout is roughly preserved), drops combining marks, and keeps
// spaces and punctuation.
func Mask(text string, spans []Span) string {
	if len(spans) == 0 {
		return text
	}
	var b strings.Builder
	b.Grow(len(text))
	prev := 0
	for _, s := range spans {
		b.WriteString(text[prev:s.Start])
		for _, r := range text[s.Start:s.End] {
			switch {
			case unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Mc, r):
			case unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.IsSymbol(r):
				b.WriteByte('*')
			default:
				b.WriteRune(r)
			}
		}
		prev = s.End
	}
	b.WriteString(text[prev:])
	return b.String()
}

// Censor masks every match in text and reports how many were masked.
func (m *Matcher) Censor(text string) (string, int) {
	spans := m.Find(text)
	return Mask(text, spans), len(spans)
}
