.PHONY: build run clean install

BINARY_NAME=awg-client
BUILD_DIR=build

build:
	@mkdir -p $(BUILD_DIR)
	go build -trimpath -ldflags="-s -w" -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/awg-client

run: build
	sudo $(BUILD_DIR)/$(BINARY_NAME)

clean:
	rm -rf $(BUILD_DIR)

install: build
	sudo cp $(BUILD_DIR)/$(BINARY_NAME) /usr/local/bin/
	sudo cp systemd/awg-client.service /etc/systemd/system/
	sudo systemctl daemon-reload
	@echo "Installed! Enable with: sudo systemctl enable --now awg-client"

uninstall:
	sudo systemctl stop awg-client || true
	sudo systemctl disable awg-client || true
	sudo rm -f /etc/systemd/system/awg-client.service
	sudo rm -f /usr/local/bin/$(BINARY_NAME)
	sudo systemctl daemon-reload

deps:
	go mod tidy

test:
	go test ./...
