.PHONY: dev dev-server test test-server test-zvec-worker

GO ?= go
NODE ?= node
CONFIG ?= config.yaml

dev: dev-server

dev-server:
	$(GO) run ./cmd/server -config $(CONFIG)

test: test-server test-zvec-worker

test-server:
	$(GO) test ./...

test-zvec-worker:
	$(NODE) --check zvec_worker.mjs
