// Package potion provides a small, in-process Model2Vec runtime for the
// minishlab/potion-code-16M-v2 model.
package potion

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/maruel/safetensors"
	hfTokenizer "github.com/sugarme/tokenizer"
	"github.com/sugarme/tokenizer/normalizer"
	"github.com/sugarme/tokenizer/pretrained"
	"golang.org/x/text/unicode/norm"
)

const (
	// ModelName is the fixed model used by the server's local embedding route.
	ModelName = "local/potion-code-16m-v2"

	// Dimension is the size of one Potion embedding vector.
	Dimension = 256

	// MaxTokens is the maximum number of WordPiece IDs included in pooling.
	MaxTokens = 1024

	modelDirectory = "potion-code-16m-v2"
)

// Model is a loaded Potion static embedding model.
//
// The weights are immutable after Open returns. Tokenizer encoding is guarded
// because the third-party tokenizer package does not promise concurrent use.
type Model struct {
	tokenizer    *hfTokenizer.Tokenizer
	weights      []byte
	vocabSize    int
	unkID        int
	hasUnkID     bool
	padID        int
	hasPadID     bool
	stripAccents bool

	encodeMu sync.Mutex
}

type modelConfig struct {
	Normalize      bool   `json:"normalize"`
	EmbeddingDType string `json:"embedding_dtype"`
}

type tokenizerConfig struct {
	Normalizer *bertNormalizerConfig `json:"normalizer"`
}

type bertNormalizerConfig struct {
	Type               string `json:"type"`
	CleanText          bool   `json:"clean_text"`
	HandleChineseChars bool   `json:"handle_chinese_chars"`
	Lowercase          bool   `json:"lowercase"`
	StripAccents       *bool  `json:"strip_accents"`
}

// Open loads the fixed Potion model from cacheDir, downloading missing files
// from Hugging Face. The returned model is ready for concurrent Embed calls.
func Open(ctx context.Context, cacheDir string) (*Model, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if cacheDir == "" {
		return nil, fmt.Errorf("potion: model cache directory is required")
	}

	dir := filepath.Join(cacheDir, modelDirectory)
	if err := ensureModelFiles(ctx, dir); err != nil {
		return nil, err
	}
	return openModelDir(dir)
}

// OpenFromDir loads a model from an already populated model directory. It is
// useful for offline deployments and tests; the directory must contain the
// fixed model's config.json, tokenizer.json, and model.safetensors files.
func OpenFromDir(dir string) (*Model, error) {
	if dir == "" {
		return nil, fmt.Errorf("potion: model directory is required")
	}
	return openModelDir(dir)
}

