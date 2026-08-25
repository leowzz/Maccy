package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"maccy-server/internal/store"
)

type fakeRepository struct {
	ingestResult store.IngestResult
	ingested     []store.UploadEvent
	tokenName    string
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

func (f *fakeRepository) ListEntries(context.Context, store.ListEntriesParams) ([]store.Entry, error) {
	return nil, nil
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

func TestProtectedEndpointRequiresAuthentication(t *testing.T) {
	handler := testHandler(&fakeRepository{})
	request := httptest.NewRequest(http.MethodGet, "/v1/entries", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", response.Code)
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
	want := store.EntryCursor{LastCopiedAt: time.Now().UTC().Round(0), ID: "entry-1"}
	encoded := encodeCursor(want)
	got, err := decodeCursor(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != want.ID || !got.LastCopiedAt.Equal(want.LastCopiedAt) {
		t.Fatalf("cursor mismatch: got %#v want %#v", got, want)
	}
}

func testHandler(repository store.Repository) http.Handler {
	return New(Config{
		AccountID: "test-account",
		Auth:      map[string]string{"test-mac-token": "test-token"},
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
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
