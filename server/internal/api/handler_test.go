package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"maccy-server/internal/store"
)

type fakeRepository struct {
	ingestResult store.IngestResult
	ingested     []store.UploadEvent
	tokenName    string
	entries      []store.Entry
	listParams   *store.ListEntriesParams
	rankedParams []store.RankedEntry
}

func (f *fakeRepository) Ping(context.Context) error { return nil }

func (f *fakeRepository) Ingest(
	_ context.Context,
	_, tokenName, _ string,
	events []store.UploadEvent,
) (store.IngestResult, error) {
	f.tokenName = tokenName
	f.ingested = events
	return f.ingestResult, nil
}

func (f *fakeRepository) ListEntries(_ context.Context, params store.ListEntriesParams) ([]store.Entry, error) {
	f.listParams = &params
	return f.entries, nil
}

func (f *fakeRepository) ListIndexEntries(context.Context, string, string, int) ([]store.IndexEntry, error) {
	return nil, nil
}

func (f *fakeRepository) EntriesByRank(_ context.Context, _ string, ranked []store.RankedEntry) ([]store.Entry, error) {
	f.rankedParams = ranked
	entries := make([]store.Entry, len(ranked))
	for index, result := range ranked {
		entries[index] = store.Entry{ID: result.ID, Score: result.Score}
	}
	return entries, nil
}

func (f *fakeRepository) ListEvents(context.Context, string, int64, int) ([]store.Event, error) {
	return nil, nil
}

func TestHealthDoesNotRequireAuthentication(t *testing.T) {
	handler := testHandler(&fakeRepository{})
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", response.Code)
	}
}

func TestHealthTreatsTypedNilHybridAsDisabled(t *testing.T) {
	var searcher *fakeHybridSearcher
	handler := New(Config{
		AccountID: "test-account",
		Auth:      map[string]string{"test-mac-token": "test-token"},
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		Hybrid:    searcher,
	}, &fakeRepository{})
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", response.Code, response.Body.String())
	}
}

func TestProtectedEndpointRequiresAuthentication(t *testing.T) {
	handler := testHandler(&fakeRepository{})
	request := httptest.NewRequest(http.MethodGet, "/v1/entries", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", response.Code)
	}
}

func TestListEntriesSupportsFuzzyMode(t *testing.T) {
	repository := &fakeRepository{entries: []store.Entry{{ID: "entry-1", Score: 0.75}}}
	handler := testHandler(repository)
	request := httptest.NewRequest(http.MethodGet, "/v1/entries?q=helo&mode=fuzzy&limit=10", nil)
	request.Header.Set("Authorization", "Bearer test-token")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", response.Code, response.Body.String())
	}
	if repository.listParams == nil || repository.listParams.Mode != store.EntrySearchFuzzy {
		t.Fatalf("unexpected list params: %#v", repository.listParams)
	}
	if repository.listParams.Query != "helo" {
		t.Fatalf("unexpected query: %#v", repository.listParams)
	}
	var payload entriesResponse
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Entries) != 1 || payload.Entries[0].Score != 0.75 {
		t.Fatalf("unexpected fuzzy response: %#v", payload)
	}
}

func TestListEntriesRejectsShortFuzzyQuery(t *testing.T) {
	handler := testHandler(&fakeRepository{})
	request := httptest.NewRequest(http.MethodGet, "/v1/entries?q=ab&mode=fuzzy", nil)
	request.Header.Set("Authorization", "Bearer test-token")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", response.Code, response.Body.String())
	}
}

func TestListEntriesRejectsUnknownSearchMode(t *testing.T) {
	handler := testHandler(&fakeRepository{})
	request := httptest.NewRequest(http.MethodGet, "/v1/entries?q=hello&mode=typo", nil)
	request.Header.Set("Authorization", "Bearer test-token")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", response.Code, response.Body.String())
	}
}

type fakeHybridSearcher struct {
	results       []store.RankedEntry
	searchQuery   string
	searchLimit   int
	upsertEntries []store.IndexEntry
	pingError     error
	upsertError   error
	searchError   error
}

func (f *fakeHybridSearcher) Ping(context.Context) error { return f.pingError }

func (f *fakeHybridSearcher) Upsert(_ context.Context, entries []store.IndexEntry) error {
	f.upsertEntries = entries
	return f.upsertError
}

func (f *fakeHybridSearcher) Search(_ context.Context, query string, limit int) ([]store.RankedEntry, error) {
	f.searchQuery = query
	f.searchLimit = limit
	return f.results, f.searchError
}

