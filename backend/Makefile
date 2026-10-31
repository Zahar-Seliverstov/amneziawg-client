.PHONY: build test clean

MODULE  := github.com/user/amnezia-web-client
VERSION ?= $(shell cat ../VERSION 2>/dev/null || echo dev)
LDFLAGS := -s -w -X $(MODULE)/internal/version.Value=$(VERSION)

# Обе программы под текущую систему. Архивы для выпуска собирает корневой
# Makefile (make dist).
build:
	@mkdir -p build
	CGO_ENABLED=0 go build -trimpath -ldflags="$(LDFLAGS)" -o build/awg-service ./cmd/awg-service
	CGO_ENABLED=0 go build -trimpath -ldflags="$(LDFLAGS)" -o build/awg-ui ./cmd/awg-ui

test:
	go vet ./...
	GOOS=windows go vet ./...
	go test ./...

clean:
	rm -rf build
