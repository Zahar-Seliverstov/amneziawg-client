// Автозапуск десктопной оболочки через XDG: ярлык в ~/.config/autostart
// подхватывает любая современная среда рабочего стола, отдельной службы для
// этого не нужно. Тот же файл создаёт и галочка в меню трея — состояние у
// них общее, поэтому переключатель в настройках и трей всегда согласованы.
package autostart

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"syscall"
)

// Manager управляет ярлыком автозапуска для пользователя рабочего стола.
//
// Backend работает от root (его поднимает pkexec), поэтому «свой» дом здесь
// не годится: и ярлык, и каталог создаются в доме того пользователя, который
// сидит за десктопом, и ему же передаются во владение — иначе root-овый файл
// нельзя будет ни удалить, ни отредактировать из сессии пользователя.
type Manager struct {
	home       string // домашний каталог пользователя рабочего стола
	uid, gid   int    // его же uid/gid, чтобы не оставлять root-овых файлов
	desktopExe string // путь к оболочке; пусто — автозапуск недоступен
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
	m := &Manager{}
	m.home, m.uid, m.gid = resolveDesktopUser(configPath)
	m.desktopExe = resolveDesktopExe(desktopExe, m.home)
	return m
}

// resolveDesktopUser возвращает дом, uid и gid пользователя рабочего стола.
func resolveDesktopUser(configPath string) (home string, uid, gid int) {
	uid, gid = os.Getuid(), os.Getgid()

	// pkexec/sudo сообщают, от чьего имени их вызвали.
	for _, key := range []string{"PKEXEC_UID", "SUDO_UID"} {
		if raw := os.Getenv(key); raw != "" {
			if parsed, err := strconv.Atoi(raw); err == nil {
				if u, err := user.LookupId(strconv.Itoa(parsed)); err == nil {
					gid, _ := strconv.Atoi(u.Gid)
					return u.HomeDir, parsed, gid
				}
			}
		}
	}

	// Иначе смотрим, кому принадлежит файл конфигурации: его создаёт
	// оболочка от имени пользователя ещё до запуска backend'а.
	if configPath != "" {
		for _, path := range []string{configPath, filepath.Dir(configPath)} {
			info, err := os.Stat(path)
			if err != nil {
				continue
			}
			stat, ok := info.Sys().(*syscall.Stat_t)
			if !ok {
				continue
			}
			if u, err := user.LookupId(strconv.FormatUint(uint64(stat.Uid), 10)); err == nil {
				return u.HomeDir, int(stat.Uid), int(stat.Gid)
			}
		}
	}

	if u, err := user.Current(); err == nil {
		return u.HomeDir, uid, gid
	}
	return os.Getenv("HOME"), uid, gid
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
	if m.home == "" {
		return ""
	}
	return filepath.Join(m.home, ".config", "autostart", "awg-client.desktop")
}

// State сообщает UI, включён ли автозапуск и можно ли его вообще включить.
func (m *Manager) State() State {
	enabled := m.IsEnabled()

	switch {
	case m.home == "":
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
	if os.Getuid() != 0 || m.uid == 0 {
		return
	}
	if err := os.Chown(path, m.uid, m.gid); err != nil {
		fmt.Fprintf(os.Stderr, "autostart: не удалось сменить владельца %s: %v\n", path, err)
	}
}