func openModelDir(dir string) (*Model, error) {
	configBytes, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		return nil, fmt.Errorf("potion: read config.json: %w", err)
	}
	var config modelConfig
	if err := json.Unmarshal(configBytes, &config); err != nil {
		return nil, fmt.Errorf("potion: parse config.json: %w", err)
	}
	if !config.Normalize {
		return nil, fmt.Errorf("potion: config.json must enable normalize")
	}
	if config.EmbeddingDType != "float16" {
		return nil, fmt.Errorf("potion: unsupported embedding dtype %q", config.EmbeddingDType)
	}

	tokenizerPath := filepath.Join(dir, "tokenizer.json")
	tokenizerBytes, err := os.ReadFile(tokenizerPath)
	if err != nil {
		return nil, fmt.Errorf("potion: read tokenizer.json: %w", err)
	}
	var tokenizerSpec tokenizerConfig
	if err := json.Unmarshal(tokenizerBytes, &tokenizerSpec); err != nil {
		return nil, fmt.Errorf("potion: parse tokenizer.json: %w", err)
	}
	if tokenizerSpec.Normalizer == nil || tokenizerSpec.Normalizer.Type != "BertNormalizer" {
		return nil, fmt.Errorf("potion: tokenizer must use BertNormalizer")
	}
	tk, err := pretrained.FromFile(tokenizerPath)
	if err != nil {
		return nil, fmt.Errorf("potion: load tokenizer.json: %w", err)
	}
	stripAccents := tokenizerSpec.Normalizer.StripAccents
	if stripAccents == nil {
		// Hugging Face's BertNormalizer uses lowercase as the default for
		// strip_accents when the JSON value is null.
		value := tokenizerSpec.Normalizer.Lowercase
		stripAccents = &value
	}
	tk.WithNormalizer(normalizer.NewBertNormalizer(
		tokenizerSpec.Normalizer.CleanText,
		tokenizerSpec.Normalizer.Lowercase,
		tokenizerSpec.Normalizer.HandleChineseChars,
		*stripAccents,
	))
	// zvec-grep's Model2Vec runtime applies its own 1024-ID truncation before
	// dropping [UNK]. The published tokenizer JSON contains a 512-token hint,
	// which would be the wrong order if enabled by the Go loader.
	tk.WithTruncation(nil)
	unkID, hasUnkID := tk.TokenToId("[UNK]")
	padID, hasPadID := tk.TokenToId("[PAD]")

	weightsBytes, err := os.ReadFile(filepath.Join(dir, "model.safetensors"))
	if err != nil {
		return nil, fmt.Errorf("potion: read model.safetensors: %w", err)
	}
	file, err := safetensors.Parse(weightsBytes)
	if err != nil {
		return nil, fmt.Errorf("potion: parse model.safetensors: %w", err)
	}
	if len(file.Tensors) != 1 || file.Tensors[0].Name != "embeddings" {
		return nil, fmt.Errorf("potion: expected one embeddings tensor, got %d tensors", len(file.Tensors))
	}
	tensor := file.Tensors[0]
	if tensor.DType != safetensors.F16 {
		return nil, fmt.Errorf("potion: embeddings tensor must be F16, got %s", tensor.DType)
	}
	if len(tensor.Shape) != 2 || tensor.Shape[1] != Dimension || tensor.Shape[0] == 0 {
		return nil, fmt.Errorf("potion: embeddings tensor must have shape [vocab,%d], got %v", Dimension, tensor.Shape)
	}
	if tensor.Shape[0] > uint64(maxInt()) {
		return nil, fmt.Errorf("potion: vocabulary is too large: %d", tensor.Shape[0])
	}
	vocabSize := int(tensor.Shape[0])
	if tk.GetVocabSize(false) != vocabSize {
		return nil, fmt.Errorf("potion: tokenizer vocab size %d does not match embeddings rows %d", tk.GetVocabSize(false), vocabSize)
	}

	return &Model{
		tokenizer:    tk,
		weights:      tensor.Data,
		vocabSize:    vocabSize,
		unkID:        unkID,
		hasUnkID:     hasUnkID,
		padID:        padID,
		hasPadID:     hasPadID,
		stripAccents: *stripAccents,
	}, nil
}

// Embed returns one normalized 256-dimensional vector per input text.
func (m *Model) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if m == nil || m.tokenizer == nil {
		return nil, fmt.Errorf("potion: model is nil")
	}
	result := make([][]float32, len(texts))
	for i, text := range texts {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		ids, err := m.tokenIDs(text)
		if err != nil {
			return nil, fmt.Errorf("potion: tokenize input %d: %w", i, err)
		}
		vector := make([]float32, Dimension)
		for _, id := range ids {
			if id < 0 || id >= m.vocabSize {
				return nil, fmt.Errorf("potion: tokenizer produced invalid token id %d", id)
			}
			row := m.weights[id*Dimension*2:]
			for j := 0; j < Dimension; j++ {
				vector[j] += halfToFloat32(binary.LittleEndian.Uint16(row[j*2:]))
			}
		}
		if len(ids) == 0 {
			// This is the Model2Vec result for empty input or input containing
			// only unknown tokens: a zero vector remains zero after normalize.
			result[i] = vector
			continue
		}
		invCount := float32(1) / float32(len(ids))
		var normSquared float64
		for j := range vector {
			vector[j] *= invCount
			normSquared += float64(vector[j]) * float64(vector[j])
		}
		norm := float32(math.Sqrt(normSquared))
		if math.IsNaN(float64(norm)) || math.IsInf(float64(norm), 0) {
			return nil, fmt.Errorf("potion: input %d produced a zero or invalid norm", i)
		}
		if norm == 0 {
			result[i] = vector
			continue
		}
		for j := range vector {
			vector[j] /= norm
		}
		result[i] = vector
	}
	return result, nil
}

