package appsteer

// Привязка к WinDivert 2.x (https://reqrypt.org/windivert.html) — драйверу,
// который отдаёт пакеты в пользовательский процесс и принимает их обратно.
// Библиотека WinDivert.dll и драйвер WinDivert64.sys лежат рядом с exe
// службы, как wintun.dll; драйвер ставится при первом открытии сам.
//
// Вызовы — через syscall, без cgo: служба собирается с CGO_ENABLED=0.
// Раскладка WINDIVERT_ADDRESS повторяет include/windivert.h.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	layerNetwork = 0

	// Биты поля после Timestamp: Layer:8, Event:8, Sniffed:1, Outbound:1,
	// Loopback:1, Impostor:1, IPv6:1, IPChecksum:1, TCPChecksum:1,
	// UDPChecksum:1.
	bitOutbound    = 1 << 17
	bitIPChecksum  = 1 << 21
	bitTCPChecksum = 1 << 22
	bitUDPChecksum = 1 << 23

	shutdownBoth = 3

	// mtuMax — WINDIVERT_MTU_MAX: больше пакета не бывает.
	mtuMax = 40 + 0xffff
)

// address — WINDIVERT_ADDRESS, 80 байт.
type address struct {
	Timestamp int64
	Bits      uint32
	Reserved2 uint32
	IfIdx     uint32 // WINDIVERT_DATA_NETWORK
	SubIfIdx  uint32
	_         [56]byte
}

// checksumsValid отмечает, что суммы в пакете посчитаны нами целиком.
func (a *address) checksumsValid() {
	a.Bits |= bitIPChecksum | bitTCPChecksum | bitUDPChecksum
}

var (
	dllOnce sync.Once
	dllErr  error

	procOpen, procRecv, procSend, procShutdown, procClose *windows.LazyProc
)

// loadDLL загружает WinDivert.dll строго из каталога программы: поиск по
// стандартным путям подсунул бы чужую библиотеку процессу с правами
// LocalSystem.
func loadDLL() error {
	dllOnce.Do(func() {
		exe, err := os.Executable()
		if err != nil {
			dllErr = err
			return
		}
		path := filepath.Join(filepath.Dir(exe), "WinDivert.dll")
		if _, err := os.Stat(path); err != nil {
			dllErr = fmt.Errorf("нет %s: %w", path, err)
			return
		}
		dll := windows.NewLazyDLL(path)
		procOpen = dll.NewProc("WinDivertOpen")
		procRecv = dll.NewProc("WinDivertRecv")
		procSend = dll.NewProc("WinDivertSend")
		procShutdown = dll.NewProc("WinDivertShutdown")
		procClose = dll.NewProc("WinDivertClose")
		dllErr = dll.Load()
	})
	return dllErr
}

// Available сообщает, можно ли перехватывать пакеты на этой машине.
func Available() error {
	return loadDLL()
}

// handle — открытый перехват.
type handle windows.Handle

// open открывает перехват сетевого уровня по фильтру WinDivert.
func open(filter string) (handle, error) {
	if err := loadDLL(); err != nil {
		return 0, err
	}
	cfilter, err := windows.BytePtrFromString(filter)
	if err != nil {
		return 0, err
	}
	r, _, callErr := procOpen.Call(uintptr(unsafe.Pointer(cfilter)), layerNetwork, 0, 0)
	if windows.Handle(r) == windows.InvalidHandle {
		return 0, fmt.Errorf("WinDivertOpen(%q): %w", filter, callErr)
	}
	return handle(r), nil
}

// recv ждёт следующий пакет. Ошибка после shutdown — штатный конец.
func (h handle) recv(buf []byte, addr *address) (int, error) {
	var n uint32
	r, _, err := procRecv.Call(uintptr(h), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)),
		uintptr(unsafe.Pointer(&n)), uintptr(unsafe.Pointer(addr)))
	if r == 0 {
		return 0, err
	}
	return int(n), nil
}

// send отправляет пакет — заново в стек, в направлении, указанном addr.
func (h handle) send(p []byte, addr *address) error {
	r, _, err := procSend.Call(uintptr(h), uintptr(unsafe.Pointer(&p[0])), uintptr(len(p)),
		0, uintptr(unsafe.Pointer(addr)))
	if r == 0 {
		return err
	}
	return nil
}

// close прекращает приём (ожидающий recv возвращается) и закрывает
// перехват. Пакеты в очереди драйвера уходят в стек как есть.
func (h handle) close() error {
	procShutdown.Call(uintptr(h), shutdownBoth)
	r, _, err := procClose.Call(uintptr(h))
	if r == 0 {
		return err
	}
	return nil
}

// isClosed — recv вернулся из-за закрытия перехвата.
func isClosed(err error) bool {
	return errors.Is(err, windows.ERROR_NO_DATA) ||
		errors.Is(err, windows.ERROR_INVALID_HANDLE) ||
		errors.Is(err, windows.ERROR_OPERATION_ABORTED)
}
