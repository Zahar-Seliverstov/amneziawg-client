.PHONY: build run clean deps test

BINARY_NAME=awg-client
BUILD_DIR=build
MODULE=github.com/user/amnezia-web-client

# Версия берётся из манифеста Tauri — единственного места, где она объявлена
# и где её сверяет с тегом workflow релиза. Собираем backend отдельно, вне
# дерева проекта, — остаётся "dev".
TAURI_CONF := ../desktop/src-tauri/tauri.conf.json
VERSION ?= $(shell sed -n 's/^[[:space:]]*"version"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' $(TAURI_CONF) 2>/dev/null | head -1)
ifeq ($(strip $(VERSION)),)
VERSION := dev
endif

LDFLAGS := -s -w -X $(MODULE)/internal/version.Value=$(VERSION)

build:
	@mkdir -p $(BUILD_DIR)
	go build -trimpath -ldflags="$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/awg-client
	@echo "Собрано: $(BUILD_DIR)/$(BINARY_NAME) (версия $(VERSION))"

run: build
	sudo $(BUILD_DIR)/$(BINARY_NAME)

clean:
	rm -rf $(BUILD_DIR)

deps:
	go mod tidy

test:
	go test ./...
