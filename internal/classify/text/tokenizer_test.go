package text

import (
	"reflect"
	"strings"
	"testing"
)

// testConfig mirrors eliasalbouzidi/distilbert-nsfw-text-classifier's
// tokenizer_config.json special-token ids.
func testConfig(maxLen int) TokenizerConfig {
	return TokenizerConfig{
		DoLowerCase: true,
		MaxLen:      maxLen,
		ClsID:       101,
		SepID:       102,
		PadID:       0,
		UnkID:       100,
	}
}

func loadTestTokenizer(t *testing.T, maxLen int) *Tokenizer {
	t.Helper()
	tok, err := LoadTokenizer("testdata/vocab.txt", testConfig(maxLen))
	if err != nil {
		t.Fatalf("LoadTokenizer: %v", err)
	}
	return tok
}

// The expected outputs below were captured directly from the real
// tokenizer this model ships (via
// `uv run --with transformers -- python -c "..."` against
// eliasalbouzidi/distilbert-nsfw-text-classifier's actual tokenizer_config,
// not guessed by analogy to a different BERT vocab), so these are
// ground-truth fixtures, not hand-estimates.
func TestBasicTokenizeMatchesRealTokenizer(t *testing.T) {
	cases := []struct {
		text string
		want []string
	}{
		{"hello", []string{"hello"}},                         // whole word, in-vocab
		{"unbelievable", []string{"unbelievable"}},            // also a whole word in this vocab
		{"tokenization", []string{"token", "##ization"}},      // multi-subword split
		{"unhappiness", []string{"un", "##ha", "##pp", "##iness"}},
		{"hello, world!", []string{"hello", ",", "world", "!"}}, // punctuation split
		{"don't stop", []string{"don", "'", "t", "stop"}},
		{"café", []string{"cafe"}},  // accent stripping
		{"naïve", []string{"naive"}},
	}
	tok := loadTestTokenizer(t, 32)
	for _, tc := range cases {
		basic := basicTokenize(tc.text, true)
		var got []string
		for _, b := range basic {
			got = append(got, wordpieceStrings(b, tok.vocab)...)
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("tokenize(%q) = %v, want %v", tc.text, got, tc.want)
		}
	}
}

// wordpieceStrings is wordpiece's test-only counterpart: same algorithm,
// returns the matched vocab strings instead of ids, so test cases can
// assert against human-readable tokens like the HF tokenizer's own
// tokenize() output.
func wordpieceStrings(token string, vocab map[string]int64) []string {
	id2tok := make(map[int64]string, len(vocab))
	for k, v := range vocab {
		id2tok[v] = k
	}
	var out []string
	for _, id := range wordpiece(token, vocab, 100) {
		out = append(out, id2tok[id])
	}
	return out
}

func TestEncodeUnknownTokenFallback(t *testing.T) {
	tok := loadTestTokenizer(t, 16)
	// A rune sequence with no plausible entry in an English WordPiece
	// vocab - the real tokenizer maps this to a single [UNK].
	ids, mask := tok.Encode("\U0001F600")
	// [CLS] [UNK] [SEP] then padding.
	if ids[0] != 101 || ids[1] != 100 || ids[2] != 102 {
		t.Fatalf("Encode(emoji) ids = %v, want [CLS]=101 [UNK]=100 [SEP]=102 prefix", ids)
	}
	if mask[0] != 1 || mask[1] != 1 || mask[2] != 1 || mask[3] != 0 {
		t.Fatalf("Encode(emoji) attention_mask = %v, want [1,1,1,0,...]", mask)
	}
}

func TestEncodeCJKSpacedAndPartiallyKnown(t *testing.T) {
	tok := loadTestTokenizer(t, 16)
	// Real tokenizer: '中文测试' -> ['中', '文', '[UNK]', '[UNK]'] - first two
	// characters happen to be individually in-vocab, the other two aren't.
	ids, _ := tok.Encode("中文测试")
	// [CLS] 中 文 [UNK] [UNK] [SEP] ...
	nonPad := idsUntilPad(ids)
	if len(nonPad) != 6 { // CLS + 4 tokens + SEP
		t.Fatalf("Encode(CJK) non-pad ids = %v, want 6 entries (CLS + 4 + SEP)", nonPad)
	}
	if nonPad[3] != 100 || nonPad[4] != 100 {
		t.Fatalf("Encode(CJK) ids = %v, want positions 3,4 to be [UNK]=100", nonPad)
	}
}

func idsUntilPad(ids []int64) []int64 {
	for i, id := range ids {
		if id == 0 && i > 0 {
			return ids[:i]
		}
	}
	return ids
}

func TestEncodePadsToMaxLen(t *testing.T) {
	tok := loadTestTokenizer(t, 16)
	ids, mask := tok.Encode("hello")
	if len(ids) != 16 || len(mask) != 16 {
		t.Fatalf("Encode() len(ids)=%d len(mask)=%d, want 16/16", len(ids), len(mask))
	}
	// [CLS] hello [SEP] then 13 pads.
	want := []int64{101, 7592, 102, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	if !reflect.DeepEqual(ids, want) {
		t.Fatalf("Encode(\"hello\") ids = %v, want %v", ids, want)
	}
	wantMask := []int64{1, 1, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	if !reflect.DeepEqual(mask, wantMask) {
		t.Fatalf("Encode(\"hello\") mask = %v, want %v", mask, wantMask)
	}
}

func TestEncodeTruncatesToMaxLen(t *testing.T) {
	tok := loadTestTokenizer(t, 5)
	// Longer than fits: [CLS] + 6 words + [SEP] must truncate to 5 total.
	ids, mask := tok.Encode("the quick brown fox jumps over")
	if len(ids) != 5 || len(mask) != 5 {
		t.Fatalf("Encode() len = %d/%d, want 5/5", len(ids), len(mask))
	}
	if ids[0] != 101 || ids[4] != 102 {
		t.Fatalf("Encode() truncated ids = %v, want to start with CLS and end with SEP", ids)
	}
	for _, m := range mask {
		if m != 1 {
			t.Fatalf("Encode() truncated mask = %v, want all 1s (no padding when truncated)", mask)
		}
	}
}

func TestWordpieceLongTokenBecomesUnknown(t *testing.T) {
	tok := loadTestTokenizer(t, 8)
	// Confirmed against the real tokenizer: a 100-char run of "a" still
	// splits normally, a 101-char run collapses to a single [UNK].
	basic100 := wordpiece(strings.Repeat("a", 100), tok.vocab, tok.cfg.UnkID)
	if len(basic100) == 1 && basic100[0] == tok.cfg.UnkID {
		t.Fatalf("wordpiece(100 chars) = %v, want a real multi-piece split, not [UNK]", basic100)
	}
	basic101 := wordpiece(strings.Repeat("a", 101), tok.vocab, tok.cfg.UnkID)
	if len(basic101) != 1 || basic101[0] != tok.cfg.UnkID {
		t.Fatalf("wordpiece(101 chars) = %v, want single [UNK]=%d", basic101, tok.cfg.UnkID)
	}
}
