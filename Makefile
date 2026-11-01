.PHONY: all build build-frontend frontend-deps test dev-service dev-ui dev-frontend \
        dist dist-linux dist-windows clean

# Единственное место, где объявлена версия. Её же сверяет с тегом workflow
# релиза; службе она подставляется на сборке, интерфейс спрашивает её у службы.
VERSION := $(shell cat VERSION)
MODULE  := github.com/user/amnezia-web-client
LDFLAGS := -s -w -X $(MODULE)/internal/version.Value=$(VERSION)
GOBUILD := cd backend && CGO_ENABLED=0 go build -trimpath -ldflags="$(LDFLAGS)"

DIST := dist

# wintun — драйвер TUN для Windows от авторов WireGuard. Библиотека лежит
# рядом с exe; сумма сверяется, потому что это драйвер ядра.
WINTUN_VERSION := 0.14.1
WINTUN_SHA256  := 07c256185d6ee3652e09fa55c0b673e2624b565e02c4b9091c79ca7d2f24ef51
WINTUN_ZIP     := backend/build/wintun-$(WINTUN_VERSION).zip

# WinDivert — подписанный драйвер перехвата пакетов: на нём на Windows
# работают правила для программ (internal/appsteer). Лицензия LGPLv3, её текст
# кладётся рядом. Сумма снята с выпуска v2.2.2 на GitHub.
WINDIVERT_VERSION := 2.2.2
WINDIVERT_SHA256  := 63cb41763bb4b20f600b6de04e991a9c2be73279e317d4d82f237b150c5f3f15
WINDIVERT_ZIP     := backend/build/WinDivert-$(WINDIVERT_VERSION)-A.zip

all: build build-frontend

# Обе программы под текущую систему — в backend/build.
build:
	$(GOBUILD) -o build/awg-service ./cmd/awg-service
	$(GOBUILD) -o build/awg-ui ./cmd/awg-ui
	@echo "Собрано: backend/build (версия $(VERSION))"

frontend-deps:
	cd frontend && npm ci --no-fund --no-audit

build-frontend: frontend-deps
	cd frontend && npm run build

test:
	cd backend && go vet ./... && GOOS=windows go vet ./... && go test ./...
	cd frontend && npm test

# ─── Разработка ─────────────────────────────────────────────────────────────
# Служба нужна с правами root: она создаёт TUN и правит маршруты. Состояние
# разработки держим отдельно от установленной службы.
dev-service: build
	sudo backend/build/awg-service -config $(CURDIR)/backend/build/dev/config.json

# Интерфейс со сборкой: как его увидит пользователь.
dev-ui: build build-frontend
	backend/build/awg-ui -www frontend/dist

# Интерфейс с горячей перезагрузкой: /api Vite пересылает службе сам.
dev-frontend:
	cd frontend && npm run dev

# ─── Архивы для выпуска ─────────────────────────────────────────────────────
dist: dist-linux dist-windows

dist-linux: build-frontend
	rm -rf $(DIST)/linux && mkdir -p $(DIST)/linux/awg-client
	cd backend && GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags="$(LDFLAGS)" -o ../$(DIST)/linux/awg-client/awg-service ./cmd/awg-service
	cd backend && GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags="$(LDFLAGS)" -o ../$(DIST)/linux/awg-client/awg-ui ./cmd/awg-ui
	cp -r frontend/dist $(DIST)/linux/awg-client/www
	cp packaging/linux/* $(DIST)/linux/awg-client/
	tar -C $(DIST)/linux -czf $(DIST)/awg-client_$(VERSION)_linux_amd64.tar.gz awg-client
	@echo "Готово: $(DIST)/awg-client_$(VERSION)_linux_amd64.tar.gz"

$(WINTUN_ZIP):
	mkdir -p $(dir $@)
	curl -sSfL -o $@.part https://www.wintun.net/builds/wintun-$(WINTUN_VERSION).zip
	echo "$(WINTUN_SHA256)  $@.part" | sha256sum -c -
	mv $@.part $@

$(WINDIVERT_ZIP):
	mkdir -p $(dir $@)
	curl -sSfL -o $@.part https://github.com/basil00/WinDivert/releases/download/v$(WINDIVERT_VERSION)/WinDivert-$(WINDIVERT_VERSION)-A.zip
	echo "$(WINDIVERT_SHA256)  $@.part" | sha256sum -c -
	mv $@.part $@

dist-windows: build-frontend $(WINTUN_ZIP) $(WINDIVERT_ZIP)
	rm -rf $(DIST)/windows && mkdir -p "$(DIST)/windows/AWG Client"
	cd backend && GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags="$(LDFLAGS)" -o "../$(DIST)/windows/AWG Client/awg-service.exe" ./cmd/awg-service
	cd backend && GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags="$(LDFLAGS)" -o "../$(DIST)/windows/AWG Client/awg-ui.exe" ./cmd/awg-ui
	unzip -jq $(WINTUN_ZIP) wintun/bin/amd64/wintun.dll -d "$(DIST)/windows/AWG Client"
	unzip -jq $(WINDIVERT_ZIP) WinDivert-$(WINDIVERT_VERSION)-A/x64/WinDivert.dll WinDivert-$(WINDIVERT_VERSION)-A/x64/WinDivert64.sys -d "$(DIST)/windows/AWG Client"
	unzip -p $(WINDIVERT_ZIP) WinDivert-$(WINDIVERT_VERSION)-A/LICENSE > "$(DIST)/windows/AWG Client/WinDivert-LICENSE.txt"
	cp -r frontend/dist "$(DIST)/windows/AWG Client/www"
	cp packaging/windows/* "$(DIST)/windows/AWG Client/"
	cd $(DIST)/windows && rm -f ../awg-client_$(VERSION)_windows_amd64.zip && python3 -m zipfile -c ../awg-client_$(VERSION)_windows_amd64.zip "AWG Client"
	@echo "Готово: $(DIST)/awg-client_$(VERSION)_windows_amd64.zip"

clean:
	rm -rf backend/build $(DIST)
	rm -rf frontend/dist frontend/node_modules
