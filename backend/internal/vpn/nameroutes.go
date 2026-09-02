package vpn

// Маршрутизация по доменам и зонам.
//
// Адреса таких правил заранее неизвестны: сайт живёт на CDN, отдаёт разные
// адреса разным клиентам и меняет их в течение дня. Разовый резолв при
// подключении устаревает через минуты — именно поэтому правила типа "зона"
// раньше не работали вовсе, а правила типа "домен" со временем начинали
// врать.
//
// Решение: встать посредником в DNS. Системный резолвер спрашивает нас, мы
// пересылаем вопрос серверу имён туннеля и попутно смотрим, какие адреса
// вернулись. Совпало с правилом — ставим маршрут ровно на тот адрес, который
// приложение сейчас и получило.

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/user/amnezia-web-client/internal/config"
	"github.com/user/amnezia-web-client/internal/dnsproxy"
	"github.com/user/amnezia-web-client/internal/iproute"
	"github.com/user/amnezia-web-client/internal/routing"
)

// sweepInterval — как часто снимаем маршруты с истёкшим сроком.
const sweepInterval = time.Minute

// startNameRouting поднимает посредника DNS, если среди правил есть хоть одно
// по имени. Возвращает адрес, который следует прописать системе как сервер
// имён, и признак того, что посредник действительно запущен.
//
// Любая неудача здесь не фатальна: возвращаем false, и вызывающий настраивает
// DNS напрямую, как раньше. Остаться без разрешения имён из-за необязательной
// функции нельзя — это оставило бы пользователя без интернета.
func (m *Manager) startNameRouting(cfg *config.AmneziaWGConfig, rc *config.RoutingConfig) (netip.Addr, bool) {
	if rc == nil {
		return netip.Addr{}, false
	}

	matcher := routing.NewMatcher(rc.Rules)
	if matcher.Empty() {
		return netip.Addr{}, false
	}

	upstream := parseDNSServers(cfg.Interface.DNS)
	if len(upstream) == 0 {
		log.Printf("Маршрутизация по именам не включена: в конфиге не задан DNS")
		return netip.Addr{}, false
	}

	listen, ok := tunnelAddr(cfg.Interface.Address)
	if !ok {
		log.Printf("Маршрутизация по именам не включена: у интерфейса нет адреса IPv4")
		return netip.Addr{}, false
	}

	// Куда ведут маршруты, зависит от режима. В режиме «только список через
	// VPN» совпавшее имя заворачиваем в туннель; в режиме «всё через VPN,
	// кроме списка» — наоборот, выводим мимо туннеля через обычный шлюз.
	var installer routing.Installer
	if rc.Mode == config.RoutingModeDirectList {
		installer = newBypassInstaller(m)
	} else {
		installer = tunnelInstaller{ip: m.ip, ifname: m.ifname()}
	}

	set := routing.NewDynamicSet(installer)

	proxy := dnsproxy.New(func(answer dnsproxy.Answer) {
		if !matcher.Match(answer.Name) {
			return
		}
		// Считаем взятые под учёт адреса, а не поставленные маршруты: часть
		// из них уже идёт куда надо и своего маршрута не требует.
		if n := set.Observe(answer.Addrs, answer.TTL, time.Now()); n > 0 {
			log.Printf("Правило по имени %s: адресов взято под учёт %d", answer.Name, n)
		}
	})

	if err := proxy.Start(netip.AddrPortFrom(listen, 53), upstream); err != nil {
		log.Printf("Маршрутизация по именам не включена: %v", err)
		return netip.Addr{}, false
	}

	stop := make(chan struct{})
	go sweepLoop(set, stop)

	m.mu.Lock()
	m.dnsProxy = proxy
	m.dynamicRoutes = set
	m.sweepStop = stop
	m.mu.Unlock()

	log.Printf("Маршрутизация по именам включена: слушаем %s, пересылаем на %v", listen, upstream)
	return listen, true
}

// stopNameRouting останавливает посредника и снимает все маршруты, которые он
// успел поставить. Безопасна, если ничего не запускалось.
func (m *Manager) stopNameRouting() {
	m.mu.Lock()
	proxy, set, stop := m.dnsProxy, m.dynamicRoutes, m.sweepStop
	m.dnsProxy, m.dynamicRoutes, m.sweepStop = nil, nil, nil
	m.mu.Unlock()

	if stop != nil {
		close(stop)
	}
	if proxy != nil {
		proxy.Stop()
	}
	// Маршруты снимаем после остановки посредника: иначе последний ответ
	// успел бы поставить маршрут уже после уборки.
	if set != nil {
		set.Clear()
	}
}

