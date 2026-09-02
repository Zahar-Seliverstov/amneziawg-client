package main

import (
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/user/amnezia-web-client/internal/desktopuser"
)

// Права сокета — единственная проверка доступа к API: до него не должен
// дотянуться никто, кроме владельца.
func TestListenClosesSocketFromOthers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "api.sock")

	ln, err := listen(path, desktopuser.User{})
	if err != nil {
		t.Fatalf("сокет не открылся: %v", err)
	}
	defer ln.Close()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("сокет не создан: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("права сокета %o, ожидались 600", perm)
	}
	if info.Mode()&os.ModeSocket == 0 {
		t.Error("создан обычный файл вместо сокета")
	}
}

// Имя сокета переживает смерть процесса, поэтому повторный запуск обязан
// снять оставшийся файл — иначе клиент не поднимется больше никогда.
func TestListenClearsStaleSocket(t *testing.T) {
	path := filepath.Join(t.TempDir(), "api.sock")

	first, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("подготовка: %v", err)
	}
	// Закрытие через файловый дескриптор, без снятия имени: так выглядит
	// сокет процесса, убитого сигналом KILL.
	unix := first.(*net.UnixListener)
	unix.SetUnlinkOnClose(false)
	unix.Close()

	ln, err := listen(path, desktopuser.User{})
	if err != nil {
		t.Fatalf("устаревший сокет не снят: %v", err)
	}
	ln.Close()
}

// Живую службу не отбираем: молча занять её сокет значит оставить систему с
// поднятым туннелем, которым уже никто не управляет.
func TestListenRefusesLiveSocket(t *testing.T) {
	path := filepath.Join(t.TempDir(), "api.sock")

	live, err := listen(path, desktopuser.User{})
	if err != nil {
		t.Fatalf("подготовка: %v", err)
	}
	defer live.Close()

	go func() {
		for {
			conn, err := live.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()

	if ln, err := listen(path, desktopuser.User{}); err == nil {
		ln.Close()
		t.Fatal("второй запуск отобрал сокет у работающей службы")
	}
}

// Чужой файл под тем же именем сносить нельзя.
func TestListenRefusesRegularFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "api.sock")
	if err := os.WriteFile(path, []byte("не сокет"), 0600); err != nil {
		t.Fatalf("подготовка: %v", err)
	}

	if ln, err := listen(path, desktopuser.User{}); err == nil {
		ln.Close()
		t.Fatal("обычный файл был затёрт")
	}
}
