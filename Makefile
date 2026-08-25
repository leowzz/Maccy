.DEFAULT_GOAL := help

PROJECT := Maccy.xcodeproj
SCHEME := Maccy
CONFIGURATION ?= Debug
BUILD_DIR ?= /tmp/maccy-local-build
APP := $(BUILD_DIR)/Maccy.app

.PHONY: help build run dev kill clean xcode server server-test

help:
	@echo "make build       Build the macOS app"
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

run: build
	@pkill -x Maccy 2>/dev/null || true
	open $(APP)

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
