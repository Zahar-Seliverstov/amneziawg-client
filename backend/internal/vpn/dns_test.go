package vpn

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// redirectResolvPaths переводит подмену DNS на временный каталог: настоящие
// пути ведут в /etc, куда тесту нельзя.
func redirectResolvPaths(t *testing.T) (conf, backup string) {
	t.Helper()

	dir := t.TempDir()
	conf = filepath.Join(dir, "resolv.conf")
	backup = filepath.Join(dir, "resolv.conf.awg-backup")

	oldConf, oldBackup := resolvConfPath, resolvBackupPath
	resolvConfPath, resolvBackupPath = conf, backup
	t.Cleanup(func() { resolvConfPath, resolvBackupPath = oldConf, oldBackup })

	return conf, backup
}

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("не прочитан %s: %v", path, err)
	}
	return string(data)
}

func TestResolvContent(t *testing.T) {
	got := resolvContent([]string{"10.8.0.1", "10.8.0.2"})

	if !strings.HasPrefix(got, resolvMarker) {
		t.Errorf("файл без метки своего происхождения:\n%s", got)
	}
	for _, want := range []string{"nameserver 10.8.0.1\n", "nameserver 10.8.0.2\n", resolvOptions} {
		if !strings.Contains(got, want) {
			t.Errorf("в файле нет %q:\n%s", want, got)
		}
	}
}

// Копия снимается один раз: правила маршрутизации меняются на живом туннеле,
// и повторная подмена не должна сохранять поверх оригинала уже наш файл.
func TestApplyViaFileBacksUpOriginalOnce(t *testing.T) {
	conf, backup := redirectResolvPaths(t)

	const original = "nameserver 192.168.1.1\n"
	if err := os.WriteFile(conf, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}

	if err := applyViaFile([]string{"10.8.0.1"}); err != nil {
		t.Fatalf("первая подмена: %v", err)
	}
	if err := applyViaFile([]string{"10.8.0.53"}); err != nil {
		t.Fatalf("повторная подмена: %v", err)
	}

	if got := read(t, backup); got != original {
		t.Errorf("оригинал затёрт нашим файлом:\n%s", got)
	}
	if got := read(t, conf); !strings.Contains(got, "nameserver 10.8.0.53") {
		t.Errorf("вторая подмена не применилась:\n%s", got)
	}

	if err := restoreResolvConf(); err != nil {
		t.Fatalf("откат: %v", err)
	}
	if got := read(t, conf); got != original {
		t.Errorf("после отката файл не тот:\n%s", got)
	}
	if _, err := os.Lstat(backup); !os.IsNotExist(err) {
		t.Errorf("копия осталась лежать: %v", err)
	}
}

// Систему, где resolv.conf не было вовсе, надо оставить такой же: свой файл
// указывает на посредник имён, исчезающий вместе с туннелем, и без уборки
// имена перестали бы разрешаться совсем.
func TestRestoreRemovesOwnFileWhenNothingWasBackedUp(t *testing.T) {
	conf, backup := redirectResolvPaths(t)

	if err := applyViaFile([]string{"10.8.0.1"}); err != nil {
		t.Fatalf("подмена: %v", err)
	}
	if _, err := os.Lstat(backup); !os.IsNotExist(err) {
		t.Fatalf("сохранять было нечего, а копия появилась: %v", err)
	}

	if err := restoreResolvConf(); err != nil {
		t.Fatalf("откат: %v", err)
	}
	if _, err := os.Lstat(conf); !os.IsNotExist(err) {
		t.Errorf("наш файл остался в системе: %v", err)
	}
}

// Чужой файл не трогаем: он мог появиться после нашей подмены — от диспетчера
// сети, например, — и удалить его значит сломать разрешение имён.
func TestRestoreKeepsForeignFile(t *testing.T) {
	conf, _ := redirectResolvPaths(t)

	const foreign = "nameserver 127.0.0.53\n"
	if err := os.WriteFile(conf, []byte(foreign), 0644); err != nil {
		t.Fatal(err)
	}

	if err := restoreResolvConf(); err != nil {
		t.Fatalf("откат: %v", err)
	}
	if got := read(t, conf); got != foreign {
		t.Errorf("чужой файл изменён:\n%s", got)
	}
}

// Прошлый запуск убили по SIGKILL — вернуть систему обязан следующий.
func TestRestoreOrphanedDNS(t *testing.T) {
	t.Run("копия осталась", func(t *testing.T) {
		conf, backup := redirectResolvPaths(t)

		const original = "nameserver 192.168.1.1\n"
		if err := os.WriteFile(conf, []byte(original), 0644); err != nil {
			t.Fatal(err)
		}
		if err := applyViaFile([]string{"10.8.0.1"}); err != nil {
			t.Fatal(err)
		}

		restoreOrphanedDNS()

		if got := read(t, conf); got != original {
			t.Errorf("прежние серверы не вернулись:\n%s", got)
		}
		if _, err := os.Lstat(backup); !os.IsNotExist(err) {
			t.Errorf("копия осталась лежать: %v", err)
		}
	})

	t.Run("копии нет, файл наш", func(t *testing.T) {
		conf, _ := redirectResolvPaths(t)

		if err := applyViaFile([]string{"10.8.0.1"}); err != nil {
			t.Fatal(err)
		}

		restoreOrphanedDNS()

		if _, err := os.Lstat(conf); !os.IsNotExist(err) {
			t.Errorf("наш файл пережил уборку: %v", err)
		}
	})

	t.Run("подменять было нечего", func(t *testing.T) {
		conf, _ := redirectResolvPaths(t)

		const foreign = "nameserver 127.0.0.53\n"
		if err := os.WriteFile(conf, []byte(foreign), 0644); err != nil {
			t.Fatal(err)
		}

		restoreOrphanedDNS()

		if got := read(t, conf); got != foreign {
			t.Errorf("тронут файл, которого мы не создавали:\n%s", got)
		}
	})
}

func TestWriteSystemFileAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "resolv.conf")

	if err := writeSystemFileAtomic(path, []byte("первый\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := writeSystemFileAtomic(path, []byte("второй\n"), 0644); err != nil {
		t.Fatal(err)
	}

	if got := read(t, path); got != "второй\n" {
		t.Errorf("перезапись не сработала: %q", got)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0644 {
		t.Errorf("права %o вместо 0644: системный резолвер читает файл не от root", perm)
	}

	// Временных огрызков после удачной записи оставаться не должно: рядом с
	// resolv.conf они сбивают с толку и живут до перезагрузки.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		names := []string{}
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("в каталоге осталось лишнее: %v", names)
	}
}
