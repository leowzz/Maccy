.PHONY: dev dev-server test test-server

GO ?= go
CONFIG ?= config.yaml

dev: dev-server

dev-server:
	$(GO) run ./cmd/server -config $(CONFIG)

test: test-server

test-server:
	$(GO) test ./...
