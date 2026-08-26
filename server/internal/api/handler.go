package api

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"maccy-server/internal/store"
)

const (
	maxRequestBytes = 20 << 20
	maxTextBytes    = 1 << 20
	maxBatchSize    = 100
	defaultPageSize = 50
	maxPageSize     = 200
)

type Config struct {
	AccountID string
	Auth      map[string]string
	Logger    *slog.Logger
}

type authToken struct {
	name       string
	secretHash [sha256.Size]byte
}

type API struct {
	accountID string
	auth      []authToken
	logger    *slog.Logger
	store     store.Repository
}

type ingestRequest struct {
	DeviceID string        `json:"device_id"`
	Events   []ingestEvent `json:"events"`
}

type ingestEvent struct {
	ClientEventID  string    `json:"client_event_id"`
	CopiedAt       time.Time `json:"copied_at"`
	SourceBundleID string    `json:"source_bundle_id,omitempty"`
	PlainText      string    `json:"plain_text"`
	ContentSHA256  string    `json:"content_sha256"`
}

type ingestResponse struct {
	Accepted      int   `json:"accepted"`
	Duplicates    int   `json:"duplicates"`
	LastServerSeq int64 `json:"last_server_seq"`
}

type entriesResponse struct {
	Entries    []store.Entry `json:"entries"`
	NextCursor string        `json:"next_cursor,omitempty"`
}

type eventsResponse struct {
	Events        []store.Event `json:"events"`
	LastServerSeq int64         `json:"last_server_seq"`
}

type errorResponse struct {
	Error string `json:"error"`
}