// sweepLoop периодически снимает просроченные маршруты.
func sweepLoop(set *routing.DynamicSet, stop <-chan struct{}) {
	ticker := time.NewTicker(sweepInterval)
	defer ticker.Stop()

	for {
		select {
		case <-stop:
			return
		case now := <-ticker.C:
			if n := set.Sweep(now); n > 0 {
				log.Printf("Снято просроченных маршрутов: %d", n)
			}
		}
	}
}

// tunnelInstaller заворачивает адрес в туннель.
type tunnelInstaller struct {
	ip     iproute.Tool
	ifname string
}

func (t tunnelInstaller) Add(p netip.Prefix) error {
	return t.ip.AddRoute(p.String(), "dev", t.ifname)
}

func (t tunnelInstaller) Remove(p netip.Prefix) error {
	return t.ip.DelRoute(p.String(), "dev", t.ifname)
}

// bypassInstaller выводит адрес мимо туннеля — тем путём, каким система шла
// бы к нему без VPN (см. bypass.go).
//
// Поставленный маршрут запоминается целиком. Спрашивать путь заново при
// снятии нельзя: он меняется при переключении сети (Wi-Fi на кабель, смена
// точки доступа), и удаление по новому пути не находит запись — маршрут
// остаётся в таблице навсегда, продолжая выводить трафик мимо туннеля уже
// после того, как правило удалили.
//
// Пустая запись значит «адрес и так шёл мимо туннеля, мы ничего не ставили».
// Помнить об этом нужно наравне с маршрутами: иначе на каждый ответ DNS с
// таким адресом клиент заново опрашивал бы ядро, а снятие пыталось бы убрать
// чужой маршрут.
type bypassInstaller struct {
	m *Manager

	mu        sync.Mutex
	installed map[netip.Prefix][]string
}

func newBypassInstaller(m *Manager) *bypassInstaller {
	return &bypassInstaller{m: m, installed: make(map[netip.Prefix][]string)}
}

func (b *bypassInstaller) Add(p netip.Prefix) error {
	args, needed, err := b.m.bypassRoute(p.String())
	if err != nil {
		return err
	}

	if needed {
		if err := b.m.ip.AddRoute(args...); err != nil {
			return err
		}
	}

	b.mu.Lock()
	b.installed[p] = args
	b.mu.Unlock()

	return nil
}

func (b *bypassInstaller) Remove(p netip.Prefix) error {
	b.mu.Lock()
	args, known := b.installed[p]
	delete(b.installed, p)
	b.mu.Unlock()

	if !known {
		return fmt.Errorf("маршрут %s не наш — снимать нечего", p)
	}
	if len(args) == 0 {
		return nil // Ничего не ставили — нечего и снимать.
	}
	return b.m.ip.DelRoute(args...)
}

// parseDNSServers превращает адреса из конфига в список серверов с портом 53.
// Нечитаемые значения пропускаются молча: конфиг пишет не наш код.
func parseDNSServers(values []string) []netip.AddrPort {
	servers := make([]netip.AddrPort, 0, len(values))

	for _, value := range values {
		addr, err := netip.ParseAddr(strings.TrimSpace(value))
		if err != nil {
			continue
		}
		servers = append(servers, netip.AddrPortFrom(addr, 53))
	}

	return servers
}

// tunnelAddr возвращает первый адрес IPv4, назначенный интерфейсу. Именно на
// нём слушает посредник: этот адрес заведомо наш, доступен только локально и
// исчезает вместе с туннелем.
func tunnelAddr(addresses []string) (netip.Addr, bool) {
	for _, value := range addresses {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(value))
		if err != nil {
			continue
		}
		if addr := prefix.Addr().Unmap(); addr.Is4() {
			return addr, true
		}
	}
	return netip.Addr{}, false
}

// ruleResolveTimeout ограничивает разовый резолв доменного правила: без него
// молчащий сервер имён задерживал бы настройку маршрутов на десятки секунд.
const ruleResolveTimeout = 5 * time.Second

// resolverFor возвращает резолвер, который спрашивает заданные серверы имён
// напрямую. Если серверов нет, остаётся системный.
//
// Принимает уже разобранные адреса, а не строки из конфига: разбор — забота
// parseDNSServers, и разделение позволяет проверить резолвер тестом, подсунув
// ему сервер на произвольном порту.
func resolverFor(addrs []netip.AddrPort) *net.Resolver {
	if len(addrs) == 0 {
		return net.DefaultResolver
	}

	return &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var dialer net.Dialer
			var lastErr error

			for _, server := range addrs {
				conn, err := dialer.DialContext(ctx, "udp", server.String())
				if err == nil {
					return conn, nil
				}
				lastErr = err
			}
			return nil, lastErr
		},
	}
}
