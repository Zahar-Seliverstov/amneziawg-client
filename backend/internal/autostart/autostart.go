// Автозапуск десктопной оболочки через XDG: ярлык в ~/.config/autostart
// подхватывает любая современная среда рабочего стола, отдельной службы для
// этого не нужно. Тот же файл создаёт и галочка в меню трея — состояние у
// них общее, поэтому переключатель в настройках и трей всегда согласованы.
package autostart

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/user/amnezia-web-client/internal/desktopuser"
)

// Manager управляет ярлыком автозапуска для пользователя рабочего стола.
//
// Backend работает от root (его поднимает pkexec), поэтому «свой» дом здесь
// не годится: и ярлык, и каталог создаются в доме того пользователя, который
// сидит за десктопом, и ему же передаются во владение — иначе root-овый файл
// нельзя будет ни удалить, ни отредактировать из сессии пользователя.
type Manager struct {
	user       desktopuser.User // чей это рабочий стол
	desktopExe string           // путь к оболочке; пусто — автозапуск недоступен
}

// State описывает автозапуск для UI.
type State struct {
	Enabled   bool   `json:"enabled"`
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
}

// Известные места установки оболочки. Относительные пути ищем в доме
// пользователя (make desktop-install кладёт всё в ~/.local).
var (
	userCandidates = []string{
		".local/lib/awg-client/awg-client-desktop",
		".local/bin/awg-client-desktop",
	}
	systemCandidates = []string{
		"/usr/local/lib/awg-client/awg-client-desktop",
		"/usr/lib/awg-client/awg-client-desktop",
		"/usr/local/bin/awg-client-desktop",
		"/usr/bin/awg-client-desktop",
	}
)

// NewManager определяет пользователя рабочего стола и путь к оболочке.
//
// desktopExe оболочка передаёт сама (флаг -desktop-exe) — это единственный
// надёжный способ узнать, чем именно её запустили, включая сборку из дерева
// разработки. Без него ищем по стандартным местам установки.
// configPath нужен, чтобы опознать пользователя, когда pkexec не подсказал.
func NewManager(configPath, desktopExe string) *Manager {
	m := &Manager{user: desktopuser.Resolve(configPath)}
	m.desktopExe = resolveDesktopExe(desktopExe, m.user.Home)
	return m
}

func resolveDesktopExe(hint, home string) string {
	candidates := []string{}
	if hint != "" {
		candidates = append(candidates, hint)
	}
	if home != "" {
		for _, rel := range userCandidates {
			candidates = append(candidates, filepath.Join(home, rel))
		}
	}
	candidates = append(candidates, systemCandidates...)

	for _, path := range candidates {
		if isExecutable(path) {
			return path
		}
	}
	return ""
}

func isExecutable(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir() && info.Mode().Perm()&0111 != 0
}

// EntryPath — путь к ярлыку автозапуска.
func (m *Manager) EntryPath() string {
	if m.user.Home == "" {
		return ""
	}
	return filepath.Join(m.user.Home, ".config", "autostart", "awg-client.desktop")
}

// State сообщает UI, включён ли автозапуск и можно ли его вообще включить.
func (m *Manager) State() State {
	enabled := m.IsEnabled()

	switch {
	case m.user.Home == "":
		return State{Enabled: enabled, Reason: "Не удалось определить домашний каталог пользователя"}
	case m.desktopExe == "":
		// Ярлык мог остаться от прежней установки — тогда его как минимум
		// нужно дать выключить.
		if enabled {
			return State{Enabled: true, Available: true}
		}
		return State{Reason: "Десктопное приложение не установлено: автозапуск доступен только для него"}
	default:
		return State{Enabled: enabled, Available: true}
	}
}

// IsEnabled — ярлык автозапуска на месте?
func (m *Manager) IsEnabled() bool {
	path := m.EntryPath()
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// SetEnabled создаёт или удаляет ярлык автозапуска.
func (m *Manager) SetEnabled(enabled bool) error {
	path := m.EntryPath()
	if path == "" {
		return fmt.Errorf("не удалось определить домашний каталог пользователя")
	}

	if !enabled {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}

	if m.desktopExe == "" {
		return fmt.Errorf("не найдено десктопное приложение awg-client-desktop")
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	m.chown(dir)

	// --hidden: при входе в систему поднимаем только значок в трее,
	// окно пользователь откроет сам, если оно ему нужно.
	entry := fmt.Sprintf(
		"[Desktop Entry]\n"+
			"Type=Application\n"+
			"Name=AWG Client\n"+
			"Comment=Клиент AmneziaWG\n"+
			"Exec=\"%s\" --hidden\n"+
			"Icon=awg-client\n"+
			"Terminal=false\n"+
			"X-GNOME-Autostart-enabled=true\n",
		m.desktopExe,
	)

	if err := os.WriteFile(path, []byte(entry), 0644); err != nil {
		return err
	}
	m.chown(path)

	return nil
}

// chown отдаёт созданное пользователю рабочего стола: backend работает от
// root, и без этого файл остался бы неудаляемым из обычной сессии.
func (m *Manager) chown(path string) {
	if err := m.user.Own(path); err != nil {
		fmt.Fprintf(os.Stderr, "autostart: не удалось сменить владельца %s: %v\n", path, err)
	}
}
