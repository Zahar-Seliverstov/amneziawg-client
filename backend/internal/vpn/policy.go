package vpn

// Маршрутизация по политикам: свои маршруты — в своей таблице.
//
// Зачем. Пока маршруты туннеля жили в общей таблице main, «мимо VPN»
// приходилось выражать маршрутом с конкретным путём, а путь брался из
// маршрута по умолчанию — то есть домашний роутер. Если поверх работал другой
// туннель (второй VPN, TUN прокси), исключения его молча обходили: правило
// «напрямую» уводило трафик туда, куда пользователь его не посылал.
//
// Здесь иначе. Наши маршруты стоят в отдельной таблице, а системная остаётся
// нетронутой. «Мимо VPN» — это маршрут throw: просмотр нашей таблицы
// заканчивается, и пакет идёт дальше по системной, как шёл бы без туннеля, —
// к роутеру, во второй VPN или куда там его вела система. Путь не угадывается
// и не застывает в момент подключения.
//
// Пока что это есть только на Linux. На Windows policyRouter — nil, и
// менеджер работает по-прежнему, маршрутами в общей таблице.

import (
	"log"
	"strings"

	"github.com/user/amnezia-web-client/internal/config"
)

// policyRouter — маршрутизация по политикам и правила для программ.
type policyRouter interface {
	// Table — хвост команды "ip route add", кладущий маршрут в нашу таблицу.
	Table() []string

	// TunnelMark — метка, которой ядро туннеля помечает свои зашифрованные
	// пакеты. Помеченное идёт системной таблицей: без этого маршрут по
	// умолчанию в нашей таблице увёл бы в туннель и пакеты к самому серверу.
	TunnelMark() uint32

	// Up ставит правила выбора таблиц. full — в нашей таблице стоит маршрут
	// по умолчанию, и тогда более точные системные маршруты (своя сеть,
	// docker, подсети второго VPN) должны выигрывать у него.
	Up(full bool) error

	// Down снимает правила и опустошает наши таблицы. Безопасна, если ничего
	// не ставилось: ею же убирается оставшееся после аварийного выхода.
	Down()

	// SetApps задаёт программы, чьи соединения идут не так, как остальное:
	// в режиме «всё, кроме списка» — мимо туннеля, в режиме «только список» —
	// в туннель. У программ из udp так идёт только UDP, остальное — общим
	// путём. Пустые списки возвращают все взятые программы на место.
	SetApps(apps appSet, viaTunnel bool, ifname string, v6 bool) error
}

// tableArgs — хвост маршрута, кладущий его в нашу таблицу. Пусто без
// маршрутизации по политикам: тогда маршрут ложится в main, как раньше.
func (m *Manager) tableArgs() []string {
	if m.policy == nil {
		return nil
	}
	return m.policy.Table()
}

// tunnelRoute — маршрут, заворачивающий префикс в туннель.
func (m *Manager) tunnelRoute(prefix string) []string {
	return append([]string{prefix, "dev", m.ifname()}, m.tableArgs()...)
}

// throwRoute — маршрут, выводящий префикс из нашей таблицы: дальше его ведёт
// система, тем путём, каким вела бы без туннеля.
func (m *Manager) throwRoute(prefix string) []string {
	return append([]string{"throw", prefix}, m.tableArgs()...)
}

// fullTunnel сообщает, ляжет ли в нашу таблицу маршрут по умолчанию.
func fullTunnel(cfg *config.AmneziaWGConfig, rc *config.RoutingConfig) bool {
	if rc == nil || len(rc.Rules) == 0 {
		var allowed []string
		for _, peer := range cfg.Peers {
			allowed = append(allowed, peer.AllowedIPs...)
		}
		return hasDefaultRoute(allowed)
	}
	return rc.Mode == config.RoutingModeDirectList
}

// appSet — имена программ из правил app:: all — все соединения программы,
// udp — только UDP (app:vesktop/udp).
type appSet struct {
	all, udp []string
}

func (s appSet) empty() bool { return len(s.all) == 0 && len(s.udp) == 0 }

func (s appSet) String() string {
	names := append([]string(nil), s.all...)
	for _, name := range s.udp {
		names = append(names, name+config.AppUDPSuffix)
	}
	return strings.Join(names, ", ")
}

// appNames — имена программ из включённых правил app:.
func appNames(rc *config.RoutingConfig) appSet {
	var set appSet
	if rc == nil {
		return set
	}
	for _, rule := range rc.Rules {
		if !rule.Enabled || rule.Type != config.RuleTypeApp {
			continue
		}
		if name, udp := config.SplitApp(strings.ToLower(rule.Value)); udp {
			set.udp = append(set.udp, name)
		} else {
			set.all = append(set.all, name)
		}
	}
	return set
}

// syncApps приводит набор взятых программ в соответствие с правилами.
// Вызывать под routeMu.
func (m *Manager) syncApps(cfg *config.AmneziaWGConfig, rc *config.RoutingConfig) {
	names := appNames(rc)

	if m.policy == nil {
		if !names.empty() {
			log.Printf("Правила для программ (%s) на этой системе не поддерживаются и не действуют", names)
		}
		return
	}

	viaTunnel := rc != nil && rc.Mode == config.RoutingModeVPNList
	if err := m.policy.SetApps(names, viaTunnel, m.ifname(), hasIPv6Address(cfg.Interface.Address)); err != nil {
		log.Printf("Правила для программ не применены: %v", err)
	}
}

// releaseApps возвращает все взятые программы на место. Вызывать под routeMu.
func (m *Manager) releaseApps() {
	if m.policy == nil {
		return
	}
	if err := m.policy.SetApps(appSet{}, false, "", false); err != nil {
		log.Printf("Программы не возвращены на место: %v", err)
	}
}
