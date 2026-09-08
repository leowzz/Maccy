package hybrid

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"

	"maccy-server/internal/store"

	zvec "github.com/zvec-ai/zvec-go"
)

const (
	// EmbeddingDimension is the fixed dimension of the Potion Model2Vec model.
	EmbeddingDimension = 256

	collectionName   = "maccy_clipboard_entries"
	textFieldName    = "plain_text"
	vectorFieldName  = "embedding"
	embeddingModel   = "local/potion-code-16m-v2"
	embeddingMetric  = "cosine"
	schemaVersion    = 1
	defaultTokenizer = "jieba"
)

// Embedder supplies local query and document embeddings to Store.
//
// The implementation is deliberately kept outside this package so the store
// does not depend on a particular model runtime.
type Embedder interface {
	EmbedOne(context.Context, string) ([]float32, error)
}

// Config controls a Zvec-backed hybrid search store.
type Config struct {
	CollectionPath  string
	Embedder        Embedder
	JiebaDictPath   string
	FTSTokenizer    string
	RRFRankConstant int
}

type collectionMetadata struct {
	SchemaVersion      int    `json:"schema_version"`
	EmbeddingModel     string `json:"embedding_model"`
	EmbeddingDimension int    `json:"embedding_dimension"`
	EmbeddingMetric    string `json:"embedding_metric"`
	FTSTokenizer       string `json:"fts_tokenizer"`
}

// Store implements the server's HybridSearcher contract in-process.
type Store struct {
	mu              sync.Mutex
	collection      *zvec.Collection
	embedder        Embedder
	rrfRankConstant int
	ownsLibrary     bool
	closed          bool
}

// NewStore initializes Zvec and opens or creates the configured collection.
func NewStore(ctx context.Context, config Config) (*Store, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	if strings.TrimSpace(config.CollectionPath) == "" {
		return nil, errors.New("zvec collection path is required")
	}
	if config.Embedder == nil {
		return nil, errors.New("zvec embedder is required")
	}
	rrfRankConstant := config.RRFRankConstant
	if rrfRankConstant == 0 {
		rrfRankConstant = 60
	}
	if rrfRankConstant < 1 {
		return nil, errors.New("zvec RRF rank constant must be positive")
	}
	tokenizer := strings.TrimSpace(config.FTSTokenizer)
	if tokenizer == "" {
		tokenizer = defaultTokenizer
	}

	ownsLibrary := false
	if !zvec.IsInitialized() {
		zvecConfig, err := zvecConfigFor(tokenizer, config.JiebaDictPath)
		if err != nil {
			return nil, err
		}
		if zvecConfig != nil {
			defer zvecConfig.Destroy()
		}
		if err := zvec.Initialize(zvecConfig); err != nil {
			return nil, fmt.Errorf("initialize zvec: %w", err)
		}
		ownsLibrary = true
	}

	collection, err := openCollection(config.CollectionPath, collectionMetadataFor(tokenizer))
	if err != nil {
		shutdownZvec(ownsLibrary)
		return nil, err
	}

	if err := validateCollectionSchema(collection); err != nil {
		_ = collection.Close()
		shutdownZvec(ownsLibrary)
		return nil, fmt.Errorf("validate zvec collection: %w", err)
	}

	return &Store{
		collection:      collection,
		embedder:        config.Embedder,
		rrfRankConstant: rrfRankConstant,
		ownsLibrary:     ownsLibrary,
	}, nil
}

func zvecConfigFor(tokenizer, configuredPath string) (*zvec.ConfigData, error) {
	if tokenizer != "jieba" {
		return nil, nil
	}
	dictPath := strings.TrimSpace(configuredPath)
	if dictPath == "" {
		dictPath = strings.TrimSpace(os.Getenv("ZVEC_JIEBA_DICT_DIR"))
	}
	if dictPath == "" {
		return nil, nil
	}
	config := zvec.NewConfigData()
	if config == nil {
		return nil, errors.New("create zvec configuration")
	}
	if err := config.SetJiebaDictDir(dictPath); err != nil {
		config.Destroy()
		return nil, fmt.Errorf("set zvec jieba dictionary directory: %w", err)
	}
	return config, nil
}

// Ping checks that the store is open and its Zvec handle is initialized.
func (s *Store) Ping(ctx context.Context) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.collection == nil || !zvec.IsInitialized() {
		return errors.New("zvec store is unavailable")
	}
	return nil
}

