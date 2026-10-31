package vpn

// Маршруты, поставленные по правилам маршрутизации, и их происхождение.
//
// Зачем отдельный учёт. installedRoutes — это устройство туннеля: пара
// /1-маршрутов, пришпиленный адрес сервера, AllowedIPs. Оно меняется только
// вместе с самим соединением. Маршруты правил живут иначе: пользователь
// правит список посреди работы, и снимать при этом надо ровно исчезнувшее.
//
// Отсюда и происхождение. Один адрес могут держать несколько правил — два
// имени на одном CDN, домен и его же адрес, вписанный отдельно, — и маршрут
// обязан пережить удаление любого из них, кроме последнего.

import (
	"log"
	"strings"

	"github.com/user/amnezia-web-client/internal/config"
	"github.com/user/amnezia-web-client/internal/iproute"
)

// ruleRoute — один маршрут, поставленный по правилам.
type ruleRoute struct {
	// args — хвост команды "ip route add" как есть. Запоминается целиком, а
	// не собирается заново при снятии: в режиме исключений он содержит путь
	// мимо туннеля, а путь меняется при переключении сети, и снятие по
	// новому не нашло бы записи.
	args []string

	// sources — ключи правил, из которых получился этот адрес.
	sources map[string]bool
}

// installRuleRoutes ставит маршруты правил и запоминает, кто их держит.
// Вызывать под routeMu.
func (m *Manager) installRuleRoutes(mode config.RoutingMode, addrs ruleAddresses) {
	if len(addrs.order) == 0 {
		return
	}

	if mode == config.RoutingModeVPNList {
		// В туннель уходит только перечисленное. Маршруты однотипны —
		// «префикс dev awg0», — поэтому ставятся одним запуском ip.
		routes := make([][]string, 0, len(addrs.order))
		for _, prefix := range addrs.order {
			routes = append(routes, m.tunnelRoute(prefix))
		}
		m.addRuleRoutes(addrs.order, routes, addrs.sources)
		return
	}

	// Режим исключений. Путь у каждого адреса свой и спрашивается у ядра
	// отдельно (см. bypass.go) — кроме наборов geoip: опрашивать ядро о
	// каждой из двадцати тысяч подсетей geoip:ru значило бы минуты ожидания.
	// Их ведём путём по умолчанию, одной пачкой. Чужие маршруты точнее
	// (сеть docker, второй VPN) этим не перебиваются: подсети наборов крупнее.
	var (
		bulkPrefixes []string
		bulkRoutes   [][]string
		defaults     = map[bool]*iproute.Path{}
	)

	// С маршрутизацией по политикам у каждого исключения один и тот же
	// маршрут — throw, «дальше решает система». Спрашивать ядро не о чем, и
	// всё ставится одной пачкой.
	if m.policy != nil {
		for _, prefix := range addrs.order {
			bulkPrefixes = append(bulkPrefixes, prefix)
			bulkRoutes = append(bulkRoutes, m.throwRoute(prefix))
		}
		m.addRuleRoutes(bulkPrefixes, bulkRoutes, addrs.sources)
		return
	}

	for _, prefix := range addrs.order {
		if addrs.bulk[prefix] {
			v6 := strings.Contains(prefix, ":")
			def, known := defaults[v6]
			if !known {
				path, err := m.ip.DefaultPathFor(prefix)
				if err != nil {
					log.Printf("Наборы geoip не выведены мимо туннеля (%s): %v", prefix, err)
				} else {
					def = &path
				}
				defaults[v6] = def
			}
			if def != nil {
				bulkPrefixes = append(bulkPrefixes, prefix)
				bulkRoutes = append(bulkRoutes, def.RouteArgs(prefix))
			}
			continue
		}

		args, needed, err := m.bypassRoute(prefix)
		if err != nil {
			log.Printf("Исключение %s не выведено мимо туннеля: %v", prefix, err)
			continue
		}

		if needed {
			if err := m.ip.AddRoute(args...); err != nil {
				log.Printf("Исключение %s не выведено мимо туннеля: %v", prefix, err)
				continue
			}
		} else {
			log.Printf("Исключение %s и так идёт мимо туннеля — маршрут не нужен", prefix)
			args = nil
		}

		m.rememberRuleRoute(prefix, args, addrs.sources[prefix])
	}

	m.addRuleRoutes(bulkPrefixes, bulkRoutes, addrs.sources)
}

