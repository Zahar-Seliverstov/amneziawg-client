package vpn

// Правка маршрутизации без полной пересборки.
//
// Полная пересборка стоит дорого и, главное, заметна: она гасит посредника
// DNS, а вместе с ним пропадают все выученные им маршруты. После каждой
// правки одного правила браузер заново учил адреса всех остальных — сайты
// на секунду отваливались, и виноватой выглядела правка.
//
// Здесь считается разница двух списков. Добавилось или исчезло правило по
// адресу — ставим или снимаем ровно его маршрут. Менялись правила по именам —
// подменяем сопоставитель под работающим посредником и снимаем маршруты
// только исчезнувших имён. Всё остальное — смена режима, первое применение,
// переподключение — по-прежнему полная пересборка: там меняется само
// устройство маршрутизации, а не список.

import (
	"log"

	"github.com/user/amnezia-web-client/internal/config"
	"github.com/user/amnezia-web-client/internal/routing"
	"github.com/user/amnezia-web-client/internal/tunnel"
)

// routingDelta — что поменялось между двумя списками правил.
type routingDelta struct {
	// added и removed — правила по адресам и именам, появившиеся и
	// исчезнувшие. Выключение правила считается исчезновением: маршрута у
	// выключенного нет, и от отсутствующего оно ничем не отличается.
	added   []config.RoutingRule
	removed map[string]bool

	// names — менялись правила по именам, и сопоставитель надо подменить.
	names bool
}

// planRoutingDelta решает, можно ли обойтись правкой вместо пересборки, и что
// именно править.
//
// ok=false означает «пересобрать целиком». Так отвечаем на всё, что меняет
// устройство маршрутизации, а не её содержание:
//
//   - режим: в одном маршруты ведут в туннель, в другом мимо него;
//   - переход между «правил нет» и «правила есть»: без правил маршрутизация
//     идёт по AllowedIPs из конфига, это совсем другая раскладка;
//   - появление или исчезновение правил по именам как таковых: вместе с ними
//     посредник DNS должен подняться или остановиться, а это и есть пересборка.
func planRoutingDelta(old, current *config.RoutingConfig) (routingDelta, bool) {
	if old == nil || current == nil {
		return routingDelta{}, false
	}
	if old.Mode != current.Mode {
		return routingDelta{}, false
	}
	if (len(old.Rules) == 0) != (len(current.Rules) == 0) {
		return routingDelta{}, false
	}

	// Посредник DNS поднимается под правила по именам и останавливается без
	// них. Появление первого такого правила и исчезновение последнего — это
	// его запуск и остановка, то есть пересборка.
	if hasNameRules(old.Rules) != hasNameRules(current.Rules) {
		return routingDelta{}, false
	}

	was := ruleKeys(old.Rules)
	now := ruleKeys(current.Rules)

	delta := routingDelta{removed: make(map[string]bool)}

	for _, rule := range current.Rules {
		if !rule.Enabled {
			continue
		}
		if !was[ruleKey(rule)] {
			delta.added = append(delta.added, rule)
			delta.names = delta.names || isNameRule(rule)
		}
	}

	for _, rule := range old.Rules {
		if !rule.Enabled {
			continue
		}
		key := ruleKey(rule)
		if !now[key] {
			delta.removed[key] = true
			delta.names = delta.names || isNameRule(rule)
		}
	}

	return delta, true
}

// needsNameRouting сообщает, есть ли среди правил хоть одно по имени, то есть
// нужен ли вообще посредник DNS.
func needsNameRouting(rc *config.RoutingConfig) bool {
	return rc != nil && hasNameRules(rc.Rules)
}

// hasNameRules сообщает, есть ли среди включённых правил хоть одно по имени.
func hasNameRules(rules []config.RoutingRule) bool {
	for _, rule := range rules {
		if rule.Enabled && isNameRule(rule) {
			return true
		}
	}
	return false
}

// isNameRule сообщает, обслуживается ли правило посредником DNS.
func isNameRule(rule config.RoutingRule) bool {
	return config.IsNameRule(rule.Type)
}

// empty сообщает, что править нечего: список правил переставили местами или
// поменяли что-то, на маршруты не влияющее.
func (d routingDelta) empty() bool {
	return len(d.added) == 0 && len(d.removed) == 0
}

// applyRoutingDelta правит маршрутизацию на живом туннеле, не трогая ни
// посредника DNS, ни resolv.conf, ни устройство туннеля.
func (m *Manager) applyRoutingDelta(spec *tunnel.Spec, rc *config.RoutingConfig, delta routingDelta) {
	m.routeMu.Lock()
	defer m.routeMu.Unlock()

	// Правила по именам подменяются под работающим посредником. Раньше это
	// было возможно только вместе с ним самим — с повторным bind на :53 и
	// потерей всего, что он успел выучить.
	if delta.names {
		matcher := routing.NewMatcher(rc.Rules, m.sites())
		m.nameMatcher.Store(matcher)

		m.mu.RLock()
		set := m.dynamicRoutes
		m.mu.RUnlock()

		if set != nil {
			// Уходят маршруты ровно тех имён, которые больше ни под одно
			// правило не подпадают. Адрес, за который держится соседнее
			// имя, остаётся на месте.
			if n := set.Retain(matcher.Match); n > 0 {
				log.Printf("Снято маршрутов исчезнувших имён: %d", n)
			}
		}
		// Новое имя могло уже лежать в кэше системы — тогда его ответ
		// прошёл бы мимо посредника, и маршрут для него не встал бы.
		m.flushDNSCache()
	}

	// Снимаем раньше, чем ставим: правило могли удалить и тут же добавить
	// заново в другом виде, и порядок «сначала новое» оставил бы адрес без
	// маршрута.
	if len(delta.removed) > 0 {
		m.dropRuleRoutes(delta.removed)
	}

	if len(delta.added) > 0 {
		// Спрашиваем только новые имена: остальные уже разрешены, и их
		// маршруты стоят.
		resolver := resolverFor(parseDNSServers(spec.DNS))
		m.installRuleRoutes(rc.Mode, m.resolveRules(delta.added, resolver, hasIPv6Address(spec.Addresses)))
	}

	m.syncApps(spec, rc)
}
