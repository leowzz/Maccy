package potion

import (
	"bytes"
	"context"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maruel/safetensors"
)

func TestSyntheticModelEmbedding(t *testing.T) {
	dir := writeSyntheticModel(t)
	model, err := OpenFromDir(dir)
	if err != nil {
		t.Fatal(err)
	}

	vectors, err := model.Embed(context.Background(), []string{"a b", "a z", ""})
	if err != nil {
		t.Fatal(err)
	}
	if len(vectors) != 3 || len(vectors[0]) != Dimension {
		t.Fatalf("unexpected output shape: %#v", vectors)
	}
	if math.Abs(float64(vectors[0][0]-float32(math.Sqrt(0.5)))) > 1e-6 || math.Abs(float64(vectors[0][1]-float32(math.Sqrt(0.5)))) > 1e-6 {
		t.Fatalf("mean pooling mismatch: first=%v second=%v", vectors[0][0], vectors[0][1])
	}
	if vectors[1][0] != 1 || vectors[1][1] != 0 {
		t.Fatalf("unknown token was not dropped: %v", vectors[1][:2])
	}
	for i, value := range vectors[2] {
		if value != 0 {
			t.Fatalf("empty input coordinate %d = %v, want zero", i, value)
		}
	}
}

func TestSyntheticModelTruncatesBeforeFilteringUnknown(t *testing.T) {
	dir := writeSyntheticModel(t)
	model, err := OpenFromDir(dir)
	if err != nil {
		t.Fatal(err)
	}

	// The first ID is [UNK], followed by 1023 known "a" IDs and a final
	// known "b" ID. zvec-grep's Model2Vec runtime truncates to 1024 IDs,
	// then removes [UNK], so the final "b" is not pooled.
	text := "z " + strings.TrimSpace(strings.Repeat("a ", MaxTokens-1)) + " b"
	vector, err := model.EmbedOne(context.Background(), text)
	if err != nil {
		t.Fatal(err)
	}
	if vector[1] != 0 || vector[0] != 1 {
		t.Fatalf("truncation-before-unknown behavior mismatch: %v", vector[:2])
	}
}

func writeSyntheticModel(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"normalize":true,"embedding_dtype":"float16"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	const tokenizerJSON = `{
  "version": "1.0",
  "truncation": null,
  "padding": null,
  "added_tokens": [
    {"id": 0, "content": "[PAD]", "single_word": true, "lstrip": true, "rstrip": true, "normalized": true, "special": true},
    {"id": 1, "content": "[UNK]", "single_word": false, "lstrip": false, "rstrip": false, "normalized": false, "special": true}
  ],
  "normalizer": {"type": "BertNormalizer", "clean_text": true, "handle_chinese_chars": true, "lowercase": true, "strip_accents": null},
  "pre_tokenizer": {"type": "BertPreTokenizer"},
  "post_processor": null,
  "decoder": {"type": "WordPiece", "prefix": "##", "cleanup": true},
  "model": {"type": "WordPiece", "unk_token": "[UNK]", "continuing_subword_prefix": "##", "max_input_chars_per_word": 100, "vocab": {"[PAD]": 0, "[UNK]": 1, "a": 2, "b": 3, "c": 4}}
}`
	if err := os.WriteFile(filepath.Join(dir, "tokenizer.json"), []byte(tokenizerJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	weights := make([]byte, 5*Dimension*2)
	setHalf(weights, 2, 0, 0x3c00)
	setHalf(weights, 3, 1, 0x3c00)
	setHalf(weights, 4, 0, 0x3c00)
	setHalf(weights, 4, 1, 0x3c00)
	file := &safetensors.File{Tensors: []safetensors.Tensor{{
		Name:  "embeddings",
		DType: safetensors.F16,
		Shape: []uint64{5, Dimension},
		Data:  weights,
	}}}
	var encoded bytes.Buffer
	if err := file.Serialize(&encoded); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "model.safetensors"), encoded.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func setHalf(data []byte, row, column int, value uint16) {
	index := (row*Dimension + column) * 2
	data[index] = byte(value)
	data[index+1] = byte(value >> 8)
}
