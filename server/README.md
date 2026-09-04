# Maccy Server

Stores deduplicated clipboard text and every copy occurrence. The service accepts text only; images, files, HTML, and RTF are intentionally outside the first version.

## Configuration

Copy the example configuration before starting the service:

```sh
cp config.example.yaml config.yaml
```

```yaml
server:
  listen_address: ":8080"

database:
  dsn: "postgres://maccy:maccy@postgres:5432/maccy?sslmode=disable"
  max_connections: 20

account_id: "default"

auth:
  my-macbook: "replace-with-a-long-random-token"
  work-mac: "another-long-random-token"

zvec:
  enabled: true
  node_path: "node"
  worker_path: "zvec_worker.mjs"
  collection_path: ".zvec/clipboard"
  model_cache_path: ".zvec/models"
  fts_tokenizer: "jieba"
  rrf_rank_constant: 60
  sync_batch_size: 20
```

The keys under `auth` are token names and the values are static bearer secrets. Secrets must be unique. The server resolves each bearer secret to its token name and stores only the token name on every clipboard event. Secrets are never stored in PostgreSQL or written to logs.

The server reads `config.yaml` by default. Use a different path with `maccy-server -config /path/to/config.yaml`.

Zvec search is optional and disabled by default. When enabled, install the Node dependencies with `npm install`. The worker uses the fixed local embedding model `local/potion-code-16m-v2`; clipboard text is embedded on the server and is not sent to a remote embedding API. The first start downloads the model files into `model_cache_path`, while later starts reuse that cache.

PostgreSQL remains the source of truth. At startup the server scans existing entries and adds only IDs missing from the Zvec collection. New ingests update PostgreSQL first and then Zvec; an indexing failure returns HTTP 503, and retrying the same event safely completes the index update. Keep `collection_path` and `model_cache_path` on persistent storage. The included Compose file persists both under the `maccy-zvec-data` volume.

## Run locally

```sh
make dev
```

This starts the Go server directly on the host using `config.yaml`. The configured PostgreSQL DSN must already be reachable.

The API listens on `http://127.0.0.1:8080`.

For the optional containerized stack, run `docker compose up --build` from this directory.

```sh
curl http://127.0.0.1:8080/healthz
```

## Upload text

`content_sha256` is the lowercase SHA-256 of the exact UTF-8 text bytes. Use the secret value from `auth` as the bearer token.

```sh
curl -X POST http://127.0.0.1:8080/v1/sync/events:batch \
  -H "Authorization: Bearer replace-with-a-long-random-token" \
  -H "Content-Type: application/json" \
  -d '{
    "device_id": "my-mac",
    "events": [{
      "client_event_id": "event-1",
      "copied_at": "2026-08-25T12:00:00Z",
      "source_bundle_id": "com.apple.TextEdit",
      "plain_text": "hello",
      "content_sha256": "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
    }]
  }'
```

Retrying the same `(device_id, client_event_id)` is safe. A repeated text with a new client event creates another occurrence without duplicating the text.

## Read history

```sh
curl -H "Authorization: Bearer replace-with-a-long-random-token" \
  "http://127.0.0.1:8080/v1/entries?limit=50&q=hello"

# Optional trigram-based fuzzy search. Queries shorter than 3 characters are rejected.
curl -H "Authorization: Bearer replace-with-a-long-random-token" \
  "http://127.0.0.1:8080/v1/entries?limit=20&q=helo&mode=fuzzy"

# Optional Zvec BM25 + vector search, fused with reciprocal rank fusion.
curl -H "Authorization: Bearer replace-with-a-long-random-token" \
  "http://127.0.0.1:8080/v1/entries?limit=20&q=local%20semantic%20search&mode=hybrid"

curl -H "Authorization: Bearer replace-with-a-long-random-token" \
  "http://127.0.0.1:8080/v1/events?after_seq=0&limit=50"
```

Use `next_cursor` from the entries response for the next page. Hybrid cursors use offset pagination over the current index ranking, so adding entries between pages can change the remaining order. Use `last_server_seq` from the events response for incremental synchronization. Event responses include `token_name`, which identifies the static token that wrote the event.

The collection metadata records the embedding model, dimension, metric, and tokenizer. If one of those settings changes, startup fails instead of opening an incompatible index. Stop the server, remove the configured collection directory and its adjacent `.maccy.json` metadata file, then restart to rebuild it from PostgreSQL.

## Tests

```sh
make test

MACCY_TEST_DATABASE_URL='postgres://maccy:maccy@127.0.0.1:54329/maccy?sslmode=disable' \
  go test ./internal/store -run TestPostgres
```

The Compose defaults are for localhost development only. Use TLS, strong token secrets, encrypted storage, and a private network for deployment. Never enable HTTP request-body logging because payloads contain clipboard text.