// EmbedOne returns one normalized vector for text.
func (m *Model) EmbedOne(ctx context.Context, text string) ([]float32, error) {
	vectors, err := m.Embed(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	return vectors[0], nil
}

// tokenIDs applies the complete Hugging Face tokenizer pipeline and returns
// IDs only, with no synthetic special tokens added by the caller.
func (m *Model) tokenIDs(text string) ([]int, error) {
	ids, err := m.encodeIDs(text)
	if err != nil {
		return nil, err
	}
	if len(ids) > MaxTokens {
		// zvec-grep's Model2Vec runtime truncates IDs before removing [UNK].
		ids = ids[:MaxTokens]
	}
	if m.hasUnkID {
		known := ids[:0]
		for _, id := range ids {
			if id != m.unkID {
				known = append(known, id)
			}
		}
		ids = known
	}
	return append([]int(nil), ids...), nil
}

func (m *Model) encodeIDs(text string) ([]int, error) {
	text = normalizeTokenizerInput(text, m.stripAccents)
	if !m.hasPadID {
		return m.encodeChunk(text)
	}

	var ids []int
	start := 0
	for pos := 0; pos+len("[PAD]") <= len(text); pos++ {
		if !strings.EqualFold(text[pos:pos+len("[PAD]")], "[PAD]") || !isWholeAddedPad(text, pos) {
			continue
		}
		chunk, err := m.encodeChunk(text[start:pos])
		if err != nil {
			return nil, err
		}
		ids = append(ids, chunk...)
		ids = append(ids, m.padID)
		start = pos + len("[PAD]")
		pos = start - 1
	}
	chunk, err := m.encodeChunk(text[start:])
	if err != nil {
		return nil, err
	}
	return append(ids, chunk...), nil
}

func (m *Model) encodeChunk(text string) ([]int, error) {
	m.encodeMu.Lock()
	defer m.encodeMu.Unlock()
	encoding, err := m.tokenizer.EncodeSingle(text, false)
	if err != nil {
		return nil, err
	}
	return append([]int(nil), encoding.GetIds()...), nil
}

func isWholeAddedPad(text string, start int) bool {
	if start > 0 {
		previous, _ := utf8.DecodeLastRuneInString(text[:start])
		if isAddedTokenWordRune(previous) {
			return false
		}
	}
	end := start + len("[PAD]")
	if end < len(text) {
		next, _ := utf8.DecodeRuneInString(text[end:])
		if isAddedTokenWordRune(next) {
			return false
		}
	}
	return true
}

func isAddedTokenWordRune(value rune) bool {
	return value == '_' || unicode.IsLetter(value) || unicode.IsMark(value) || unicode.IsDigit(value)
}

func normalizeTokenizerInput(text string, stripAccents bool) string {
	if stripAccents {
		// sugarme/tokenizer removes combining marks but does not first apply
		// NFD, while Hugging Face's BertNormalizer does both.
		text = norm.NFD.String(text)
	}
	return strings.Map(func(value rune) rune {
		if stripAccents && unicode.Is(unicode.Mn, value) {
			return -1
		}
		if unicode.IsSpace(value) {
			return ' '
		}
		return value
	}, text)
}

func maxInt() int {
	return int(^uint(0) >> 1)
}
