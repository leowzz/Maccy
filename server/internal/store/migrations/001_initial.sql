CREATE EXTENSION IF NOT EXISTS pg_trgm;

CREATE TABLE clipboard_entries (
    id TEXT PRIMARY KEY,
    account_id TEXT NOT NULL,
    content_sha256 CHAR(64) NOT NULL,
    plain_text TEXT NOT NULL,
    text_bytes BIGINT NOT NULL CHECK (text_bytes >= 0),
    first_copied_at TIMESTAMPTZ NOT NULL,
    last_copied_at TIMESTAMPTZ NOT NULL,
    occurrence_count BIGINT NOT NULL DEFAULT 0 CHECK (occurrence_count >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX clipboard_entries_account_hash_uq
    ON clipboard_entries (account_id, content_sha256);
CREATE INDEX clipboard_entries_account_recent_idx
    ON clipboard_entries (account_id, last_copied_at DESC, id DESC);
CREATE INDEX clipboard_entries_plain_text_trgm_idx
    ON clipboard_entries USING GIN (plain_text gin_trgm_ops);

CREATE TABLE clipboard_events (
    id TEXT PRIMARY KEY,
    account_id TEXT NOT NULL,
    token_name TEXT NOT NULL,
    device_id TEXT NOT NULL,
    client_event_id TEXT NOT NULL,
    entry_id TEXT NOT NULL,
    copied_at TIMESTAMPTZ NOT NULL,
    received_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    source_bundle_id TEXT,
    server_seq BIGSERIAL NOT NULL
);

CREATE UNIQUE INDEX clipboard_events_account_device_client_uq
    ON clipboard_events (account_id, device_id, client_event_id);
CREATE UNIQUE INDEX clipboard_events_server_seq_uq
    ON clipboard_events (server_seq);
CREATE INDEX clipboard_events_account_seq_idx
    ON clipboard_events (account_id, server_seq);
CREATE INDEX clipboard_events_account_copied_idx
    ON clipboard_events (account_id, copied_at DESC);
CREATE INDEX clipboard_events_account_entry_idx
    ON clipboard_events (account_id, entry_id, copied_at DESC);
CREATE INDEX clipboard_events_account_source_idx
    ON clipboard_events (account_id, source_bundle_id, copied_at DESC);
CREATE INDEX clipboard_events_account_token_idx
    ON clipboard_events (account_id, token_name, copied_at DESC);
