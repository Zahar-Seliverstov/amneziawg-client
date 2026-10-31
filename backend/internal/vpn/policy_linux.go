//go:build linux

package vpn

// Маршрутизация по политикам на Linux: ip rule, свои таблицы и метки.
//
// Порядок правил (меньше — раньше):
//
//	5200  fwmark tunnelMark  lookup main   пакеты самого туннеля — системным путём
//	5201  fwmark appsMark    lookup main   программы мимо туннеля («всё, кроме списка»)
//	      или                lookup 51822  программы в туннель («только список»)
//	5210  lookup main suppress_prefixlength 0
//	                                        точные системные маршруты выигрывают
//	                                        у нашего маршрута по умолчанию
//	5220  lookup 51821                      наши маршруты
//
// 5210 ставится, только когда в нашей таблице есть маршрут по умолчанию. В
// режиме «только список» он вреден: второй туннель с маршрутами /1 в main
// перебивал бы им наши /32 из списка.

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
)

const (
	routeTable = "51821"
	appsTable  = "51822"

	tunnelMark uint32 = 0x51820
	appsMark   uint32 = 0x51821

	prefTunnelMark = "5200"
	prefAppsMark   = "5201"
	prefSuppress   = "5210"
	prefTable      = "5220"

	// nftTable — метки соединений: живёт, пока стоят правила выбора таблиц.
	// nftAppsTable — метка программ: живёт, пока есть правила app:.
	nftTable     = "awg_client"
	nftAppsTable = "awg_client_apps"
)

var ourPrefs = []string{prefTunnelMark, prefAppsMark, prefSuppress, prefTable}

type linuxPolicy struct {
	mu sync.Mutex

	// up — стоят ли правила выбора таблиц.
	up bool

	// appsVia — куда сейчас ведёт правило программ: "main" или appsTable.
	// Пусто — правила нет.
	appsVia string

	apps *appWatcher
}

func newPolicyRouter() policyRouter {
	p := &linuxPolicy{apps: newAppWatcher(appsCgroup)}

	// Прошлый запуск мог умереть от SIGKILL и оставить правила: с ними весь
	// трафик уходил бы в пустую таблицу, а программы — в чужую cgroup.
	p.Down()
	p.apps.cleanupOrphans()
	_ = nft("delete", "table", "inet", nftTable)
	_ = nft("delete", "table", "inet", nftAppsTable)

	return p
}

func (p *linuxPolicy) Table() []string { return []string{"table", routeTable} }

func (p *linuxPolicy) TunnelMark() uint32 { return tunnelMark }

func (p *linuxPolicy) Up(full bool) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.removeRules(prefTunnelMark, prefSuppress, prefTable)

	if err := installMarkKeeper(); err != nil {
		return fmt.Errorf("nftables: %w", err)
	}

	for _, family := range []string{"-4", "-6"} {
		if err := ipRun(family, "rule", "add", "pref", prefTunnelMark,
			"fwmark", markHex(tunnelMark), "lookup", "main"); err != nil {
			return err
		}
		if full {
			if err := ipRun(family, "rule", "add", "pref", prefSuppress,
				"lookup", "main", "suppress_prefixlength", "0"); err != nil {
				return err
			}
		}
		if err := ipRun(family, "rule", "add", "pref", prefTable, "lookup", routeTable); err != nil {
			return err
		}
	}

	p.up = true
	return nil
}

func (p *linuxPolicy) Down() {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.removeRules(prefTunnelMark, prefSuppress, prefTable)
	for _, family := range []string{"-4", "-6"} {
		_ = ipRun(family, "route", "flush", "table", routeTable)
	}
	_ = nft("delete", "table", "inet", nftTable)
	p.up = false
}

// installMarkKeeper ставит перенос меток через conntrack.
//
// Метка есть только у исходящих пакетов: её ставит сокет туннеля (fwmark) или
// правило для программ. Ответы приходят без неё, и строгая проверка
// обратного пути (rp_filter=1 — так на многих системах по умолчанию) смотрит
// на маршрут без метки. А он ведёт в туннель: ответ сервера VPN, пришедший
// через Wi-Fi, отбрасывался бы как подделка, и туннель не поднимался бы
// вовсе. Поэтому метка запоминается в соединении на выходе и возвращается
// входящим пакетам на входе, а src_valid_mark велит ядру её учитывать. Так
// же делает wg-quick.
func installMarkKeeper() error {
	_ = nft("delete", "table", "inet", nftTable)

	marks := markHex(tunnelMark) + ", " + markHex(appsMark)
	script := fmt.Sprintf(`table inet %[1]s {
	chain postrouting {
		type filter hook postrouting priority mangle; policy accept;
		meta mark { %[2]s } ct mark set meta mark
	}
	chain prerouting {
		type filter hook prerouting priority mangle; policy accept;
		ct mark { %[2]s } meta mark set ct mark
	}
}
`, nftTable, marks)

	if err := nftScript(script); err != nil {
		return err
	}

	return os.WriteFile("/proc/sys/net/ipv4/conf/all/src_valid_mark", []byte("1"), 0o644)
}

