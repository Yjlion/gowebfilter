// Package text implements the ONNX-backed NSFW text scorer for
// internal/proxy/addons.TextClassifier: an export of
// eliasalbouzidi/distilbert-nsfw-text-classifier (DistilBERT, Apache-2.0)
// run through github.com/yalue/onnxruntime_go, replacing the project's
// earlier untrained pure-Go TF-IDF scorer. This package requires
// CGO_ENABLED=1, a C toolchain, and the onnxruntime shared library
// available at runtime (see internal/classify/onnxrt and HANDOFF.md).
//
// Load reads a model directory produced by scripts/export_text_model.py:
//
//   - model.onnx    - the exported DistilBERT sequence-classification graph
//   - vocab.txt     - WordPiece vocabulary, one token per line (index = id)
//   - config.json   - a minimal derived config (NOT the full HuggingFace
//     config.json - just what this package needs):
//     {"max_position_embeddings": 512, "do_lower_case": true,
//     "id2label": {"0": "safe", "1": "nsfw"}, "pad_token_id": 0,
//     "unk_token_id": 100, "cls_token_id": 101, "sep_token_id": 102}
package text

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	ort "github.com/yalue/onnxruntime_go"

	"github.com/yjlion/gowebfilter/internal/classify/onnxrt"
)

// maxSeqLen caps the tokenizer's sequence length independent of the
// model's own max_position_embeddings (512 for DistilBERT) - proxied
// response bodies are scored per-request, and running full 512-token
// BERT inference on every response would be needless latency when
// NSFW-signaling content concentrates well within the first couple hundred
// tokens anyway. Load takes the smaller of this and the model's own limit.
const maxSeqLen = 256

type modelConfig struct {
	MaxPositionEmbeddings int               `json:"max_position_embeddings"`
	DoLowerCase           bool              `json:"do_lower_case"`
	ID2Label              map[string]string `json:"id2label"`
	PadTokenID            int64             `json:"pad_token_id"`
	UnkTokenID            int64             `json:"unk_token_id"`
	ClsTokenID            int64             `json:"cls_token_id"`
	SepTokenID            int64             `json:"sep_token_id"`
}

func loadModelConfig(path string) (modelConfig, error) {
	var cfg modelConfig
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, fmt.Errorf("read config: %w", err)
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("parse config %s: %w", path, err)
	}
	if cfg.MaxPositionEmbeddings <= 0 {
		return cfg, fmt.Errorf("config %s: max_position_embeddings must be positive", path)
	}
	if len(cfg.ID2Label) == 0 {
		return cfg, fmt.Errorf("config %s: id2label must not be empty", path)
	}
	return cfg, nil
}

// nsfwLabelIndex finds which output logit index corresponds to the "nsfw"
// label (case-insensitive) rather than assuming index 1, so a future
// re-export with a different label order can't silently invert the score.
func nsfwLabelIndex(id2label map[string]string) (int, error) {
	for idxStr, label := range id2label {
		if strings.EqualFold(label, "nsfw") {
			idx, err := strconv.Atoi(idxStr)
			if err != nil {
				return 0, fmt.Errorf("id2label key %q is not a valid index: %w", idxStr, err)
			}
			return idx, nil
		}
	}
	return 0, fmt.Errorf(`id2label has no "nsfw" entry (got %v)`, id2label)
}

// Model implements addons.MLScorer via an ONNX-exported DistilBERT
// sequence-classification model - see the package doc for the on-disk
// model directory format Load expects.
type Model struct {
	// onnxruntime does not document AdvancedSession.Run as safe for
	// concurrent use from multiple goroutines sharing the same
	// input/output tensors, so Score serializes calls - same rationale as
	// internal/classify/image's detector.
	mu sync.Mutex

	session *ort.AdvancedSession
	inputs  map[string]*ort.Tensor[int64] // keyed by the model's own input names
	logits  *ort.Tensor[float32]

	tok     *Tokenizer
	nsfwIdx int
}

// Load reads a model directory (see package doc) and returns a ready-to-use
// addons.MLScorer. An empty dir returns (nil, nil): keyword-only,
// matching addons.TextClassifier's fail-open contract for a deployment
// that hasn't configured the ML stage at all.
func Load(dir string) (*Model, error) {
	if dir == "" {
		return nil, nil
	}
	if err := onnxrt.EnsureEnvironment(); err != nil {
		return nil, fmt.Errorf("text classifier: initialize onnxruntime: %w", err)
	}

	cfg, err := loadModelConfig(filepath.Join(dir, "config.json"))
	if err != nil {
		return nil, fmt.Errorf("text classifier: %w", err)
	}
	nsfwIdx, err := nsfwLabelIndex(cfg.ID2Label)
	if err != nil {
		return nil, fmt.Errorf("text classifier: %w", err)
	}

	maxLen := maxSeqLen
	if cfg.MaxPositionEmbeddings < maxLen {
		maxLen = cfg.MaxPositionEmbeddings
	}

	tok, err := LoadTokenizer(filepath.Join(dir, "vocab.txt"), TokenizerConfig{
		DoLowerCase: cfg.DoLowerCase,
		MaxLen:      maxLen,
		ClsID:       cfg.ClsTokenID,
		SepID:       cfg.SepTokenID,
		PadID:       cfg.PadTokenID,
		UnkID:       cfg.UnkTokenID,
	})
	if err != nil {
		return nil, fmt.Errorf("text classifier: %w", err)
	}

	m, err := newSession(filepath.Join(dir, "model.onnx"), maxLen, len(cfg.ID2Label))
	if err != nil {
		return nil, fmt.Errorf("text classifier: %w", err)
	}
	m.tok = tok
	m.nsfwIdx = nsfwIdx
	return m, nil
}

// Score implements addons.MLScorer: a probability in [0,1] that text is
// NSFW, derived by softmax over the model's output logits.
func (m *Model) Score(text string) (float64, bool) {
	if m == nil {
		return 0, false
	}
	inputIDs, attentionMask := m.tok.Encode(text)

	m.mu.Lock()
	defer m.mu.Unlock()
	copy(m.inputs["input_ids"].GetData(), inputIDs)
	if mask, ok := m.inputs["attention_mask"]; ok {
		copy(mask.GetData(), attentionMask)
	}
	if err := m.session.Run(); err != nil {
		return 0, false
	}
	return softmaxAt(m.logits.GetData(), m.nsfwIdx), true
}

// softmaxAt returns softmax(logits)[idx] without allocating the full
// softmax vector, using the standard max-subtraction for numerical
// stability.
func softmaxAt(logits []float32, idx int) float64 {
	maxLogit := logits[0]
	for _, l := range logits {
		if l > maxLogit {
			maxLogit = l
		}
	}
	var sum float64
	for _, l := range logits {
		sum += math.Exp(float64(l - maxLogit))
	}
	return math.Exp(float64(logits[idx]-maxLogit)) / sum
}
