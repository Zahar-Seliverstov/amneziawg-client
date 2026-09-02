package autostart

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/user/amnezia-web-client/internal/desktopuser"
)

// newManager собирает менеджер с домом во временном каталоге: настоящий
// Resolve смотрел бы на pkexec и на владельца файлов, а проверять надо не его.
func newManager(t *testing.T, desktopExe string) (*Manager, string) {
	t.Helper()
	home := t.TempDir()
	return &Manager{user: desktopuser.User{Home: home}, desktopExe: desktopExe}, home
}

// executable создаёт файл, который выглядит как оболочка.
func executable(t *testing.T, path string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSetEnabledCreatesAndRemovesEntry(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "awg-client-desktop")
	executable(t, exe)

	m, home := newManager(t, exe)

	if m.IsEnabled() {
		t.Fatal("автозапуск включён до того, как его включили")
	}

	if err := m.SetEnabled(true); err != nil {
		t.Fatalf("не удалось включить: %v", err)
	}
	if !m.IsEnabled() {
		t.Fatal("ярлык не появился")
	}

	path := filepath.Join(home, ".config", "autostart", "awg-client.desktop")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ярлык не там, где его ищет среда рабочего стола: %v", err)
	}

	entry := string(data)
	// Путь к оболочке в кавычках, иначе каталог с пробелом разорвёт команду.
	if !strings.Contains(entry, `Exec="`+exe+`" --hidden`) {
		t.Errorf("в ярлыке не та команда запуска:\n%s", entry)
	}
	if !strings.Contains(entry, "[Desktop Entry]") || !strings.Contains(entry, "Type=Application") {
		t.Errorf("ярлык не похож на .desktop:\n%s", entry)
	}

	// Повторное включение — не ошибка: переключатель могли нажать дважды.
	if err := m.SetEnabled(true); err != nil {
		t.Fatalf("повторное включение отказало: %v", err)
	}

	if err := m.SetEnabled(false); err != nil {
		t.Fatalf("не удалось выключить: %v", err)
	}
	if m.IsEnabled() {
		t.Fatal("ярлык остался на месте")
	}

	// Выключение того, что и так выключено, тоже проходит молча: файла нет —
	// значит, требуемое состояние уже достигнуто.
	if err := m.SetEnabled(false); err != nil {
		t.Fatalf("повторное выключение отказало: %v", err)
	}
}

func TestSetEnabledWithoutDesktopExe(t *testing.T) {
	m, _ := newManager(t, "")

	if err := m.SetEnabled(true); err == nil {
		t.Fatal("автозапуск включён без оболочки, которую он должен запускать")
	}
	if m.IsEnabled() {
		t.Fatal("ярлык всё же создан")
	}
}

func TestSetEnabledWithoutHome(t *testing.T) {
	m := &Manager{}

	if got := m.EntryPath(); got != "" {
		t.Errorf("без дома путь ярлыка не пуст: %q", got)
	}
	if err := m.SetEnabled(true); err == nil {
		t.Fatal("включение без дома пользователя должно отказывать")
	}
}

func TestState(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "awg-client-desktop")
	executable(t, exe)

	t.Run("оболочка на месте", func(t *testing.T) {
		m, _ := newManager(t, exe)
		if got := m.State(); !got.Available || got.Enabled || got.Reason != "" {
			t.Errorf("неожиданное состояние: %+v", got)
		}
	})

	t.Run("оболочки нет", func(t *testing.T) {
		m, _ := newManager(t, "")
		got := m.State()
		if got.Available || got.Reason == "" {
			t.Errorf("недоступность не объяснена: %+v", got)
		}
	})

	// Ярлык от прежней установки обязан оставаться выключаемым, иначе он
	// переживёт удаление приложения и будет каждый вход звать несуществующее.
	t.Run("оболочки нет, ярлык остался", func(t *testing.T) {
		m, home := newManager(t, exe)
		if err := m.SetEnabled(true); err != nil {
			t.Fatal(err)
		}

		orphan := &Manager{user: desktopuser.User{Home: home}}
		got := orphan.State()
		if !got.Enabled || !got.Available {
			t.Errorf("осиротевший ярлык нельзя выключить: %+v", got)
		}
	})

	t.Run("дом неизвестен", func(t *testing.T) {
		m := &Manager{desktopExe: exe}
		if got := m.State(); got.Available || got.Reason == "" {
			t.Errorf("без дома автозапуск не может быть доступен: %+v", got)
		}
	})
}

// Подсказка от оболочки главнее найденного по стандартным местам: только она
// знает, чем приложение запустили на самом деле — включая сборку из дерева
// разработки.
func TestResolveDesktopExePrefersHint(t *testing.T) {
	home := t.TempDir()
	installed := executable(t, filepath.Join(home, ".local/lib/awg-client/awg-client-desktop"))
	hint := executable(t, filepath.Join(t.TempDir(), "target/debug/awg-client-desktop"))

	if got := resolveDesktopExe(hint, home); got != hint {
		t.Errorf("выбран %q вместо подсказки %q", got, hint)
	}
	if got := resolveDesktopExe("", home); got != installed {
		t.Errorf("установленная оболочка не найдена: %q", got)
	}

	// Несуществующая подсказка не должна перебивать установленную оболочку:
	// оболочку могли запустить и удалить, а ярлыку нужен рабочий путь.
	if got := resolveDesktopExe(filepath.Join(home, "нет-такого"), home); got != installed {
		t.Errorf("мёртвая подсказка вытеснила установленную оболочку: %q", got)
	}
}

func TestIsExecutable(t *testing.T) {
	dir := t.TempDir()

	plain := filepath.Join(dir, "plain")
	if err := os.WriteFile(plain, nil, 0644); err != nil {
		t.Fatal(err)
	}

	cases := map[string]bool{
		executable(t, filepath.Join(dir, "exe")): true,
		plain:                                    false,
		dir:                                      false,
		filepath.Join(dir, "нет-такого"): false,
	}

	for path, want := range cases {
		if got := isExecutable(path); got != want {
			t.Errorf("isExecutable(%q) = %v, ожидалось %v", path, got, want)
		}
	}
}
