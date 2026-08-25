.PHONY: dev dev-server test test-server

dev:
	cd server && docker compose up --build

dev-server:
	cd server && go run ./cmd/server -config config.local.yaml

test: test-server

test-server:
	cd server && go test ./...
