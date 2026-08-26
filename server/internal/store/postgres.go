package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Postgres struct {
	pool *pgxpool.Pool
}

func Open(ctx context.Context, databaseURL string, maxConnections int32) (*Postgres, error) {
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse database URL: %w", err)
	}
	config.MaxConns = maxConnections
	config.MinConns = 1
	config.MaxConnIdleTime = 5 * time.Minute
	config.MaxConnLifetime = 30 * time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("create database pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}

	return &Postgres{pool: pool}, nil
}

func (p *Postgres) Close() {
	p.pool.Close()
}

func (p *Postgres) Ping(ctx context.Context) error {
	return p.pool.Ping(ctx)
}

func (p *Postgres) Ingest(
	ctx context.Context,
	accountID string,
	tokenName string,
	deviceID string,
	events []UploadEvent,
) (IngestResult, error) {
	tx, err := p.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return IngestResult{}, fmt.Errorf("begin ingest transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	result := IngestResult{}
	for _, event := range events {
		var existingSeq int64
		err := tx.QueryRow(ctx, `
			SELECT server_seq
			FROM clipboard_events
			WHERE account_id = $1 AND device_id = $2 AND client_event_id = $3
		`, accountID, deviceID, event.ClientEventID).Scan(&existingSeq)
		if err == nil {
			result.Duplicates++
			result.LastServerSeq = max(result.LastServerSeq, existingSeq)
			continue
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return IngestResult{}, fmt.Errorf("check existing event: %w", err)
		}

		entryID, err := newID()
		if err != nil {
			return IngestResult{}, fmt.Errorf("generate entry ID: %w", err)
		}
		if err := tx.QueryRow(ctx, `
			INSERT INTO clipboard_entries (
				id, account_id, content_sha256, plain_text, text_bytes,
				first_copied_at, last_copied_at, occurrence_count
			)
			VALUES ($1, $2, $3, $4, $5, $6, $6, 0)
			ON CONFLICT (account_id, content_sha256)
			DO UPDATE SET content_sha256 = EXCLUDED.content_sha256
			RETURNING id
		`, entryID, accountID, event.ContentSHA256, event.PlainText, len([]byte(event.PlainText)), event.CopiedAt).Scan(&entryID); err != nil {
			return IngestResult{}, fmt.Errorf("upsert clipboard entry: %w", err)
		}

		eventID, err := newID()
		if err != nil {
			return IngestResult{}, fmt.Errorf("generate event ID: %w", err)
		}
		var serverSeq int64
		err = tx.QueryRow(ctx, `
			INSERT INTO clipboard_events (
				id, account_id, token_name, device_id, client_event_id, entry_id,
				copied_at, source_bundle_id
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7, NULLIF($8, ''))
			ON CONFLICT (account_id, device_id, client_event_id) DO NOTHING
			RETURNING server_seq
		`, eventID, accountID, tokenName, deviceID, event.ClientEventID, entryID, event.CopiedAt, event.SourceBundleID).Scan(&serverSeq)
		if errors.Is(err, pgx.ErrNoRows) {
			result.Duplicates++
			continue
		}
		if err != nil {
			return IngestResult{}, fmt.Errorf("insert clipboard event: %w", err)
		}

		if _, err := tx.Exec(ctx, `
			UPDATE clipboard_entries
			SET first_copied_at = LEAST(first_copied_at, $2),
				last_copied_at = GREATEST(last_copied_at, $2),
				occurrence_count = occurrence_count + 1,
				updated_at = NOW()
			WHERE id = $1 AND account_id = $3
		`, entryID, event.CopiedAt, accountID); err != nil {
			return IngestResult{}, fmt.Errorf("update clipboard entry counters: %w", err)
		}

		result.Accepted++
		result.LastServerSeq = max(result.LastServerSeq, serverSeq)
	}

	if err := tx.Commit(ctx); err != nil {
		return IngestResult{}, fmt.Errorf("commit ingest transaction: %w", err)
	}
	return result, nil
}

func (p *Postgres) ListEntries(ctx context.Context, params ListEntriesParams) ([]Entry, error) {
	mode := params.Mode
	if mode == "" {
		mode = EntrySearchContains
	}

	query := `
		SELECT id, content_sha256, plain_text, text_bytes,
			first_copied_at, last_copied_at, occurrence_count,
	`
	args := []any{params.AccountID}
	switch mode {
	case EntrySearchContains:
		query += `0::real AS search_score
		FROM clipboard_entries
		WHERE account_id = $1
			AND ($2 = '' OR plain_text ILIKE '%' || $2 || '%' ESCAPE E'\\')
		`
		args = append(args, escapeLike(params.Query))
		if params.Cursor != nil {
			query += ` AND (last_copied_at, id) < ($3, $4)`
			args = append(args, params.Cursor.LastCopiedAt, params.Cursor.ID)
		}
		query += fmt.Sprintf(" ORDER BY last_copied_at DESC, id DESC LIMIT $%d", len(args)+1)
	case EntrySearchFuzzy:
		query += `word_similarity($2, plain_text) AS search_score
		FROM clipboard_entries
		WHERE account_id = $1
			AND $2 <% plain_text
		`
		args = append(args, params.Query)
		if params.Cursor != nil {
			query += ` AND (word_similarity($2, plain_text), last_copied_at, id) < ($3, $4, $5)`
			args = append(args, params.Cursor.Score, params.Cursor.LastCopiedAt, params.Cursor.ID)
		}
		query += fmt.Sprintf(" ORDER BY word_similarity($2, plain_text) DESC, last_copied_at DESC, id DESC LIMIT $%d", len(args)+1)
	default:
		return nil, fmt.Errorf("unsupported entry search mode %q", mode)
	}
	args = append(args, params.Limit)

	rows, err := p.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list clipboard entries: %w", err)
	}
	defer rows.Close()

	entries, err := pgx.CollectRows(rows, pgx.RowToStructByName[Entry])
	if err != nil {
		return nil, fmt.Errorf("scan clipboard entries: %w", err)
	}
	return entries, nil
}

func (p *Postgres) ListEvents(ctx context.Context, accountID string, afterSeq int64, limit int) ([]Event, error) {
	rows, err := p.pool.Query(ctx, `
		SELECT event.server_seq, event.client_event_id, event.entry_id,
			event.device_id, event.copied_at, event.received_at,
			COALESCE(event.source_bundle_id, '') AS source_bundle_id, event.token_name,
			entry.content_sha256, entry.plain_text
		FROM clipboard_events AS event
		JOIN clipboard_entries AS entry
			ON entry.id = event.entry_id AND entry.account_id = event.account_id
		WHERE event.account_id = $1 AND event.server_seq > $2
		ORDER BY event.server_seq
		LIMIT $3
	`, accountID, afterSeq, limit)
	if err != nil {
		return nil, fmt.Errorf("list clipboard events: %w", err)
	}
	defer rows.Close()

	events, err := pgx.CollectRows(rows, pgx.RowToStructByName[Event])
	if err != nil {
		return nil, fmt.Errorf("scan clipboard events: %w", err)
	}
	return events, nil
}

func escapeLike(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `%`, `\%`)
	return strings.ReplaceAll(value, `_`, `\_`)
}

func newID() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	bytes[6] = (bytes[6] & 0x0f) | 0x40
	bytes[8] = (bytes[8] & 0x3f) | 0x80
	encoded := make([]byte, 32)
	hex.Encode(encoded, bytes)
	return string(encoded[0:8]) + "-" + string(encoded[8:12]) + "-" +
		string(encoded[12:16]) + "-" + string(encoded[16:20]) + "-" + string(encoded[20:32]), nil
}
