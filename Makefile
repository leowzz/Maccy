.DEFAULT_GOAL := help

PROJECT := Maccy.xcodeproj
SCHEME := Maccy
CONFIGURATION ?= Debug
BUILD_DIR ?= /tmp/maccy-local-build
APP := $(BUILD_DIR)/Maccy.app
RELEASE_DIR ?= $(CURDIR)/dist
RELEASE_APP := $(RELEASE_DIR)/Maccy.app
PYTHON ?= python3
ENV_FILE ?= .env

.PHONY: help build release build-release package version-check test-release run dev kill clean xcode server server-test

help:
	@echo "make build       Build the macOS app"
	@echo "make build-release Build an optimized Release app in ./dist"
	@echo "make package     Build a universal macOS DMG and checksum"
	@echo "make release     Bump patch, commit and tag (V=vX.Y.Z overrides; no push)"
	@echo "make version-check / test-release  Validate version / release tooling"
	@echo "make run         Build and launch the macOS app"
	@echo "make dev         Alias for make run"
	@echo "make kill        Stop all running Maccy instances"
	@echo "make clean       Stop Maccy and clean local build output"
	@echo "make xcode       Open the Xcode project"
	@echo "make server      Run the clipboard backend"
	@echo "make server-test Run backend tests"

build:
	xcodebuild \
		-project $(PROJECT) \
		-scheme $(SCHEME) \
		-configuration $(CONFIGURATION) \
		-destination 'platform=macOS' \
		CODE_SIGN_STYLE=Manual \
		CODE_SIGN_IDENTITY=- \
		DEVELOPMENT_TEAM= \
		CONFIGURATION_BUILD_DIR=$(BUILD_DIR) \
		build

release:
	@$(PYTHON) scripts/release.py release --env-file "$(ENV_FILE)" --version "$(V)"

version-check:
	@$(PYTHON) scripts/release.py check --env-file "$(ENV_FILE)" --version "$(V)"

test-release:
	@$(PYTHON) -m unittest discover -s tests -p 'test_release.py'

package: version-check
	@ENV_FILE="$(ENV_FILE)" RELEASE_DIR="$(RELEASE_DIR)" PYTHON="$(PYTHON)" bash scripts/package-macos.sh

build-release:
	@mkdir -p $(RELEASE_DIR)
	xcodebuild \
		-project $(PROJECT) \
		-scheme $(SCHEME) \
		-configuration Release \
		-destination 'platform=macOS' \
		CODE_SIGN_STYLE=Manual \
		CODE_SIGN_IDENTITY=- \
		DEVELOPMENT_TEAM= \
		ENABLE_HARDENED_RUNTIME=NO \
		CONFIGURATION_BUILD_DIR=$(RELEASE_DIR) \
		build

run: build
	@pkill -x Maccy 2>/dev/null || true
	@for attempt in 1 2 3 4 5; do \
		if ! pgrep -x Maccy >/dev/null 2>&1; then break; fi; \
		sleep 0.2; \
	done
	open -n $(APP)

dev: run

kill:
	@pkill -x Maccy 2>/dev/null || true

clean: kill
	xcodebuild \
		-project $(PROJECT) \
		-scheme $(SCHEME) \
		-configuration $(CONFIGURATION) \
		CONFIGURATION_BUILD_DIR=$(BUILD_DIR) \
		clean

xcode:
	open $(PROJECT)

server:
	$(MAKE) -C server dev

server-test:
	$(MAKE) -C server test
