package vpn

// Серверы имён на Windows.
//
// Здесь не нужно ни подменять файлов, ни откатывать: серверы имён на Windows
// принадлежат интерфейсу. Они прописываются туннелю, а выбирает их система
// потому, что у туннеля наименьшая метрика (см. iproute.LinkUp). Вместе с
// интерфейсом исчезают и они — поэтому аварийное завершение тоже ничего за
// собой не оставляет.

import (
	"errors"
	"fmt"
	"log"
	"net/netip"
	"strings"
	"sync"

	"golang.org/x/sys/windows"
	"golang.zx2c4.com/wireguard/windows/tunnel/winipcfg"
)

// dnsControl прописывает серверы имён интерфейсу туннеля.
// Нулевое значение готово к использованию.
type dnsControl struct {
	mu     sync.Mutex
	ifname string
}

// Apply прописывает серверы имён. Повторный вызов заменяет прежние.
func (d *dnsControl) Apply(ifname string, servers []string) error {
	if len(servers) == 0 {
		return nil
	}

	var v4, v6 []netip.Addr
	for _, s := range servers {
		addr, err := netip.ParseAddr(strings.TrimSpace(s))
		if err != nil {
			return fmt.Errorf("сервер имён %q: %w", s, err)
		}
		if addr.Is4() {
			v4 = append(v4, addr)
		} else {
			v6 = append(v6, addr)
		}
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	luid, err := tunnelLUID(ifname)
	if err != nil {
		return err
	}

	// Семейство без серверов тоже записываем — пустым списком: иначе в нём
	// остались бы серверы от прошлого применения.
	errV4 := luid.SetDNS(windows.AF_INET, v4, nil)
	errV6 := luid.SetDNS(windows.AF_INET6, v6, nil)
	if len(v6) == 0 {
		// IPv6 на интерфейсе может не быть вовсе, а прописывать в него
		// нечего — его отказ здесь не ошибка.
		errV6 = nil
	}
	if err := errors.Join(errV4, errV6); err != nil {
		return fmt.Errorf("не удалось прописать серверы имён: %w", err)
	}

	d.ifname = ifname
	return nil
}

// Restore убирает серверы имён с туннеля. Интерфейс к этому моменту может
// уже исчезнуть — тогда убирать нечего.
func (d *dnsControl) Restore() {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.ifname == "" {
		return
	}
	ifname := d.ifname
	d.ifname = ""

	luid, err := tunnelLUID(ifname)
	if err != nil {
		return
	}
	for _, family := range []winipcfg.AddressFamily{windows.AF_INET, windows.AF_INET6} {
		if err := luid.FlushDNS(family); err != nil && family == windows.AF_INET {
			log.Printf("Не удалось убрать серверы имён с %s: %v", ifname, err)
		}
	}
}

// restoreOrphanedDNS на Windows не нужна: серверы имён исчезают вместе с
// интерфейсом туннеля, а он — вместе с процессом.
func restoreOrphanedDNS() {}

// tunnelLUID находит интерфейс туннеля по имени.
func tunnelLUID(ifname string) (winipcfg.LUID, error) {
	rows, err := winipcfg.GetIfTable2Ex(winipcfg.MibIfEntryNormalWithoutStatistics)
	if err != nil {
		return 0, err
	}
	for i := range rows {
		if strings.EqualFold(rows[i].Alias(), ifname) {
			return rows[i].InterfaceLUID, nil
		}
	}
	return 0, fmt.Errorf("интерфейс %q не найден", ifname)
}
