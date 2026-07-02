package text

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// TokenizerConfig holds the pieces of a BERT/DistilBERT tokenizer_config.json
// this package actually needs - see the minimal config.json format documented
// in model.go, derived by scripts/export_text_model.py from the full
// HuggingFace config rather than round-tripping every field.
type TokenizerConfig struct {
	DoLowerCase                       bool
	MaxLen                            int
	ClsID, SepID, PadID, UnkID int64
}

// Tokenizer is a from-scratch Go implementation of BERT's WordPiece
// tokenization (BasicTokenizer + greedy-longest-match subword splitting),
// matching bert-base-uncased's scheme: lowercase, accent-stripping,
// whitespace/punctuation pre-tokenization, then per-token subword splitting
// against vocab.txt using the "##" continuation-piece convention. See
// tokenizer_test.go for fixtures cross-checked against the real HF
// tokenizer via `uv run --with transformers ...`.
type Tokenizer struct {
	vocab map[string]int64
	cfg   TokenizerConfig
}

// LoadTokenizer reads vocabPath (one token per line, line number = id - the
// format every HF BERT-family vocab.txt uses) and returns a ready-to-use
// Tokenizer.
func LoadTokenizer(vocabPath string, cfg TokenizerConfig) (*Tokenizer, error) {
	f, err := os.Open(vocabPath)
	if err != nil {
		return nil, fmt.Errorf("open vocab file: %w", err)
	}
	defer f.Close()

	vocab := make(map[string]int64)
	scanner := bufio.NewScanner(f)
	var idx int64
	for scanner.Scan() {
		vocab[scanner.Text()] = idx
		idx++
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read vocab file: %w", err)
	}
	if len(vocab) == 0 {
		return nil, fmt.Errorf("vocab file %s is empty", vocabPath)
	}
	return &Tokenizer{vocab: vocab, cfg: cfg}, nil
}

// Encode tokenizes text into [CLS] ... [SEP] input_ids plus a matching
// attention_mask, padded or truncated to t.cfg.MaxLen - the standard single-
// sequence BERT input format for a sequence-classification head.
func (t *Tokenizer) Encode(text string) (inputIDs, attentionMask []int64) {
	ids := []int64{t.cfg.ClsID}
	for _, tok := range basicTokenize(text, t.cfg.DoLowerCase) {
		ids = append(ids, wordpiece(tok, t.vocab, t.cfg.UnkID)...)
	}

	maxLen := t.cfg.MaxLen
	if len(ids) >= maxLen {
		ids = ids[:maxLen-1]
	}
	ids = append(ids, t.cfg.SepID)

	mask := make([]int64, len(ids))
	for i := range mask {
		mask[i] = 1
	}
	for len(ids) < maxLen {
		ids = append(ids, t.cfg.PadID)
		mask = append(mask, 0)
	}
	return ids, mask
}

// basicTokenize implements BERT's BasicTokenizer: clean control/whitespace
// characters, space out CJK characters so they tokenize individually,
// lowercase + strip accents if doLowerCase, then split off punctuation as
// its own tokens.
func basicTokenize(text string, doLowerCase bool) []string {
	text = cleanText(text)
	text = spaceOutCJK(text)

	var tokens []string
	for _, tok := range strings.Fields(text) {
		if doLowerCase {
			tok = stripAccents(strings.ToLower(tok))
		}
		tokens = append(tokens, splitOnPunctuation(tok)...)
	}
	return tokens
}

func cleanText(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r == 0 || r == 0xFFFD || isControl(r) {
			continue
		}
		if isWhitespace(r) {
			b.WriteRune(' ')
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func isWhitespace(r rune) bool {
	switch r {
	case ' ', '\t', '\n', '\r':
		return true
	}
	return unicode.Is(unicode.Zs, r)
}

func isControl(r rune) bool {
	switch r {
	case '\t', '\n', '\r':
		return false
	}
	return unicode.IsControl(r)
}

// spaceOutCJK inserts spaces around CJK ideographs so they become
// individual tokens, matching BertTokenizer's tokenize_chinese_chars
// default (true).
func spaceOutCJK(s string) string {
	var b strings.Builder
	for _, r := range s {
		if isCJK(r) {
			b.WriteRune(' ')
			b.WriteRune(r)
			b.WriteRune(' ')
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func isCJK(r rune) bool {
	return (r >= 0x4E00 && r <= 0x9FFF) ||
		(r >= 0x3400 && r <= 0x4DBF) ||
		(r >= 0x20000 && r <= 0x2A6DF) ||
		(r >= 0x2A700 && r <= 0x2B73F) ||
		(r >= 0x2B740 && r <= 0x2B81F) ||
		(r >= 0x2B820 && r <= 0x2CEAF) ||
		(r >= 0xF900 && r <= 0xFAFF) ||
		(r >= 0x2F800 && r <= 0x2FA1F)
}

// stripAccents decomposes s (Unicode NFD) and drops nonspacing combining
// marks - e.g. "café" -> "cafe" - matching BertTokenizer's
// strip_accents=True behavior (the default when do_lower_case=True).
func stripAccents(s string) string {
	var b strings.Builder
	for _, r := range norm.NFD.String(s) {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// isPunctuation matches BERT's own _is_punctuation: the ASCII ranges it
// explicitly treats as punctuation (which include symbols like '$'/'+'/'^'
// that Unicode itself categorizes as Sc/Sm/Sk, not P*) unioned with
// Unicode's general punctuation categories.
func isPunctuation(r rune) bool {
	if (r >= 33 && r <= 47) || (r >= 58 && r <= 64) || (r >= 91 && r <= 96) || (r >= 123 && r <= 126) {
		return true
	}
	return unicode.IsPunct(r)
}

func splitOnPunctuation(tok string) []string {
	var out []string
	var cur []rune
	for _, r := range tok {
		if isPunctuation(r) {
			if len(cur) > 0 {
				out = append(out, string(cur))
				cur = nil
			}
			out = append(out, string(r))
		} else {
			cur = append(cur, r)
		}
	}
	if len(cur) > 0 {
		out = append(out, string(cur))
	}
	return out
}

// maxWordPieceChars matches BERT's own WordpieceTokenizer default
// (max_input_chars_per_word=100, confirmed against the live HF tokenizer:
// a 100-char run of "a" still splits normally, a 101-char run collapses to
// a single [UNK]): a basic-token longer than this is treated as unknown
// outright rather than attempted.
const maxWordPieceChars = 100

// wordpiece greedy-longest-match-first subword-splits a single basic token
// against vocab, using the "##" continuation-piece prefix convention.
// Returns [unkID] if no split succeeds (including for a token longer than
// maxWordPieceChars).
func wordpiece(token string, vocab map[string]int64, unkID int64) []int64 {
	runes := []rune(token)
	if len(runes) == 0 {
		return nil
	}
	if len(runes) > maxWordPieceChars {
		return []int64{unkID}
	}

	var out []int64
	start := 0
	for start < len(runes) {
		end := len(runes)
		id := int64(-1)
		for end > start {
			sub := string(runes[start:end])
			if start > 0 {
				sub = "##" + sub
			}
			if v, ok := vocab[sub]; ok {
				id = v
				break
			}
			end--
		}
		if id == -1 {
			return []int64{unkID}
		}
		out = append(out, id)
		start = end
	}
	return out
}