// addRuleRoutes ставит маршруты пачкой и запоминает легшие. Отказы
// считаются, а в журнал идут только первые: у набора geoip их бывают
// сотни, и все они об одном. Вызывать под routeMu.
func (m *Manager) addRuleRoutes(prefixes []string, routes [][]string, sources map[string]map[string]bool) {
	if len(routes) == 0 {
		return
	}

	failed := 0
	for i, err := range m.ip.AddRoutes(routes) {
		if err != nil {
			if failed < 5 {
				log.Printf("Warning: failed to add route %s: %v", prefixes[i], err)
			}
			failed++
			continue
		}
		m.rememberRuleRoute(prefixes[i], routes[i], sources[prefixes[i]])
	}

	if failed > 5 {
		log.Printf("Не поставлено маршрутов: %d из %d", failed, len(routes))
	}
}

// rememberRuleRoute заносит маршрут в учёт или дописывает ему источники.
// Пустой args значит «адрес и так шёл куда надо, мы ничего не ставили»:
// помнить об этом нужно наравне с маршрутами, иначе снятие полезло бы убирать
// чужую запись. Вызывать под routeMu.
func (m *Manager) rememberRuleRoute(prefix string, args []string, sources map[string]bool) {
	if m.ruleRoutes == nil {
		m.ruleRoutes = make(map[string]*ruleRoute)
	}

	route, known := m.ruleRoutes[prefix]
	if !known {
		route = &ruleRoute{args: args, sources: make(map[string]bool)}
		m.ruleRoutes[prefix] = route
	}

	for key := range sources {
		route.sources[key] = true
	}
}

// dropRuleRoutes снимает маршруты правил, которых больше нет в списке.
//
// Держится маршрут не правилом, а всеми своими источниками разом: у адреса,
// за которым стоят два имени, удаление одного ничего не меняет. Вызывать под
// routeMu.
func (m *Manager) dropRuleRoutes(gone map[string]bool) {
	var (
		prefixes []string
		routes   [][]string
	)

	for prefix, route := range m.ruleRoutes {
		for key := range route.sources {
			if gone[key] {
				delete(route.sources, key)
			}
		}
		if len(route.sources) > 0 {
			continue
		}

		delete(m.ruleRoutes, prefix)

		// Пустой args — мы ничего не ставили, снимать нечего.
		if len(route.args) > 0 {
			prefixes = append(prefixes, prefix)
			routes = append(routes, route.args)
		}
	}

	if len(routes) == 0 {
		return
	}

	for i, err := range m.ip.DelRoutes(routes) {
		if err != nil {
			log.Printf("Маршрут %s не снят: %v", prefixes[i], err)
		}
	}
}

// flushRuleRoutes снимает все маршруты правил разом — при разборе туннеля и
// при полной пересборке. Вызывать под routeMu.
func (m *Manager) flushRuleRoutes() [][]string {
	if len(m.ruleRoutes) == 0 {
		return nil
	}

	routes := make([][]string, 0, len(m.ruleRoutes))
	for _, route := range m.ruleRoutes {
		if len(route.args) > 0 {
			routes = append(routes, route.args)
		}
	}
	m.ruleRoutes = nil

	return routes
}

// ruleKeys собирает ключи действующих правил. Выключенные не в счёт: маршрута
// у них нет, и от отсутствующих в списке они ничем не отличаются.
func ruleKeys(rules []config.RoutingRule) map[string]bool {
	keys := make(map[string]bool, len(rules))
	for _, rule := range rules {
		if !rule.Enabled {
			continue
		}
		keys[ruleKey(rule)] = true
	}
	return keys
}