// removeRules снимает наши правила с заданными приоритетами. Правило с одним
// приоритетом может оказаться не одно (остатки аварийного выхода), поэтому
// снимаем, пока снимается.
func (p *linuxPolicy) removeRules(prefs ...string) {
	for _, family := range []string{"-4", "-6"} {
		for _, pref := range prefs {
			for i := 0; i < 16; i++ {
				if ipRun(family, "rule", "del", "pref", pref) != nil {
					break
				}
			}
		}
	}
}

func (p *linuxPolicy) SetApps(names appSet, viaTunnel bool, ifname string, v6 bool) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if names.empty() {
		p.apps.stop()
		p.dropAppsRouting()
		return nil
	}

	via := "main"
	if viaTunnel {
		via = appsTable
	}

	if err := p.apps.ensureCgroup(); err != nil {
		return err
	}

	if via != p.appsVia {
		p.dropAppsRouting()
		if err := p.installAppsRouting(via, ifname, v6); err != nil {
			p.dropAppsRouting()
			return err
		}
	}

	p.apps.watch(names)
	return nil
}

// installAppsRouting ставит метку для программ и правило, куда её вести.
func (p *linuxPolicy) installAppsRouting(via, ifname string, v6 bool) error {
	// Метка ставится в хуке output, в цепочке типа route: смена метки там
	// заставляет ядро заново выбрать маршрут.
	// Ответам метку возвращает installMarkKeeper — она общая с туннелем.
	//
	// Подмена адреса обязательна. Адрес отправителя ядро выбирает при
	// открытии сокета, по маршруту без метки, — то есть адрес туннеля. Метка
	// уводит пакет в Wi-Fi уже с ним, и ответа на такой пакет не бывает:
	// программа видела бы, что ни одно соединение не устанавливается. В
	// режиме «только список» то же самое наоборот. masquerade ставит адрес
	// того интерфейса, куда пакет действительно ушёл.
	//
	// Программы с правилом app:имя/udp живут в отдельной cgroup, и метку
	// получает только их UDP.
	script := fmt.Sprintf(`table inet %[1]s {
	chain output {
		type route hook output priority mangle; policy accept;
		socket cgroupv2 level %[2]d "%[3]s" meta mark set %[6]s
		socket cgroupv2 level %[4]d "%[5]s" meta l4proto udp meta mark set %[6]s
	}
	chain nat {
		type nat hook postrouting priority srcnat; policy accept;
		meta mark %[6]s oifname != "lo" masquerade
	}
}
`, nftAppsTable, cgroupLevel(p.apps.all), p.apps.all, cgroupLevel(p.apps.udp), p.apps.udp, markHex(appsMark))

	if err := nftScript(script); err != nil {
		return fmt.Errorf("nftables: %w", err)
	}

	if via == appsTable {
		if err := ipRun("route", "replace", "0.0.0.0/0", "dev", ifname, "table", appsTable); err != nil {
			return err
		}
		if v6 {
			if err := ipRun("-6", "route", "replace", "::/0", "dev", ifname, "table", appsTable); err != nil {
				log.Printf("Программы в туннель только по IPv4: %v", err)
			}
		}
	}

	for _, family := range []string{"-4", "-6"} {
		if err := ipRun(family, "rule", "add", "pref", prefAppsMark,
			"fwmark", markHex(appsMark), "lookup", via); err != nil {
			return err
		}
	}

	p.appsVia = via
	return nil
}

func (p *linuxPolicy) dropAppsRouting() {
	p.removeRules(prefAppsMark)
	for _, family := range []string{"-4", "-6"} {
		_ = ipRun(family, "route", "flush", "table", appsTable)
	}
	_ = nft("delete", "table", "inet", nftAppsTable)
	p.appsVia = ""
}

// cgroupLevel — глубина cgroup от корня иерархии: её ждёт socket cgroupv2.
func cgroupLevel(path string) int {
	return strings.Count(path, "/") + 1
}

func markHex(mark uint32) string {
	return "0x" + strconv.FormatUint(uint64(mark), 16)
}

func ipRun(args ...string) error {
	out, err := exec.Command("ip", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("ip %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func nft(args ...string) error {
	out, err := exec.Command("nft", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("nft %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func nftScript(script string) error {
	cmd := exec.Command("nft", "-f", "-")
	cmd.Stdin = strings.NewReader(script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