func New(cfg Config, repository store.Repository) http.Handler {
	auth := make([]authToken, 0, len(cfg.Auth))
	for name, secret := range cfg.Auth {
		auth = append(auth, authToken{name: name, secretHash: sha256.Sum256([]byte(secret))})
	}
	api := &API{
		accountID: cfg.AccountID,
		auth:      auth,
		logger:    cfg.Logger,
		store:     repository,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", api.health)
	mux.Handle("POST /v1/sync/events:batch", api.authenticate(http.HandlerFunc(api.ingest)))
	mux.Handle("GET /v1/entries", api.authenticate(http.HandlerFunc(api.listEntries)))
	mux.Handle("GET /v1/events", api.authenticate(http.HandlerFunc(api.listEvents)))
	return api.logRequests(api.recoverPanic(mux))
}

func (a *API) health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := a.store.Ping(ctx); err != nil {
		a.logger.Error("health check failed", "error", err)
		writeError(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (a *API) ingest(w http.ResponseWriter, r *http.Request) {
	var request ingestRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validateIngestRequest(request); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}

	events := make([]store.UploadEvent, len(request.Events))
	for index, event := range request.Events {
		events[index] = store.UploadEvent{
			ClientEventID:  event.ClientEventID,
			CopiedAt:       event.CopiedAt,
			SourceBundleID: event.SourceBundleID,
			PlainText:      event.PlainText,
			ContentSHA256:  strings.ToLower(event.ContentSHA256),
		}
	}

	tokenName, ok := r.Context().Value(tokenNameContextKey{}).(string)
	if !ok {
		writeError(w, http.StatusInternalServerError, "authentication source unavailable")
		return
	}
	ingestStarted := time.Now()
	result, err := a.store.Ingest(r.Context(), a.accountID, tokenName, request.DeviceID, events)
	ingestDuration := time.Since(ingestStarted)
	if err != nil {
		a.logger.Error(
			"ingest failed",
			"error", err,
			"event_count", len(events),
			"device_id", request.DeviceID,
			"token_name", tokenName,
			"duration_us", ingestDuration.Microseconds(),
			"duration_ms", durationMilliseconds(ingestDuration),
		)
		writeError(w, http.StatusInternalServerError, "ingest failed")
		return
	}
	a.logger.Info(
		"ingest completed",
		"event_count", len(events),
		"accepted", result.Accepted,
		"duplicates", result.Duplicates,
		"last_server_seq", result.LastServerSeq,
		"device_id", request.DeviceID,
		"token_name", tokenName,
		"duration_us", ingestDuration.Microseconds(),
		"duration_ms", durationMilliseconds(ingestDuration),
	)
	writeJSON(w, http.StatusOK, ingestResponse{
		Accepted:      result.Accepted,
		Duplicates:    result.Duplicates,
		LastServerSeq: result.LastServerSeq,
	})
}

func (a *API) listEntries(w http.ResponseWriter, r *http.Request) {
	limit, err := pageSize(r.URL.Query().Get("limit"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	query := r.URL.Query().Get("q")
	if utf8.RuneCountInString(query) > 256 {
		writeError(w, http.StatusBadRequest, "q must not exceed 256 characters")
		return
	}
	mode, err := searchMode(r.URL.Query().Get("mode"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if mode == store.EntrySearchFuzzy && utf8.RuneCountInString(query) < 3 {
		writeError(w, http.StatusBadRequest, "fuzzy search requires q to contain at least 3 characters")
		return
	}
	cursor, err := decodeCursor(r.URL.Query().Get("cursor"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid cursor")
		return
	}
	if cursor != nil && cursor.Mode != "" && cursor.Mode != mode {
		writeError(w, http.StatusBadRequest, "cursor does not match search mode")
		return
	}

	entries, err := a.store.ListEntries(r.Context(), store.ListEntriesParams{
		AccountID: a.accountID,
		Query:     query,
		Mode:      mode,
		Cursor:    cursor,
		Limit:     limit + 1,
	})
	if err != nil {
		a.logger.Error("list entries failed", "error", err)
		writeError(w, http.StatusInternalServerError, "list entries failed")
		return
	}

	response := entriesResponse{Entries: entries}
	if len(entries) > limit {
		last := entries[limit-1]
		response.Entries = entries[:limit]
		response.NextCursor = encodeCursor(store.EntryCursor{
			LastCopiedAt: last.LastCopiedAt,
			ID:           last.ID,
			Mode:         mode,
			Score:        last.Score,
		})
	}
	writeJSON(w, http.StatusOK, response)
}

func (a *API) listEvents(w http.ResponseWriter, r *http.Request) {
	limit, err := pageSize(r.URL.Query().Get("limit"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	afterSeq := int64(0)
	if raw := r.URL.Query().Get("after_seq"); raw != "" {
		afterSeq, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || afterSeq < 0 {
			writeError(w, http.StatusBadRequest, "after_seq must be a non-negative integer")
			return
		}
	}

	events, err := a.store.ListEvents(r.Context(), a.accountID, afterSeq, limit)
	if err != nil {
		a.logger.Error("list events failed", "error", err)
		writeError(w, http.StatusInternalServerError, "list events failed")
		return
	}
	response := eventsResponse{Events: events, LastServerSeq: afterSeq}
	if len(events) > 0 {
		response.LastServerSeq = events[len(events)-1].ServerSeq
	}
	writeJSON(w, http.StatusOK, response)
}

func (a *API) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := r.Header.Get("Authorization")
		if !strings.HasPrefix(header, "Bearer ") {
			w.Header().Set("WWW-Authenticate", "Bearer")
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}

		actual := sha256.Sum256([]byte(strings.TrimPrefix(header, "Bearer ")))
		tokenName := ""
		for _, token := range a.auth {
			if subtle.ConstantTimeCompare(actual[:], token.secretHash[:]) == 1 {
				tokenName = token.name
			}
		}
		if tokenName == "" {
			w.Header().Set("WWW-Authenticate", "Bearer")
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}

		ctx := context.WithValue(r.Context(), tokenNameContextKey{}, tokenName)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

type tokenNameContextKey struct{}

func (a *API) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		recorder := &responseRecorder{ResponseWriter: w}
		next.ServeHTTP(recorder, r)
		duration := time.Since(started)
		a.logger.Info(
			"request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", recorder.statusCode(),
			"response_bytes", recorder.bytes,
			"duration_us", duration.Microseconds(),
			"duration_ms", durationMilliseconds(duration),
		)
	})
}

type responseRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (r *responseRecorder) WriteHeader(status int) {
	if r.status != 0 {
		return
	}
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *responseRecorder) Write(data []byte) (int, error) {
	if r.status == 0 {
		r.WriteHeader(http.StatusOK)
	}
	count, err := r.ResponseWriter.Write(data)
	r.bytes += count
	return count, err
}

func (r *responseRecorder) statusCode() int {
	if r.status == 0 {
		return http.StatusOK
	}
	return r.status
}

func durationMilliseconds(duration time.Duration) float64 {
	return float64(duration.Microseconds()) / 1000
}

func (a *API) recoverPanic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				a.logger.Error("request panic", "error", recovered)
				writeError(w, http.StatusInternalServerError, "internal server error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func validateIngestRequest(request ingestRequest) error {
	if request.DeviceID == "" || len(request.DeviceID) > 128 {
		return errors.New("device_id must contain 1 to 128 bytes")
	}
	if len(request.Events) == 0 || len(request.Events) > maxBatchSize {
		return fmt.Errorf("events must contain 1 to %d items", maxBatchSize)
	}

	seen := make(map[string]struct{}, len(request.Events))
	for index, event := range request.Events {
		prefix := fmt.Sprintf("events[%d]", index)
		if event.ClientEventID == "" || len(event.ClientEventID) > 128 {
			return fmt.Errorf("%s.client_event_id must contain 1 to 128 bytes", prefix)
		}
		if _, exists := seen[event.ClientEventID]; exists {
			return fmt.Errorf("%s.client_event_id is duplicated in this batch", prefix)
		}
		seen[event.ClientEventID] = struct{}{}
		if event.CopiedAt.IsZero() {
			return fmt.Errorf("%s.copied_at is required", prefix)
		}
		if len(event.SourceBundleID) > 512 {
			return fmt.Errorf("%s.source_bundle_id must not exceed 512 bytes", prefix)
		}
		if event.PlainText == "" {
			return fmt.Errorf("%s.plain_text must not be empty", prefix)
		}
		if !utf8.ValidString(event.PlainText) || len([]byte(event.PlainText)) > maxTextBytes {
			return fmt.Errorf("%s.plain_text must be valid UTF-8 and no larger than %d bytes", prefix, maxTextBytes)
		}
		hashBytes, err := hex.DecodeString(event.ContentSHA256)
		if err != nil || len(hashBytes) != sha256.Size {
			return fmt.Errorf("%s.content_sha256 must be a 64-character hex SHA-256", prefix)
		}
		actual := sha256.Sum256([]byte(event.PlainText))
		if subtle.ConstantTimeCompare(actual[:], hashBytes) != 1 {
			return fmt.Errorf("%s.content_sha256 does not match plain_text", prefix)
		}
	}
	return nil
}

func pageSize(raw string) (int, error) {
	if raw == "" {
		return defaultPageSize, nil
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit < 1 || limit > maxPageSize {
		return 0, fmt.Errorf("limit must be between 1 and %d", maxPageSize)
	}
	return limit, nil
}

func encodeCursor(cursor store.EntryCursor) string {
	mode := cursor.Mode
	if mode == "" {
		mode = store.EntrySearchContains
	}
	payload := "v2\n" + string(mode) + "\n" + strconv.FormatFloat(float64(cursor.Score), 'g', -1, 32) + "\n" +
		cursor.LastCopiedAt.UTC().Format(time.RFC3339Nano) + "\n" + cursor.ID
	return base64.RawURLEncoding.EncodeToString([]byte(payload))
}

func decodeCursor(raw string) (*store.EntryCursor, error) {
	if raw == "" {
		return nil, nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, err
	}
	parts := strings.Split(string(decoded), "\n")
	if len(parts) == 2 {
		timestamp, err := time.Parse(time.RFC3339Nano, parts[0])
		if err != nil || parts[1] == "" {
			return nil, errors.New("malformed cursor")
		}
		return &store.EntryCursor{LastCopiedAt: timestamp, ID: parts[1]}, nil
	}
	if len(parts) != 5 || parts[0] != "v2" || parts[1] == "" || parts[4] == "" {
		return nil, errors.New("malformed cursor")
	}
	mode := store.EntrySearchMode(parts[1])
	if mode != store.EntrySearchContains && mode != store.EntrySearchFuzzy {
		return nil, errors.New("malformed cursor")
	}
	score, err := strconv.ParseFloat(parts[2], 32)
	if err != nil {
		return nil, errors.New("malformed cursor")
	}
	timestamp, err := time.Parse(time.RFC3339Nano, parts[3])
	if err != nil {
		return nil, err
	}
	return &store.EntryCursor{
		LastCopiedAt: timestamp,
		ID:           parts[4],
		Mode:         mode,
		Score:        float32(score),
	}, nil
}

func searchMode(raw string) (store.EntrySearchMode, error) {
	switch raw {
	case "", string(store.EntrySearchContains):
		return store.EntrySearchContains, nil
	case string(store.EntrySearchFuzzy):
		return store.EntrySearchFuzzy, nil
	default:
		return "", errors.New("mode must be contains or fuzzy")
	}
}

func decodeJSON(w http.ResponseWriter, r *http.Request, destination any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain one JSON object")
	}
	return nil
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, errorResponse{Error: message})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