// Upsert indexes entries that are not already present in the collection.
func (s *Store) Upsert(ctx context.Context, entries []store.IndexEntry) error {
	if len(entries) == 0 {
		return nil
	}
	if err := contextError(ctx); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkOpen(); err != nil {
		return err
	}

	ids := make([]string, 0, len(entries))
	uniqueEntries := make([]store.IndexEntry, 0, len(entries))
	seen := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		if strings.TrimSpace(entry.ID) == "" {
			return errors.New("zvec index entry ID is required")
		}
		if entry.PlainText == "" {
			return fmt.Errorf("zvec index entry %q has empty text", entry.ID)
		}
		if _, ok := seen[entry.ID]; ok {
			continue
		}
		seen[entry.ID] = struct{}{}
		ids = append(ids, entry.ID)
		uniqueEntries = append(uniqueEntries, entry)
	}

	existing, err := s.collection.Fetch(ids, nil)
	if err != nil {
		return fmt.Errorf("fetch existing zvec entries: %w", err)
	}
	existingIDs := make(map[string]struct{}, len(existing))
	for _, doc := range existing {
		if doc != nil {
			existingIDs[doc.GetPK()] = struct{}{}
		}
	}
	zvec.FreeDocs(existing)

	docs := make([]*zvec.Doc, 0, len(uniqueEntries)-len(existingIDs))
	defer zvec.FreeDocs(docs)
	for _, entry := range uniqueEntries {
		if _, ok := existingIDs[entry.ID]; ok {
			continue
		}
		vector, err := s.embedder.EmbedOne(ctx, entry.PlainText)
		if err != nil {
			return fmt.Errorf("embed zvec entry %q: %w", entry.ID, err)
		}
		if err := validateEmbedding(vector); err != nil {
			return fmt.Errorf("embed zvec entry %q: %w", entry.ID, err)
		}

		doc := zvec.NewDoc()
		if doc == nil {
			return errors.New("create zvec document")
		}
		doc.SetPK(entry.ID)
		if err := doc.AddStringField(textFieldName, entry.PlainText); err != nil {
			doc.Destroy()
			return fmt.Errorf("set zvec entry text %q: %w", entry.ID, err)
		}
		if err := doc.AddVectorFP32Field(vectorFieldName, vector); err != nil {
			doc.Destroy()
			return fmt.Errorf("set zvec entry vector %q: %w", entry.ID, err)
		}
		docs = append(docs, doc)
	}
	if len(docs) == 0 {
		return nil
	}

	result, err := s.collection.Upsert(docs)
	if err != nil {
		return fmt.Errorf("upsert zvec entries: %w", err)
	}
	if result == nil {
		return errors.New("upsert zvec entries returned no result")
	}
	if result.ErrorCount > 0 {
		return fmt.Errorf("upsert zvec entries failed for %d documents", result.ErrorCount)
	}
	if err := s.collection.Flush(); err != nil {
		return fmt.Errorf("flush zvec entries: %w", err)
	}
	return contextError(ctx)
}

