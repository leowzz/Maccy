package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"testing"
	"time"
)

func TestPostgresIngestIsIdempotentAndDeduplicatesContent(t *testing.T) {
	databaseURL := os.Getenv("MACCY_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("MACCY_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	database, err := Open(ctx, databaseURL, 4)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := database.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	accountID := "integration-" + time.Now().UTC().Format("20060102150405.000000000")
	defer func() {
		_, _ = database.pool.Exec(ctx, "DELETE FROM clipboard_events WHERE account_id = $1", accountID)
		_, _ = database.pool.Exec(ctx, "DELETE FROM clipboard_entries WHERE account_id = $1", accountID)
	}()

	text := "hello clipboard"
	hash := sha256.Sum256([]byte(text))
	event := UploadEvent{
		ClientEventID: "event-1",
		CopiedAt:      time.Now().UTC(),
		PlainText:     text,
		ContentSHA256: hex.EncodeToString(hash[:]),
	}

	first, err := database.Ingest(ctx, accountID, "macbook-token", "test-mac", []UploadEvent{event})
	if err != nil {
		t.Fatal(err)
	}
	second, err := database.Ingest(ctx, accountID, "macbook-token", "test-mac", []UploadEvent{event})
	if err != nil {
		t.Fatal(err)
	}
	event.ClientEventID = "event-2"
	third, err := database.Ingest(ctx, accountID, "macbook-token", "test-mac", []UploadEvent{event})
	if err != nil {
		t.Fatal(err)
	}

	if first.Accepted != 1 || second.Duplicates != 1 || third.Accepted != 1 {
		t.Fatalf("unexpected ingest results: first=%#v second=%#v third=%#v", first, second, third)
	}
	entries, err := database.ListEntries(ctx, ListEntriesParams{AccountID: accountID, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].OccurrenceCount != 2 {
		t.Fatalf("unexpected entries: %#v", entries)
	}
	entries, err = database.ListEntries(ctx, ListEntriesParams{
		AccountID: accountID,
		Query:     "clipboard",
		Limit:     10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].PlainText != text {
		t.Fatalf("unexpected search results: %#v", entries)
	}
	entries, err = database.ListEntries(ctx, ListEntriesParams{
		AccountID: accountID,
		Query:     "clipbord",
		Mode:      EntrySearchFuzzy,
		Limit:     10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].PlainText != text || entries[0].Score <= 0 {
		t.Fatalf("unexpected fuzzy search results: %#v", entries)
	}
	events, err := database.ListEvents(ctx, accountID, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0].TokenName != "macbook-token" || events[1].TokenName != "macbook-token" {
		t.Fatalf("unexpected token source: %#v", events)
	}
}
