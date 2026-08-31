// Package desktopuser определяет, от чьего имени работает рабочий стол.
//
// Backend поднимают через pkexec, поэтому сам он работает от root, а файлы
// создаёт в доме пользователя: ярлык автозапуска и файл с токеном доступа.
// Оставить их принадлежащими root значит сделать их неудаляемыми и
// нечитаемыми из обычной сессии — то есть сломать ровно то, ради чего их и
// создавали.
package desktopuser

import (
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"syscall"
)

// User — пользователь, за чьим рабочим столом сидят.
type User struct {
	Home string
	UID  int
	GID  int
}

// Resolve определяет пользователя рабочего стола.
//
// configPath нужен как подсказка: каталог настроек создаёт оболочка от имени
// пользователя ещё до запуска backend'а, поэтому его владелец — надёжный
// ответ там, где pkexec ничего не подсказал.
func Resolve(configPath string) User {
	current := User{Home: os.Getenv("HOME"), UID: os.Getuid(), GID: os.Getgid()}

	// pkexec и sudo сообщают, от чьего имени их вызвали.
	for _, key := range []string{"PKEXEC_UID", "SUDO_UID"} {
		raw := os.Getenv(key)
		if raw == "" {
			continue
		}

		uid, err := strconv.Atoi(raw)
		if err != nil {
			continue
		}
		if u, err := user.LookupId(strconv.Itoa(uid)); err == nil {
			gid, _ := strconv.Atoi(u.Gid)
			return User{Home: u.HomeDir, UID: uid, GID: gid}
		}
	}

	// Иначе смотрим, кому принадлежит файл настроек.
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
				return User{Home: u.HomeDir, UID: int(stat.Uid), GID: int(stat.Gid)}
			}
		}
	}

	if u, err := user.Current(); err == nil {
		current.Home = u.HomeDir
	}
	return current
}

// Own передаёт файл пользователю рабочего стола.
//
// Ничего не делает, когда backend и так работает от имени пользователя: в
// этом случае менять владельца не нужно и, как правило, нельзя.
func (u User) Own(path string) error {
	if os.Getuid() != 0 || u.UID == 0 {
		return nil
	}
	return os.Chown(path, u.UID, u.GID)
}
