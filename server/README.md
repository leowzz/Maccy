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
```

The keys under `auth` are token names and the values are static bearer secrets. Secrets must be unique. The server resolves each bearer secret to its token name and stores only the token name on every clipboard event. Secrets are never stored in PostgreSQL or written to logs.

The server reads `config.yaml` by default. Use a different path with `maccy-server -config /path/to/config.yaml`.

## Run locally

```sh
docker compose up --build
```

The API listens on `http://127.0.0.1:8080`. PostgreSQL listens on `127.0.0.1:54329` for local diagnostics and integration tests.

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

curl -H "Authorization: Bearer replace-with-a-long-random-token" \
  "http://127.0.0.1:8080/v1/events?after_seq=0&limit=50"
```

Use `next_cursor` from the entries response for the next page. Use `last_server_seq` from the events response for incremental synchronization. Event responses include `token_name`, which identifies the static token that wrote the event.

## Tests

```sh
go test ./...

MACCY_TEST_DATABASE_URL='postgres://maccy:maccy@127.0.0.1:54329/maccy?sslmode=disable' \
  go test ./internal/store -run TestPostgres
```

The Compose defaults are for localhost development only. Use TLS, strong token secrets, encrypted storage, and a private network for deployment. Never enable HTTP request-body logging because payloads contain clipboard text.

