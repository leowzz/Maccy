package store

import (
	"context"
	"time"
)

type UploadEvent struct {
	ClientEventID  string
	CopiedAt       time.Time
	SourceBundleID string
	PlainText      string
	ContentSHA256  string
}

type IngestResult struct {
	Accepted      int
	Duplicates    int
	LastServerSeq int64
}

type Entry struct {
	ID              string    `json:"id"`
	ContentSHA256   string    `json:"content_sha256"`
	PlainText       string    `json:"plain_text"`
	TextBytes       int64     `json:"text_bytes"`
	FirstCopiedAt   time.Time `json:"first_copied_at"`
	LastCopiedAt    time.Time `json:"last_copied_at"`
	OccurrenceCount int64     `json:"occurrence_count"`
}

type EntryCursor struct {
	LastCopiedAt time.Time
	ID           string
}

type ListEntriesParams struct {
	AccountID string
	Query     string
	Cursor    *EntryCursor
	Limit     int
}

type Event struct {
	ServerSeq      int64     `json:"server_seq"`
	ClientEventID  string    `json:"client_event_id"`
	EntryID        string    `json:"entry_id"`
	DeviceID       string    `json:"device_id"`
	CopiedAt       time.Time `json:"copied_at"`
	ReceivedAt     time.Time `json:"received_at"`
	SourceBundleID string    `json:"source_bundle_id,omitempty"`
	TokenName      string    `json:"token_name"`
	ContentSHA256  string    `json:"content_sha256"`
	PlainText      string    `json:"plain_text"`
}

type Repository interface {
	Ping(context.Context) error
	Ingest(context.Context, string, string, string, []UploadEvent) (IngestResult, error)
	ListEntries(context.Context, ListEntriesParams) ([]Entry, error)
	ListEvents(context.Context, string, int64, int) ([]Event, error)
}
