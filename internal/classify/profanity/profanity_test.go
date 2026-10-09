package profanity

import (
	"strings"
	"testing"
)

func TestEntriesCoverEveryLanguage(t *testing.T) {
	langs := Languages()
	if len(langs) < 25 {
		t.Fatalf("Languages() = %d files, want every LDNOOBW list (%v)", len(langs), langs)
	}
	per := map[string]int{}
	for _, e := range Entries() {
		per[e.Lang]++
	}
	for _, l := range []string{"en", "de", "es", "fr", "ja", "zh", "ru", "ar", "th", "ko", "hi"} {
		if per[l] == 0 {
			t.Errorf("no usable entries for %q", l)
		}
	}
}

func TestFindWholeWordsOnly(t *testing.T) {
	m := ForLanguages([]string{"en"}, nil)
	if spans := m.Find("A classic class assessment"); len(spans) != 0 {
		t.Fatalf("matched inside words: %v", spans)
	}
	text := "What the Fuck is this shit?"
	spans := m.Find(text)
	if len(spans) != 2 {
		t.Fatalf("Find = %v, want 2 spans", spans)
	}
	if got := text[spans[0].Start:spans[0].End]; got != "Fuck" {
		t.Fatalf("first span = %q", got)
	}
}

func TestLatinEntriesAreLanguageScoped(t *testing.T) {
	en := ForLanguages([]string{"en"}, nil)
	if en.Contains("Du bist ein Arschloch") {
		t.Fatal("German entry matched on an English matcher")
	}
	de := ForLanguages([]string{"de-DE"}, nil)
	if !de.Contains("Du bist ein Arschloch") {
		t.Fatal("German entry not matched for de-DE")
	}
	// Turkish "am" is skipped outright; it must never mask English text.
	tr := ForLanguages([]string{"tr"}, nil)
	if tr.Contains("I am here") {
		t.Fatal(`Turkish "am" matched English prose`)
	}
}

func TestNonLatinEntriesAlwaysLoaded(t *testing.T) {
	m := ForLanguages(nil, nil) // defaults to English for Latin entries
	for _, s := range []string{"ты хуй", "这是三级片", "섹스 영상", "ไอ้ควาย", "SM女王"} {
		if !m.Contains(s) {
			t.Errorf("non-Latin entry not matched in %q", s)
		}
	}
}

func TestShortUnspacedEntriesDropped(t *testing.T) {
	m := ForLanguages(nil, nil)
	// 乳 (milk/breast) and 性 are single-character list entries; as
	// substrings they would mask "dairy industry" and "nature".
	for _, s := range []string{"乳业公司", "性格很好", "ขี้เกียจ"} {
		if m.Contains(s) {
			t.Errorf("short unspaced entry matched neutral text %q (%v)", s, m.Find(s))
		}
	}
}

func TestNumericEntriesDropped(t *testing.T) {
	m := ForLanguages(nil, nil)
	if m.Contains("Chapter 13. Room 13") {
		t.Fatal(`numeric entry "13." matched`)
	}
}

func TestMultiWordPhrase(t *testing.T) {
	m := ForLanguages([]string{"en"}, nil)
	text := "a red  Ball-Gag here"
	spans := m.Find(text)
	if len(spans) != 1 || text[spans[0].Start:spans[0].End] != "Ball-Gag" {
		t.Fatalf("Find = %v", spans)
	}
}

func TestExtraWords(t *testing.T) {
	m := ForLanguages([]string{"en"}, []string{"Frak", "  "})
	if !m.Contains("what the frak") {
		t.Fatal("extra word not matched")
	}
	if ForLanguages([]string{"en"}, nil).Contains("what the frak") {
		t.Fatal("extra word leaked into the cached default matcher")
	}
}

func TestMaskKeepsLayout(t *testing.T) {
	m := ForLanguages([]string{"en"}, nil)
	got, n := m.Censor("oh shit, a fuck-up")
	if n != 2 {
		t.Fatalf("Censor count = %d, want 2 (%q)", n, got)
	}
	if got != "oh ****, a ****-up" {
		t.Fatalf("Censor = %q", got)
	}
	got, _ = ForLanguages(nil, nil).Censor("看三级片吧")
	if got != "看***吧" {
		t.Fatalf("CJK Censor = %q", got)
	}
}

func TestCensorUnchangedWhenClean(t *testing.T) {
	text := "Ordinary text about gardening, " + strings.Repeat("soil ", 50)
	got, n := ForLanguages([]string{"en"}, nil).Censor(text)
	if n != 0 || got != text {
		t.Fatalf("clean text changed: n=%d", n)
	}
}

func TestTokenize(t *testing.T) {
	toks := Tokenize("Größe: ÜBER-cool 42")
	var got []string
	for _, tk := range toks {
		got = append(got, tk.Text)
	}
	if strings.Join(got, "|") != "größe|über|cool|42" {
		t.Fatalf("Tokenize = %v", got)
	}
}

func BenchmarkFind(b *testing.B) {
	m := ForLanguages([]string{"en", "de"}, nil)
	text := strings.Repeat("The quick brown fox jumps over the lazy dog. 日本語のテキスト。 ", 2000)
	b.SetBytes(int64(len(text)))
	for i := 0; i < b.N; i++ {
		m.Find(text)
	}
}
