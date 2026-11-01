package vpn

// Серверы имён на Windows.
//
// Серверы имён принадлежат интерфейсу: они прописываются туннелю и исчезают
// вместе с ним. Чтобы система спрашивала только их, а не все адаптеры
// наперегонки, рядом ставится правило NRPT (nrpt_windows.go).

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

	all := append(append([]netip.Addr(nil), v4...), v6...)
	names := make([]string, len(all))
	for i, a := range all {
		names[i] = a.String()
	}
	if err := setNRPT(names); err != nil {
		// Без правила DNS работает, но наперегонки с роутером — правила по
		// именам станут ненадёжными. Соединение из-за этого не рвём.
		log.Printf("Внимание: %v — правила по именам могут срабатывать не всегда", err)
	}
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
	if err := removeNRPT(); err != nil {
		log.Printf("Не удалось снять правило DNS: %v", err)
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

// restoreOrphanedDNS снимает правило NRPT, оставшееся от аварийно
// завершённого запуска: оно указывает на адрес исчезнувшего туннеля и
// оставило бы систему без DNS.
func restoreOrphanedDNS() {
	if err := removeNRPT(); err != nil {
		log.Printf("Не удалось снять оставшееся правило DNS: %v", err)
	}
}

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
