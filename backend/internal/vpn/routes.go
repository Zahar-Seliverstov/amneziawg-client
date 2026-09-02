package vpn

// Куда уходит трафик, пока поднят туннель: маршруты, системные серверы имён
// и блокировка всего, что мимо.
//
// Собрано в одном месте, потому что содержание здесь — это порядок действий:
// сначала снять поставленное нами прежде, потом поставить новое, и всё под
// одним замком. Те же самые шаги повторяются на живом туннеле, когда правила
// меняют из интерфейса.

import (
	"context"
	"fmt"
	"log"
	"net"
	"strings"

	"github.com/user/amnezia-web-client/internal/config"
)

// configureTraffic перенаправляет трафик в туннель и переключает DNS.
//
// Вызывается ПОСЛЕ рукопожатия. Раньше маршруты ставились сразу, и мёртвый
// конфиг забирал весь трафик в туннель, который никуда не ведёт: пользователь
// оставался без интернета всё время ожидания. Рукопожатию маршруты не нужны —
// оно идёт по обычному пути.
//
// Та же самая функция пересобирает маршрутизацию на живом туннеле, когда
// правила поменяли из интерфейса: последовательность действий там ровно та
// же, а два её отдельных списка успели разъехаться.
func (m *Manager) configureTraffic(cfg *config.AmneziaWGConfig, routingCfg *config.RoutingConfig) error {
	m.routeMu.Lock()
	defer m.routeMu.Unlock()

	// Посредник имён пересобирается целиком: правила по именам могли
	// появиться, исчезнуть или поменять направление вместе с режимом.
	// При первичной настройке оба вызова — пустая работа.
	m.stopNameRouting()
	m.flushRoutes()

	if err := m.configureRouting(cfg, routingCfg); err != nil {
		return fmt.Errorf("не удалось настроить маршрутизацию: %w", err)
	}

	// Правила по доменам и зонам обслуживает посредник DNS: адреса для них
	// становятся известны только из ответов на запросы пользователя.
	// Не поднялся — работаем как раньше, с прямым DNS из конфига.
	servers := cfg.Interface.DNS
	if proxyAddr, ok := m.startNameRouting(cfg, routingCfg); ok {
		servers = []string{proxyAddr.String()}
	}

	if err := m.dns.Apply(m.ifname(), servers); err != nil {
		log.Printf("Warning: failed to configure DNS: %v", err)
	}

	// Блокировка взводится здесь же, вместе с маршрутами: с этого момента
	// трафик рассчитывает уйти в туннель, и с этого же момента его нельзя
	// выпускать мимо. Снимается она не здесь — см. killswitch.go.
	m.syncKillSwitch(cfg, routingCfg)

	return nil
}

// addRoute добавляет маршрут и запоминает его, если добавление удалось.
// Уже существующие чужие маршруты (RTNETLINK "File exists") не запоминаются,
// поэтому при пересборке мы их не удалим. Вызывать под routeMu.
func (m *Manager) addRoute(args ...string) error {
	if err := m.ip.AddRoute(args...); err != nil {
		return err
	}

	m.installedRoutes = append(m.installedRoutes, args)
	return nil
}

// flushRoutes снимает все маршруты, поставленные нами, в обратном порядке.
// Вызывать под routeMu.
func (m *Manager) flushRoutes() {
	for i := len(m.installedRoutes) - 1; i >= 0; i-- {
		m.ip.DelRoute(m.installedRoutes[i]...)
	}
	m.installedRoutes = nil
}

// ApplyRouting перестраивает маршруты на живом туннеле.
// Если туннель не поднят — ничего не делает: правила применятся при подключении.
func (m *Manager) ApplyRouting(routing *config.RoutingConfig) error {
	m.mu.RLock()
	state := m.status.State
	cfg := m.activeConfig
	m.mu.RUnlock()

	if state != StateConnected || cfg == nil {
		return nil
	}

	log.Printf("Re-applying routing rules on live tunnel")

	if err := m.configureTraffic(cfg, routing); err != nil {
		return fmt.Errorf("не удалось пересобрать маршруты: %w", err)
	}

	m.mu.Lock()
	m.routingConfig = routing
	m.mu.Unlock()

	m.notifyStatusChange()
	return nil
}

