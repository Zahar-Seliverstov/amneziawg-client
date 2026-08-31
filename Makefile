.PHONY: all build build-backend build-frontend embed-ui dev-backend dev-frontend \
        desktop desktop-dev desktop-run desktop-install desktop-uninstall \
        desktop-nopasswd desktop-nopasswd-off clean

# Установка приложения — в пользовательский префикс: root не нужен, права
# приложение и так берёт через polkit в момент запуска VPN.
DESKTOP_PREFIX ?= $(HOME)/.local
DESKTOP_LIBDIR := $(DESKTOP_PREFIX)/lib/awg-client
DESKTOP_APPDIR := $(DESKTOP_PREFIX)/share/applications
DESKTOP_ICONS  := $(DESKTOP_PREFIX)/share/icons/hicolor
DESKTOP_BUILD  := desktop/src-tauri/target/release

# Копия backend'а, на которую ссылается polkit-политика. Лежит под root:root —
# иначе разрешение «запускать без пароля» означало бы root по первому желанию
# любого процесса пользователя.
PRIV_LIBDIR := /usr/local/lib/awg-client
PRIV_POLICY := /usr/share/polkit-1/actions/org.amnezia.awgclient.policy

# Тройка цели Rust — по ней Tauri ищет sidecar-бинарник backend'а
TARGET_TRIPLE := $(shell rustc -Vv 2>/dev/null | awk '/^host:/{print $$2}')

all: build

# Полная сборка: UI -> вшивается в backend -> готовый бинарник
build: build-frontend embed-ui build-backend

build-backend:
	cd backend && make build

build-frontend:
	cd frontend && npm install --no-fund --no-audit && npm run generate

# Кладём собранный UI туда, откуда его забирает go:embed
embed-ui:
	rm -rf backend/internal/web/dist
	mkdir -p backend/internal/web/dist
	cp -a frontend/.output/public/. backend/internal/web/dist/
	touch backend/internal/web/dist/.gitkeep

# Development mode
dev-backend:
	cd backend && make run

dev-frontend:
	cd frontend && npm run dev

# ─── Десктопное приложение (Tauri) ──────────────────────────────────────────

# Сборка пакетов (.deb и AppImage) в desktop/src-tauri/target/release/bundle
#
# APPIMAGE_EXTRACT_AND_RUN — linuxdeploy сам является AppImage и без fuse2
# не монтируется; NO_STRIP — его strip не понимает секцию .relr.dyn в
# современных системных библиотеках.
desktop: build
	mkdir -p desktop/src-tauri/binaries
	cp backend/build/awg-client desktop/src-tauri/binaries/awg-client-$(TARGET_TRIPLE)
	cd desktop && npm install --no-fund --no-audit && \
		APPIMAGE_EXTRACT_AND_RUN=1 NO_STRIP=1 npm run build

# Запуск оболочки без упаковки (пересобирает backend, UI берётся вшитый)
desktop-dev: build
	mkdir -p desktop/src-tauri/binaries
	cp backend/build/awg-client desktop/src-tauri/binaries/awg-client-$(TARGET_TRIPLE)
	cd desktop && npm install --no-fund --no-audit && npm run dev

# Запустить уже собранное приложение
desktop-run:
	$(DESKTOP_BUILD)/awg-client-desktop

# Установка в ~/.local: два бинарника рядом (оболочка ищет backend возле себя),
# ярлык в меню и иконки. Ничего системного не трогаем.
desktop-install: desktop
	install -Dm755 $(DESKTOP_BUILD)/awg-client-desktop $(DESKTOP_LIBDIR)/awg-client-desktop
	install -Dm755 $(DESKTOP_BUILD)/awg-client         $(DESKTOP_LIBDIR)/awg-client
	install -d $(DESKTOP_PREFIX)/bin
	ln -sf $(DESKTOP_LIBDIR)/awg-client-desktop $(DESKTOP_PREFIX)/bin/awg-client-desktop
	install -Dm644 desktop/src-tauri/icons/32x32.png      $(DESKTOP_ICONS)/32x32/apps/awg-client.png
	install -Dm644 desktop/src-tauri/icons/64x64.png      $(DESKTOP_ICONS)/64x64/apps/awg-client.png
	install -Dm644 desktop/src-tauri/icons/128x128.png    $(DESKTOP_ICONS)/128x128/apps/awg-client.png
	install -Dm644 desktop/src-tauri/icons/128x128@2x.png $(DESKTOP_ICONS)/256x256/apps/awg-client.png
	install -d $(DESKTOP_APPDIR)
	sed 's|@BIN@|$(DESKTOP_LIBDIR)/awg-client-desktop|' desktop/awg-client.desktop.in \
		> $(DESKTOP_APPDIR)/awg-client.desktop
	-update-desktop-database $(DESKTOP_APPDIR) 2>/dev/null
	-gtk-update-icon-cache -qtf $(DESKTOP_ICONS) 2>/dev/null
	@echo "Установлено в $(DESKTOP_LIBDIR). Ищи «AWG Client» в меню."

# Разовая выдача прав: после неё backend стартует без запроса пароля —
# из активной локальной сессии, по политике polkit.
desktop-nopasswd: build
	sudo install -Dm755 -o root -g root backend/build/awg-client $(PRIV_LIBDIR)/awg-client
	sudo install -Dm644 -o root -g root desktop/org.amnezia.awgclient.policy $(PRIV_POLICY)
	@echo "Готово: пароль больше не спрашивается. Отменить — make desktop-nopasswd-off"

desktop-nopasswd-off:
	sudo rm -f $(PRIV_POLICY)
	sudo rm -rf $(PRIV_LIBDIR)
	@echo "Разрешение отозвано: backend снова спрашивает пароль при запуске"

desktop-uninstall:
	rm -rf $(DESKTOP_LIBDIR)
	rm -f $(DESKTOP_PREFIX)/bin/awg-client-desktop
	rm -f $(DESKTOP_APPDIR)/awg-client.desktop
	rm -f $(DESKTOP_ICONS)/*/apps/awg-client.png
	-update-desktop-database $(DESKTOP_APPDIR) 2>/dev/null
	-gtk-update-icon-cache -qtf $(DESKTOP_ICONS) 2>/dev/null

# Clean build artifacts
clean:
	cd backend && make clean
	rm -rf frontend/.nuxt frontend/.output frontend/node_modules
	rm -rf desktop/node_modules desktop/src-tauri/target desktop/src-tauri/binaries/awg-client-*
	find backend/internal/web/dist -mindepth 1 ! -name .gitkeep -delete
