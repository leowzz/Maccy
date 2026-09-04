.PHONY: dev dev-server test test-server

GO ?= go
CGO_ENABLED ?= 0
CONFIG ?= config.yaml

dev: dev-server

dev-server:
	CGO_ENABLED=$(CGO_ENABLED) $(GO) run ./cmd/server -config $(CONFIG)

test: test-server

test-server:
	CGO_ENABLED=$(CGO_ENABLED) $(GO) test ./...