// configureRouting sets up routing based on configuration. Вызывать под routeMu.
func (m *Manager) configureRouting(cfg *config.AmneziaWGConfig, routing *config.RoutingConfig) error {
	ifname := m.ifname()

	// Правила по доменам резолвим через серверы имён из конфига НАПРЯМУЮ,
	// минуя системный резолвер.
	//
	// Через системный нельзя: пересборка правил на живом туннеле начинается с
	// остановки нашего же посредника DNS, а resolv.conf в этот момент всё ещё
	// указывает на него. Запрос уходил бы в мёртвый сокет, и правила по
	// доменам молча оставались без маршрутов — со стороны это выглядело как
	// «удалил одно правило, перестали работать все остальные».
	resolver := resolverFor(parseDNSServers(cfg.Interface.DNS))

	// Get the peer's allowed IPs
	var peerAllowedIPs []string
	for _, peer := range cfg.Peers {
		peerAllowedIPs = append(peerAllowedIPs, peer.AllowedIPs...)
	}

	if routing == nil || len(routing.Rules) == 0 {
		// Правил нет — маршрутизируем по AllowedIPs из конфига.
		//
		// AllowedIPs вида 0.0.0.0/0 нельзя добавлять напрямую: тогда в туннель
		// уйдут и сами зашифрованные пакеты к серверу VPN, получится петля —
		// туннель встаёт, пинг внутри него идёт, а интернет пропадает.
		// Полный туннель поднимаем через setupDefaultVPNRoute: он исключает
		// адрес сервера и вместо 0.0.0.0/0 ставит пару /1-маршрутов.
		if hasDefaultRoute(peerAllowedIPs) {
			return m.setupDefaultVPNRoute(cfg)
		}

		for _, ip := range peerAllowedIPs {
			if err := m.addRoute(ip, "dev", ifname); err != nil {
				log.Printf("Warning: failed to add route %s: %v", ip, err)
			}
		}
		return nil
	}

	// Apply routing rules based on mode
	switch routing.Mode {
	case config.RoutingModeVPNList:
		// Route only specified items through VPN
		for _, rule := range routing.Rules {
			if !rule.Enabled {
				continue
			}

			ips, err := m.resolveRoutingRule(rule, resolver)
			if err != nil {
				log.Printf("Warning: failed to resolve rule %s: %v", rule.Value, err)
				continue
			}

			for _, ip := range ips {
				if err := m.addRoute(ip, "dev", ifname); err != nil {
					log.Printf("Warning: failed to add route %s: %v", ip, err)
				}
			}
		}

	case config.RoutingModeDirectList:
		// Route all through VPN except specified items
		// First, set up default route through VPN
		if err := m.setupDefaultVPNRoute(cfg); err != nil {
			log.Printf("Warning: failed to setup default VPN route: %v", err)
		}

		// Then exclude specific routes
		for _, rule := range routing.Rules {
			if !rule.Enabled {
				continue
			}

			ips, err := m.resolveRoutingRule(rule, resolver)
			if err != nil {
				log.Printf("Warning: failed to resolve rule %s: %v", rule.Value, err)
				continue
			}

			// Эти адреса должны идти мимо туннеля — тем путём, каким система
			// шла к ним до подключения. Каким именно, решает bypassRoute:
			// шлюз по умолчанию тут не годится, см. bypass.go.
			for _, prefix := range ips {
				if err := m.addBypassRoute(prefix); err != nil {
					log.Printf("Исключение %s не выведено мимо туннеля: %v", prefix, err)
				}
			}
		}
	}

	return nil
}