// Search performs vector and FTS retrieval and fuses both ranks with RRF.
func (s *Store) Search(ctx context.Context, query string, limit int) ([]store.RankedEntry, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	if strings.TrimSpace(query) == "" {
		return nil, errors.New("zvec search query is required")
	}
	if limit < 1 {
		return nil, errors.New("zvec search limit must be positive")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkOpen(); err != nil {
		return nil, err
	}

	vector, err := s.embedder.EmbedOne(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("embed zvec query: %w", err)
	}
	if err := validateEmbedding(vector); err != nil {
		return nil, fmt.Errorf("embed zvec query: %w", err)
	}
	if err := contextError(ctx); err != nil {
		return nil, err
	}

	candidateCount := limit
	if candidateCount < 10 {
		candidateCount = 10
	}
	multiQuery := zvec.NewMultiQuery()
	if multiQuery == nil {
		return nil, errors.New("create zvec multi-query")
	}
	defer multiQuery.Destroy()
	if err := multiQuery.SetTopK(limit); err != nil {
		return nil, fmt.Errorf("set zvec result limit: %w", err)
	}
	if err := multiQuery.SetRerankRRF(s.rrfRankConstant); err != nil {
		return nil, fmt.Errorf("set zvec RRF rank constant: %w", err)
	}

	textQuery := zvec.NewSubQuery()
	if textQuery == nil {
		return nil, errors.New("create zvec FTS query")
	}
	defer textQuery.Destroy()
	if err := textQuery.SetFieldName(textFieldName); err != nil {
		return nil, fmt.Errorf("set zvec FTS field: %w", err)
	}
	if err := textQuery.SetNumCandidates(candidateCount); err != nil {
		return nil, fmt.Errorf("set zvec FTS candidates: %w", err)
	}
	fts := zvec.NewFTS()
	if fts == nil {
		return nil, errors.New("create zvec FTS payload")
	}
	defer fts.Destroy()
	if err := fts.SetMatchString(query); err != nil {
		return nil, fmt.Errorf("set zvec FTS query: %w", err)
	}
	if err := textQuery.SetFTS(fts); err != nil {
		return nil, fmt.Errorf("attach zvec FTS query: %w", err)
	}
	if err := multiQuery.AddSubQuery(textQuery); err != nil {
		return nil, fmt.Errorf("add zvec FTS query: %w", err)
	}

	vectorQuery := zvec.NewSubQuery()
	if vectorQuery == nil {
		return nil, errors.New("create zvec vector query")
	}
	defer vectorQuery.Destroy()
	if err := vectorQuery.SetFieldName(vectorFieldName); err != nil {
		return nil, fmt.Errorf("set zvec vector field: %w", err)
	}
	if err := vectorQuery.SetNumCandidates(candidateCount); err != nil {
		return nil, fmt.Errorf("set zvec vector candidates: %w", err)
	}
	if err := vectorQuery.SetQueryVector(vector); err != nil {
		return nil, fmt.Errorf("set zvec query vector: %w", err)
	}
	if err := multiQuery.AddSubQuery(vectorQuery); err != nil {
		return nil, fmt.Errorf("add zvec vector query: %w", err)
	}

	docs, err := s.collection.MultiQuery(multiQuery)
	if err != nil {
		return nil, fmt.Errorf("run zvec hybrid query: %w", err)
	}
	defer zvec.FreeDocs(docs)
	results := make([]store.RankedEntry, 0, len(docs))
	for _, doc := range docs {
		if doc == nil || doc.GetPK() == "" {
			continue
		}
		results = append(results, store.RankedEntry{ID: doc.GetPK(), Score: doc.GetScore()})
	}
	return results, contextError(ctx)
}

// Close closes the collection and releases the process-wide Zvec library.
func (s *Store) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	var closeErr error
	if s.collection != nil {
		closeErr = s.collection.Close()
		s.collection = nil
	}
	if s.ownsLibrary {
		closeErr = errors.Join(closeErr, zvec.Shutdown())
		s.ownsLibrary = false
	}
	return closeErr
}

func (s *Store) checkOpen() error {
	if s.closed || s.collection == nil || s.embedder == nil {
		return errors.New("zvec store is closed")
	}
	if !zvec.IsInitialized() {
		return errors.New("zvec store is unavailable")
	}
	return nil
}

func openCollection(path string, metadata collectionMetadata) (*zvec.Collection, error) {
	if _, err := os.Stat(path); err == nil {
		if err := validateMetadata(metadataPath(path), metadata); err != nil {
			return nil, err
		}
		collection, err := zvec.Open(path, nil)
		if err != nil {
			return nil, fmt.Errorf("open zvec collection: %w", err)
		}
		return collection, nil
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("stat zvec collection: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create zvec collection directory: %w", err)
	}
	collection, err := createCollection(path, metadata.FTSTokenizer)
	if err == nil {
		if err := writeMetadata(metadataPath(path), metadata); err != nil {
			_ = collection.Close()
			return nil, fmt.Errorf("write zvec metadata: %w", err)
		}
		return collection, nil
	}
	if !zvec.IsAlreadyExists(err) {
		return nil, fmt.Errorf("create zvec collection: %w", err)
	}
	if err := validateMetadata(metadataPath(path), metadata); err != nil {
		return nil, err
	}
	collection, err = zvec.Open(path, nil)
	if err != nil {
		return nil, fmt.Errorf("open zvec collection after concurrent create: %w", err)
	}
	return collection, nil
}

