//go:build integration

package hybrid

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"maccy-server/internal/potion"
	"maccy-server/internal/store"
)

type integrationEmbedder struct{}

func (integrationEmbedder) EmbedOne(_ context.Context, text string) ([]float32, error) {
	vector := make([]float32, EmbeddingDimension)
	if text == "alpha" {
		vector[0] = 1
	} else {
		vector[1] = 1
	}
	return vector, nil
}

func TestStoreWithPublishedPotion(t *testing.T) {
	modelDir := os.Getenv("POTION_TEST_MODEL_DIR")
	if modelDir == "" {
		t.Skip("POTION_TEST_MODEL_DIR is not set")
	}
	jiebaDictPath := os.Getenv("ZVEC_JIEBA_DICT_DIR")
	if os.Getenv("ZVEC_LIBRARY_PATH") == "" || jiebaDictPath == "" {
		t.Skip("ZVEC_LIBRARY_PATH and ZVEC_JIEBA_DICT_DIR must be set")
	}
	embedder, err := potion.OpenFromDir(modelDir)
	if err != nil {
		t.Fatal(err)
	}
	searcher, err := NewStore(context.Background(), Config{
		CollectionPath:  filepath.Join(t.TempDir(), "collection"),
		Embedder:        embedder,
		JiebaDictPath:   jiebaDictPath,
		FTSTokenizer:    "jieba",
		RRFRankConstant: 60,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = searcher.Close() }()

	if err := searcher.Upsert(context.Background(), []store.IndexEntry{
		{ID: "entry-database", PlainText: "PostgreSQL schema migration scripts"},
		{ID: "entry-search", PlainText: "Build a local semantic vector search engine in Go"},
		{ID: "entry-clipboard", PlainText: "macOS clipboard manager preferences"},
	}); err != nil {
		t.Fatal(err)
	}
	results, err := searcher.Search(context.Background(), "semantic vector retrieval in Go", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 || results[0].ID != "entry-search" {
		t.Fatalf("unexpected Potion hybrid results: %#v", results)
	}
}

func TestStoreWithRealZvec(t *testing.T) {
	if os.Getenv("ZVEC_LIBRARY_PATH") == "" {
		t.Skip("ZVEC_LIBRARY_PATH is not set")
	}
	jiebaDictPath := os.Getenv("ZVEC_JIEBA_DICT_DIR")
	if jiebaDictPath == "" {
		t.Skip("ZVEC_JIEBA_DICT_DIR is not set")
	}
	collectionPath := filepath.Join(t.TempDir(), "collection")
	searcher, err := NewStore(context.Background(), Config{
		CollectionPath:  collectionPath,
		Embedder:        integrationEmbedder{},
		JiebaDictPath:   jiebaDictPath,
		FTSTokenizer:    "jieba",
		RRFRankConstant: 60,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = searcher.Close() }()

	if err := searcher.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
	entries := []store.IndexEntry{
		{ID: "entry-alpha", PlainText: "alpha"},
		{ID: "entry-beta", PlainText: "beta"},
	}
	if err := searcher.Upsert(context.Background(), entries); err != nil {
		t.Fatal(err)
	}
	if err := searcher.Upsert(context.Background(), entries); err != nil {
		t.Fatal(err)
	}
	stats, err := searcher.collection.GetStats()
	if err != nil {
		t.Fatal(err)
	}
	if stats.DocCount != uint64(len(entries)) {
		t.Fatalf("duplicate upsert changed document count: got %d, want %d", stats.DocCount, len(entries))
	}
	results, err := searcher.Search(context.Background(), "alpha", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 || results[0].ID != "entry-alpha" {
		t.Fatalf("unexpected hybrid results: %#v", results)
	}
}