// resolveRoutingRule resolves a routing rule to IP addresses/CIDRs
func (m *Manager) resolveRoutingRule(rule config.RoutingRule, resolver *net.Resolver) ([]string, error) {
	switch rule.Type {
	case config.RuleTypeIP:
		// Single IP - add /32 suffix if no mask
		if !strings.Contains(rule.Value, "/") {
			if strings.Contains(rule.Value, ":") {
				return []string{rule.Value + "/128"}, nil // IPv6
			}
			return []string{rule.Value + "/32"}, nil // IPv4
		}
		return []string{rule.Value}, nil

	case config.RuleTypeCIDR:
		return []string{rule.Value}, nil

	case config.RuleTypeDomain:
		// Разовый резолв нужен ради быстрого старта: маршрут появляется сразу
		// при подключении, не дожидаясь, пока пользователь откроет сайт.
		// Поддерживает же правило в актуальном состоянии посредник DNS
		// (nameroutes.go) — адреса за именем меняются, и одного резолва мало.
		ctx, cancel := context.WithTimeout(context.Background(), ruleResolveTimeout)
		defer cancel()

		ips, err := resolver.LookupIP(ctx, "ip", rule.Value)
		if err != nil {
			return nil, err
		}

		result := make([]string, 0, len(ips))
		for _, ip := range ips {
			if ip.To4() != nil {
				result = append(result, ip.String()+"/32")
			} else {
				result = append(result, ip.String()+"/128")
			}
		}
		return result, nil

	case config.RuleTypeZone:
		// У зоны нет заранее известных адресов: под .ru подпадают миллионы
		// имён, перечислить их невозможно. Такие правила целиком на
		// посреднике DNS — он ставит маршрут в тот момент, когда имя из зоны
		// действительно спросили. Здесь ставить нечего.
		return nil, nil

	default:
		return nil, fmt.Errorf("неизвестный тип правила: %s", rule.Type)
	}
}

