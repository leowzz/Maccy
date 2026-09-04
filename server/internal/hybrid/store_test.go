package hybrid

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"maccy-server/internal/store"
)

func TestStoreSearchValidatesArgumentsBeforeZvec(t *testing.T) {
	searcher := &Store{}

	if _, err := searcher.Search(context.Background(), "", 10); err == nil {
		t.Fatal("Search with an empty query should fail")
	}
	if _, err := searcher.Search(context.Background(), "query", 0); err == nil {
		t.Fatal("Search with a non-positive limit should fail")
	}
}

func TestStoreUpsertEmptyIsNoop(t *testing.T) {
	if err := (&Store{}).Upsert(context.Background(), nil); err != nil {
		t.Fatalf("empty Upsert should be a no-op: %v", err)
	}
}

func TestStoreCloseIsIdempotent(t *testing.T) {
	searcher := &Store{closed: true}
	if err := searcher.Close(); err != nil {
		t.Fatalf("closing an already closed store should be a no-op: %v", err)
	}
}

func TestValidateEmbedding(t *testing.T) {
	if err := validateEmbedding(make([]float32, EmbeddingDimension)); err != nil {
		t.Fatalf("valid embedding rejected: %v", err)
	}
	if err := validateEmbedding(make([]float32, EmbeddingDimension-1)); err == nil {
		t.Fatal("invalid embedding dimension accepted")
	}
}

func TestMetadataRoundTripAndMismatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "collection.maccy.json")
	expected := collectionMetadataFor("jieba")
	if err := writeMetadata(path, expected); err != nil {
		t.Fatal(err)
	}
	if err := validateMetadata(path, expected); err != nil {
		t.Fatalf("metadata round trip failed: %v", err)
	}
	if err := validateMetadata(path, collectionMetadataFor("standard")); err == nil {
		t.Fatal("metadata tokenizer mismatch was accepted")
	}
	if err := os.WriteFile(path, []byte(`{"schema_version":1,"embedding_model":"local/potion-code-16m-v2","embedding_dimension":256,"embedding_metric":"cosine","fts_tokenizer":"jieba","unexpected":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := validateMetadata(path, expected); err == nil {
		t.Fatal("metadata with unknown field was accepted")
	}
}

func TestContextError(t *testing.T) {
	if err := contextError(nil); err == nil {
		t.Fatal("nil context should fail")
	}
	canceled := context.WithValue(context.Background(), struct{}{}, "value")
	if err := contextError(canceled); err != nil {
		t.Fatalf("active context rejected: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if !errors.Is(contextError(ctx), context.Canceled) {
		t.Fatal("canceled context was not propagated")
	}
}

type testEmbedder struct{}

func (testEmbedder) EmbedOne(context.Context, string) ([]float32, error) {
	return make([]float32, EmbeddingDimension), nil
}

func TestEmbedderContractCompiles(t *testing.T) {
	var _ Embedder = testEmbedder{}
	var _ = store.IndexEntry{}
}
