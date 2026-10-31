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
	"errors"
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

// minNameRouteLifetime — нижняя граница срока маршрута по имени.
//
// TTL записи говорит, когда пора переспросить DNS, а не когда адрес перестал
// использоваться. Браузер держит открытые соединения и кеш имён дольше TTL:
// у ВК записи живут 3–15 минут, и после уборки уже установленное прямое
// соединение начинало уходить в туннель с чужим адресом источника и
// повисало — песни обрывались, страница грузилась по минуте. Лишний
// маршрут на один адрес почти ничего не стоит, оборванное соединение — дорого.
const minNameRouteLifetime = 6 * time.Hour

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

	matcher := routing.NewMatcher(rc.Rules, m.sites())
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
		installer = newTunnelInstaller(m.ip, m.tunnelRoute)
	}

	set := routing.NewDynamicSet(installer)

	// Сопоставитель кладём в атомарный указатель, а не захватываем здесь:
	// правку правил по именам надо уметь применять под работающим
	// посредником, не пересоздавая его.
	m.nameMatcher.Store(matcher)

	proxy := dnsproxy.New(func(answer dnsproxy.Answer) {
		if !m.nameMatcher.Load().Match(answer.Name) {
			return
		}
		// Считаем взятые под учёт адреса, а не поставленные маршруты: часть
		// из них уже идёт куда надо и своего маршрута не требует.
		if n := set.Observe(answer.Name, answer.Addrs, max(answer.TTL, minNameRouteLifetime), time.Now()); n > 0 {
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

	m.nameMatcher.Store(nil)

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
//
// Как и bypassInstaller, помнит, какие маршруты поставил сам. Адрес домена
// часто уже заведён в туннель правилом, разрешённым при подключении; раньше
// такой ответ DNS считался неудачей, не брался под учёт и на каждом
// следующем ответе снова запускал ip — с записью «File exists» в журнал.
type tunnelInstaller struct {
	ip iproute.Tool
	// route собирает маршрут в туннель — с нашей таблицей, если она есть.
	route func(prefix string) []string

	mu    sync.Mutex
	owned map[netip.Prefix]bool
}

func newTunnelInstaller(ip iproute.Tool, route func(prefix string) []string) *tunnelInstaller {
	return &tunnelInstaller{ip: ip, route: route, owned: make(map[netip.Prefix]bool)}
}

func (t *tunnelInstaller) Add(p netip.Prefix) error {
	own := true
	if err := t.ip.AddRoute(t.route(p.String())...); err != nil {
		// Маршрут уже стоит: его поставило правило по адресу или домену.
		// Снимет его тот, кто ставил.
		if !errors.Is(err, iproute.ErrExists) {
			return err
		}
		own = false
	}

	t.mu.Lock()
	t.owned[p] = own
	t.mu.Unlock()
	return nil
}

func (t *tunnelInstaller) Remove(p netip.Prefix) error {
	t.mu.Lock()
	own := t.owned[p]
	delete(t.owned, p)
	t.mu.Unlock()

	if !own {
		return nil
	}
	return t.ip.DelRoute(t.route(p.String())...)
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
			// Такой маршрут уже стоит: адрес вывело правило по адресу или
			// домену, разрешённое заранее. Своим его не считаем — снимет его
			// тот, кто ставил.
			if !errors.Is(err, iproute.ErrExists) {
				return err
			}
			args = nil
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

// ruleResolveTimeout ограничивает разовый резолв одного доменного правила:
// без него молчащий сервер имён задерживал бы настройку маршрутов на десятки
// секунд.
//
// Полторы секунды, а не пять: это не последняя попытка узнать адреса имени, а
// первая. Не успевшее правило подхватит посредник DNS в тот момент, когда имя
// действительно спросят, — и подхватит адресом, который приложение как раз и
// получило. Пять секунд здесь оплачивались задержкой применения правил, а
// покупали лишь чуть более полный список на старте.
const ruleResolveTimeout = 1500 * time.Millisecond

// rulesResolveDeadline ограничивает резолв ВСЕГО набора правил.
//
// Отдельно от таймаута на имя, потому что параллельность его не заменяет:
// сотня молчащих имён при восьми потоках — это по-прежнему десяток заходов
// по полторы секунды подряд. Общий срок ставит предел тому, сколько времени
// применение правил вообще вправе занять.
const rulesResolveDeadline = 6 * time.Second

// resolveParallel — сколько имён спрашиваем разом. Столько же, сколько в
// rulesource: восемь одновременных вопросов сервер имён не смущают.
const resolveParallel = 8

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