func createCollection(path, tokenizer string) (*zvec.Collection, error) {
	schema := zvec.NewCollectionSchema(collectionName)
	if schema == nil {
		return nil, errors.New("create zvec collection schema")
	}
	defer schema.Destroy()

	ftsParams, err := zvec.NewFTSIndexParams(tokenizer, []string{"lowercase"}, "")
	if err != nil {
		return nil, fmt.Errorf("create zvec FTS index params: %w", err)
	}
	defer ftsParams.Destroy()
	textField := zvec.NewFieldSchema(textFieldName, zvec.DataTypeString, false, 0)
	if textField == nil {
		return nil, errors.New("create zvec text field")
	}
	defer textField.Destroy()
	if err := textField.SetIndexParams(ftsParams); err != nil {
		return nil, fmt.Errorf("set zvec FTS index: %w", err)
	}
	if err := schema.AddField(textField); err != nil {
		return nil, fmt.Errorf("add zvec text field: %w", err)
	}

	hnswParams, err := zvec.NewHNSWIndexParams(zvec.MetricTypeCosine, 16, 200)
	if err != nil {
		return nil, fmt.Errorf("create zvec HNSW index params: %w", err)
	}
	defer hnswParams.Destroy()
	embeddingField := zvec.NewFieldSchema(vectorFieldName, zvec.DataTypeVectorFP32, false, EmbeddingDimension)
	if embeddingField == nil {
		return nil, errors.New("create zvec embedding field")
	}
	defer embeddingField.Destroy()
	if err := embeddingField.SetIndexParams(hnswParams); err != nil {
		return nil, fmt.Errorf("set zvec HNSW index: %w", err)
	}
	if err := schema.AddField(embeddingField); err != nil {
		return nil, fmt.Errorf("add zvec embedding field: %w", err)
	}

	collection, err := zvec.CreateAndOpen(path, schema, nil)
	if err != nil {
		return nil, err
	}
	return collection, nil
}

func validateCollectionSchema(collection *zvec.Collection) error {
	schema, err := collection.GetSchema()
	if err != nil {
		return err
	}
	defer schema.Destroy()

	textField := schema.GetField(textFieldName)
	if textField == nil {
		return errors.New("collection has no plain_text field")
	}
	validText := textField.GetDataType() == zvec.DataTypeString && textField.GetIndexType() == zvec.IndexTypeFTS
	textField.Destroy()
	if !validText {
		return errors.New("collection plain_text field has incompatible schema")
	}

	embeddingField := schema.GetField(vectorFieldName)
	if embeddingField == nil {
		return errors.New("collection has no embedding field")
	}
	defer embeddingField.Destroy()
	if embeddingField.GetDataType() != zvec.DataTypeVectorFP32 ||
		embeddingField.GetDimension() != EmbeddingDimension ||
		embeddingField.GetIndexType() != zvec.IndexTypeHNSW {
		return errors.New("collection embedding field has incompatible schema")
	}
	return nil
}

func collectionMetadataFor(tokenizer string) collectionMetadata {
	return collectionMetadata{
		SchemaVersion:      schemaVersion,
		EmbeddingModel:     embeddingModel,
		EmbeddingDimension: EmbeddingDimension,
		EmbeddingMetric:    embeddingMetric,
		FTSTokenizer:       tokenizer,
	}
}

func metadataPath(collectionPath string) string {
	return collectionPath + ".maccy.json"
}

func validateMetadata(path string, expected collectionMetadata) error {
	actual, err := readMetadata(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("zvec metadata is missing at %s; rebuild the collection", path)
		}
		return fmt.Errorf("read zvec metadata %s: %w", path, err)
	}
	if !reflect.DeepEqual(actual, expected) {
		return fmt.Errorf("zvec collection configuration changed at %s; rebuild the collection before startup", path)
	}
	return nil
}

func readMetadata(path string) (collectionMetadata, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return collectionMetadata{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var metadata collectionMetadata
	if err := decoder.Decode(&metadata); err != nil {
		return collectionMetadata{}, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return collectionMetadata{}, errors.New("metadata must contain one JSON object")
		}
		return collectionMetadata{}, err
	}
	return metadata, nil
}

func writeMetadata(path string, metadata collectionMetadata) error {
	data, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	temporary := fmt.Sprintf("%s.tmp-%d", path, os.Getpid())
	if err := os.WriteFile(temporary, data, 0o644); err != nil {
		return err
	}
	defer os.Remove(temporary)
	if err := os.Rename(temporary, path); err != nil {
		return err
	}
	return nil
}

func validateEmbedding(vector []float32) error {
	if len(vector) != EmbeddingDimension {
		return fmt.Errorf("embedding dimension must be %d, got %d", EmbeddingDimension, len(vector))
	}
	return nil
}

func contextError(ctx context.Context) error {
	if ctx == nil {
		return errors.New("context is nil")
	}
	return ctx.Err()
}

func shutdownZvec(ownsLibrary bool) {
	if ownsLibrary {
		_ = zvec.Shutdown()
	}
}