func TestListEntriesSupportsHybridMode(t *testing.T) {
	repository := &fakeRepository{}
	searcher := &fakeHybridSearcher{results: []store.RankedEntry{
		{ID: "entry-2", Score: 0.8},
		{ID: "entry-1", Score: 0.7},
		{ID: "entry-3", Score: 0.6},
	}}
	handler := testHybridHandler(repository, searcher)
	request := httptest.NewRequest(http.MethodGet, "/v1/entries?q=semantic&mode=hybrid&limit=2", nil)
	request.Header.Set("Authorization", "Bearer test-token")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", response.Code, response.Body.String())
	}
	if searcher.searchQuery != "semantic" || searcher.searchLimit != 3 {
		t.Fatalf("unexpected hybrid query: query=%q limit=%d", searcher.searchQuery, searcher.searchLimit)
	}
	if len(repository.rankedParams) != 2 || repository.rankedParams[0].ID != "entry-2" {
		t.Fatalf("unexpected ranked hydration: %#v", repository.rankedParams)
	}
	var payload entriesResponse
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Entries) != 2 || payload.Entries[0].Score != 0.8 || payload.NextCursor == "" {
		t.Fatalf("unexpected hybrid response: %#v", payload)
	}
	cursor, err := decodeCursor(payload.NextCursor)
	if err != nil || cursor.Mode != store.EntrySearchHybrid || cursor.Offset != 2 {
		t.Fatalf("unexpected hybrid cursor: cursor=%#v err=%v", cursor, err)
	}
}

func TestListEntriesRejectsEmptyHybridQuery(t *testing.T) {
	handler := testHybridHandler(&fakeRepository{}, &fakeHybridSearcher{})
	request := httptest.NewRequest(http.MethodGet, "/v1/entries?mode=hybrid", nil)
	request.Header.Set("Authorization", "Bearer test-token")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", response.Code, response.Body.String())
	}
}

func TestListEntriesReportsUnavailableHybridSearch(t *testing.T) {
	handler := testHandler(&fakeRepository{})
	request := httptest.NewRequest(http.MethodGet, "/v1/entries?q=semantic&mode=hybrid", nil)
	request.Header.Set("Authorization", "Bearer test-token")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", response.Code, response.Body.String())
	}
}

func TestRequestLogIncludesPreciseDurationAndResponseMetadata(t *testing.T) {
	var logs bytes.Buffer
	handler := New(Config{
		AccountID: "test-account",
		Auth:      map[string]string{"test-mac-token": "test-token"},
		Logger:    slog.New(slog.NewJSONHandler(&logs, nil)),
	}, &fakeRepository{})

	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	var record map[string]any
	if err := json.Unmarshal(logs.Bytes(), &record); err != nil {
		t.Fatalf("decode request log: %v; logs=%s", err, logs.String())
	}
	if record["msg"] != "request" {
		t.Fatalf("expected request log, got %#v", record["msg"])
	}
	if record["status"] != float64(http.StatusOK) {
		t.Fatalf("expected status 200, got %#v", record["status"])
	}
	if record["response_bytes"] != float64(len(response.Body.Bytes())) {
		t.Fatalf("unexpected response bytes: %#v", record["response_bytes"])
	}
	if _, ok := record["duration_us"]; !ok {
		t.Fatal("request log is missing duration_us")
	}
	if _, ok := record["duration_ms"]; !ok {
		t.Fatal("request log is missing duration_ms")
	}
}

func TestIngestValidatesHashAndPassesEventToStore(t *testing.T) {
	repository := &fakeRepository{ingestResult: store.IngestResult{Accepted: 1, LastServerSeq: 7}}
	handler := testHandler(repository)
	body := ingestBody(t, "hello")
	request := httptest.NewRequest(http.MethodPost, "/v1/sync/events:batch", body)
	request.Header.Set("Authorization", "Bearer test-token")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", response.Code, response.Body.String())
	}
	if len(repository.ingested) != 1 || repository.ingested[0].PlainText != "hello" {
		t.Fatalf("unexpected ingested events: %#v", repository.ingested)
	}
	if repository.tokenName != "test-mac-token" {
		t.Fatalf("expected token source test-mac-token, got %q", repository.tokenName)
	}
	var payload ingestResponse
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Accepted != 1 || payload.LastServerSeq != 7 {
		t.Fatalf("unexpected response: %#v", payload)
	}
}

func TestIngestUpdatesHybridIndex(t *testing.T) {
	indexEntry := store.IndexEntry{ID: "entry-1", PlainText: "hello"}
	repository := &fakeRepository{ingestResult: store.IngestResult{
		Accepted: 1, LastServerSeq: 7, IndexEntries: []store.IndexEntry{indexEntry},
	}}
	searcher := &fakeHybridSearcher{}
	handler := testHybridHandler(repository, searcher)
	request := httptest.NewRequest(http.MethodPost, "/v1/sync/events:batch", ingestBody(t, "hello"))
	request.Header.Set("Authorization", "Bearer test-token")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", response.Code, response.Body.String())
	}
	if len(searcher.upsertEntries) != 1 || searcher.upsertEntries[0] != indexEntry {
		t.Fatalf("unexpected indexed entries: %#v", searcher.upsertEntries)
	}
}