// setupDefaultVPNRoute sets up routing all traffic through VPN
func (m *Manager) setupDefaultVPNRoute(cfg *config.AmneziaWGConfig) error {
	ifname := m.ifname()

	// Адрес сервера VPN должен идти В ОБХОД туннеля, иначе получится петля.
	// Endpoint может быть именем хоста — резолвим его, пока DNS ещё исправен.
	for _, peer := range cfg.Peers {
		if peer.Endpoint == "" {
			continue
		}

		host, _, err := net.SplitHostPort(peer.Endpoint)
		if err != nil || host == "" {
			return fmt.Errorf("не разобрать адрес сервера %q: %w", peer.Endpoint, err)
		}

		addrs, err := net.LookupIP(host)
		if err != nil {
			return fmt.Errorf("не удалось определить адрес сервера %q: %w", host, err)
		}

		// Исключаем адреса ОБЕИХ версий протокола. Раньше IPv6 пропускался
		// целиком, потому что шлюз спрашивался один раз и только для IPv4:
		// сервер, доступный лишь по IPv6, не исключался из туннеля, и
		// зашифрованные пакеты к нему уходили в сам туннель — петля, из-за
		// которой соединение не поднималось вовсе.
		excluded := 0
		for _, addr := range addrs {
			// Закрепляем ТОТ ЖЕ путь, которым система идёт к серверу сейчас:
			// через шлюз, по линку или вовсе через другой туннель. Шлюз по
			// умолчанию, который брался здесь раньше, — лишь частный случай,
			// и для сервера, доступного иначе, он уводил пакеты в никуда.
			path, err := m.ip.PathTo(addr.String())
			if err != nil {
				log.Printf("Адрес сервера %s не исключён: %v", addr, err)
				continue
			}

			// Адрес самой машины: локальная таблица просматривается раньше
			// всех, туннель до него не доберётся и без исключения.
			if path.Local {
				excluded++
				continue
			}

			// Путь уже ведёт в туннель — это петля, и закреплять её нельзя.
			if path.Device == ifname {
				log.Printf("Адрес сервера %s не исключён: путь к нему ведёт в сам туннель", addr)
				continue
			}

			if err := m.addRoute(path.RouteArgs(hostPrefix(addr))...); err != nil {
				// Маршрут мог уже существовать: его оставил аварийно
				// завершившийся прошлый запуск или другой клиент к тому же
				// серверу. Отказываться из-за этого нельзя — важно не кто
				// поставил маршрут, а что адрес сервера пришпилен мимо
				// туннеля. Раньше такой маршрут валил настройку целиком, и
				// клиент оставался подключённым, но без единого маршрута.
				if m.pinnedOutsideTunnel(hostPrefix(addr)) {
					log.Printf("Адрес сервера %s уже выведен мимо туннеля чужим маршрутом — оставляем как есть", addr)
					excluded++
					continue
				}

				log.Printf("Адрес сервера %s не исключён: %v", addr, err)
				continue
			}
			excluded++
		}

		// Ни одного исключения — дальше нельзя: маршруты ниже уведут в туннель
		// и сами пакеты к серверу. Получится петля, и вместо полного туннеля
		// пользователь останется без сети.
		if excluded == 0 {
			return fmt.Errorf("не удалось вывести сервер %s мимо туннеля: без этого весь трафик, включая пакеты к самому серверу, ушёл бы в петлю", host)
		}
	}

	// Две /1-подсети вместо 0.0.0.0/0: перекрывают весь адресный простор,
	// но не конфликтуют с существующим маршрутом по умолчанию.
	//
	// Отказ здесь — это отказ полного туннеля целиком, поэтому он ошибка, а
	// не предупреждение: молча оставить весь трафик вне VPN нельзя, ради него
	// пользователь и подключался.
	if err := m.addRoute("0.0.0.0/1", "dev", ifname); err != nil {
		return fmt.Errorf("не удалось направить трафик в туннель: %w", err)
	}
	if err := m.addRoute("128.0.0.0/1", "dev", ifname); err != nil {
		return fmt.Errorf("не удалось направить трафик в туннель: %w", err)
	}

	// IPv6 заворачиваем в туннель ТОЛЬКО если у интерфейса есть IPv6-адрес.
	// Иначе маршрут ведёт в никуда: браузер по AAAA-записи уходит в IPv6,
	// упирается в чёрную дыру и сайт не открывается, хотя IPv4 живой.
	if hasIPv6Address(cfg.Interface.Address) {
		for _, prefix := range []string{"::/1", "8000::/1"} {
			if err := m.addRoute(prefix, "dev", ifname); err != nil {
				log.Printf("Warning: failed to add IPv6 route %s: %v", prefix, err)
			}
		}
	}

	return nil
}

// hostPrefix превращает адрес в одноадресный префикс: /32 для IPv4 и /128
// для IPv6 — в таком виде адреса и попадают в таблицу маршрутизации.
func hostPrefix(ip net.IP) string {
	if ip4 := ip.To4(); ip4 != nil {
		return ip4.String() + "/32"
	}
	return ip.String() + "/128"
}

// hasDefaultRoute сообщает, покрывают ли AllowedIPs весь трафик.
func hasDefaultRoute(allowedIPs []string) bool {
	for _, ip := range allowedIPs {
		switch strings.TrimSpace(ip) {
		case "0.0.0.0/0", "::/0":
			return true
		}
	}
	return false
}

// hasIPv6Address сообщает, назначен ли интерфейсу IPv6-адрес.
func hasIPv6Address(addresses []string) bool {
	for _, addr := range addresses {
		host, _, err := net.ParseCIDR(strings.TrimSpace(addr))
		if err != nil {
			continue
		}
		if host.To4() == nil {
			return true
		}
	}
	return false
}