func TestIngestReturnsRetryableErrorWhenHybridIndexingFails(t *testing.T) {
	indexEntry := store.IndexEntry{ID: "entry-1", PlainText: "hello"}
	repository := &fakeRepository{ingestResult: store.IngestResult{
		Accepted: 1, LastServerSeq: 7, IndexEntries: []store.IndexEntry{indexEntry},
	}}
	searcher := &fakeHybridSearcher{upsertError: errors.New("index unavailable")}
	handler := testHybridHandler(repository, searcher)
	request := httptest.NewRequest(http.MethodPost, "/v1/sync/events:batch", ingestBody(t, "hello"))
	request.Header.Set("Authorization", "Bearer test-token")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", response.Code, response.Body.String())
	}
	if len(searcher.upsertEntries) != 1 || searcher.upsertEntries[0] != indexEntry {
		t.Fatalf("unexpected indexed entries: %#v", searcher.upsertEntries)
	}
}

func TestIngestLogIncludesOutcomeWithoutClipboardText(t *testing.T) {
	var logs bytes.Buffer
	handler := New(Config{
		AccountID: "test-account",
		Auth:      map[string]string{"test-mac-token": "test-token"},
		Logger:    slog.New(slog.NewJSONHandler(&logs, nil)),
	}, &fakeRepository{ingestResult: store.IngestResult{Accepted: 1, Duplicates: 2, LastServerSeq: 7}})

	request := httptest.NewRequest(http.MethodPost, "/v1/sync/events:batch", ingestBody(t, "super-secret-clipboard"))
	request.Header.Set("Authorization", "Bearer test-token")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", response.Code, response.Body.String())
	}
	if strings.Contains(logs.String(), "super-secret-clipboard") {
		t.Fatalf("clipboard text leaked into logs: %s", logs.String())
	}

	var found bool
	for _, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("decode log: %v; line=%s", err, line)
		}
		if record["msg"] != "ingest completed" {
			continue
		}
		found = true
		if record["event_count"] != float64(1) || record["accepted"] != float64(1) || record["duplicates"] != float64(2) {
			t.Fatalf("unexpected ingest outcome: %#v", record)
		}
		if _, ok := record["duration_us"]; !ok {
			t.Fatal("ingest log is missing duration_us")
		}
		if _, ok := record["duration_ms"]; !ok {
			t.Fatal("ingest log is missing duration_ms")
		}
	}
	if !found {
		t.Fatalf("missing ingest completed log: %s", logs.String())
	}
}

func TestIngestRejectsMismatchedHash(t *testing.T) {
	handler := testHandler(&fakeRepository{})
	payload := map[string]any{
		"device_id": "test-mac",
		"events": []map[string]any{{
			"client_event_id": "event-1",
			"copied_at":       time.Now().UTC(),
			"plain_text":      "hello",
			"content_sha256":  hex.EncodeToString(make([]byte, sha256.Size)),
		}},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/sync/events:batch", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer test-token")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d: %s", response.Code, response.Body.String())
	}
}

func TestCursorRoundTrip(t *testing.T) {
	want := store.EntryCursor{
		LastCopiedAt: time.Now().UTC().Round(0),
		ID:           "entry-1",
		Mode:         store.EntrySearchFuzzy,
		Score:        0.8125,
	}
	encoded := encodeCursor(want)
	got, err := decodeCursor(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != want.ID || got.Mode != want.Mode || got.Score != want.Score || !got.LastCopiedAt.Equal(want.LastCopiedAt) {
		t.Fatalf("cursor mismatch: got %#v want %#v", got, want)
	}
}

func TestLegacyCursorStillDecodes(t *testing.T) {
	legacy := base64.RawURLEncoding.EncodeToString([]byte("2026-08-26T15:04:53Z\nentry-1"))
	got, err := decodeCursor(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "entry-1" || got.Mode != "" {
		t.Fatalf("unexpected legacy cursor: %#v", got)
	}
}

func testHandler(repository store.Repository) http.Handler {
	return New(Config{
		AccountID: "test-account",
		Auth:      map[string]string{"test-mac-token": "test-token"},
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	}, repository)
}

func testHybridHandler(repository store.Repository, searcher HybridSearcher) http.Handler {
	return New(Config{
		AccountID: "test-account",
		Auth:      map[string]string{"test-mac-token": "test-token"},
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		Hybrid:    searcher,
	}, repository)
}

func ingestBody(t *testing.T, text string) io.Reader {
	t.Helper()
	hash := sha256.Sum256([]byte(text))
	payload := map[string]any{
		"device_id": "test-mac",
		"events": []map[string]any{{
			"client_event_id":  "event-1",
			"copied_at":        time.Now().UTC(),
			"source_bundle_id": "com.apple.TextEdit",
			"plain_text":       text,
			"content_sha256":   hex.EncodeToString(hash[:]),
		}},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return bytes.NewReader(body)
}
