package vpn

import (
	"context"
	"fmt"
	"log"
	"net"
	"strings"
	"sync"

	"github.com/user/amnezia-web-client/internal/config"
)

// ruleAddresses — адреса, к которым свелись правила, и то, какие правила за
// каждым стоят.
//
// Порядок нужен ради предсказуемости: маршруты ложатся в том же порядке, в
// каком правила стоят в списке. Происхождение — ради инкрементальной правки:
// без него удаление правила нечем отличить от удаления соседнего, и
// оставалось бы только снести всё и собрать заново.
type ruleAddresses struct {
	order   []string
	sources map[string]map[string]bool

	// bulk — адреса, пришедшие только из наборов geoip. Их десятки тысяч,
	// и ставятся они пачкой, без опроса ядра о каждом (см. installRuleRoutes).
	bulk map[string]bool
}

// ruleKey — правило в виде, пригодном для сравнения наборов. Регистр не в
// счёт: «EXAMPLE.ORG» и «example.org» — одно правило.
func ruleKey(rule config.RoutingRule) string {
	return rule.Type + ":" + strings.ToLower(strings.TrimSpace(rule.Value))
}

// resolveRules превращает правила в адреса, спрашивая имена параллельно.
//
// Параллельно — потому что ожидание здесь чужое: сервер имён отвечает за
// десятки миллисекунд или не отвечает вовсе, и складывать эти ожидания в
// столбик незачем. Ширина та же, что в rulesource: восемь вопросов разом
// сервер имён не смущают, а сотня выглядела бы как перебор.
//
// Общий дедлайн один на весь набор. Без него сотня молчащих имён давала бы
// сотню таймаутов подряд даже при параллельной работе — восемь потоков лишь
// поделили бы это время на восемь.
//
// Порядок правил сохраняется, повторы отсеиваются: два имени вполне могут
// жить на одном адресе, и второй "ip route add" на него всё равно отказал бы.
// Оба имени при этом запоминаются — маршрут держится, пока жив хоть один их
// источник.
//
// v6 — есть ли у туннеля IPv6-адрес: без него подсети IPv6 из наборов не
// ставятся (см. geoPrefixes).
func (m *Manager) resolveRules(rules []config.RoutingRule, resolver *net.Resolver, v6 bool) ruleAddresses {
	ctx, cancel := context.WithTimeout(context.Background(), rulesResolveDeadline)
	defer cancel()

	resolved := make([][]string, len(rules))

	var wg sync.WaitGroup
	slots := make(chan struct{}, resolveParallel)

	for i, rule := range rules {
		if !rule.Enabled {
			continue
		}

		wg.Add(1)
		slots <- struct{}{}

		go func(i int, rule config.RoutingRule) {
			defer wg.Done()
			defer func() { <-slots }()

			ips, err := m.resolveRoutingRule(ctx, rule, resolver, v6)
			if err != nil {
				log.Printf("Warning: failed to resolve rule %s: %v", rule.Value, err)
				return
			}
			resolved[i] = ips
		}(i, rule)
	}

	wg.Wait()

	addrs := ruleAddresses{sources: make(map[string]map[string]bool), bulk: make(map[string]bool)}
	individual := make(map[string]bool)

	for i, ips := range resolved {
		key := ruleKey(rules[i])
		geo := rules[i].Type == config.RuleTypeGeoIP

		for _, prefix := range ips {
			if addrs.sources[prefix] == nil {
				addrs.sources[prefix] = make(map[string]bool)
				addrs.order = append(addrs.order, prefix)
			}
			addrs.sources[prefix][key] = true

			if geo && !individual[prefix] {
				addrs.bulk[prefix] = true
			} else {
				individual[prefix] = true
				delete(addrs.bulk, prefix)
			}
		}
	}

	return addrs
}

// resolveRoutingRule resolves a routing rule to IP addresses/CIDRs.
//
// Родительский контекст задаёт общий срок на весь набор правил, свой таймаут
// накладывается поверх: одно молчащее имя не должно съедать срок остальных.
func (m *Manager) resolveRoutingRule(ctx context.Context, rule config.RoutingRule, resolver *net.Resolver, v6 bool) ([]string, error) {
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
		ctx, cancel := context.WithTimeout(ctx, ruleResolveTimeout)
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

	case config.RuleTypeZone, config.RuleTypeFull, config.RuleTypeKeyword,
		config.RuleTypeRegexp, config.RuleTypeGeoSite:
		// У зоны и у наборов имён нет заранее известных адресов: под .ru
		// подпадают миллионы имён, перечислить их невозможно. Такие правила
		// целиком на посреднике DNS — он ставит маршрут в тот момент, когда
		// имя действительно спросили. Здесь ставить нечего.
		return nil, nil

	case config.RuleTypeGeoIP:
		return m.geoPrefixes(rule.Value, v6)

	case config.RuleTypeApp:
		// Программа узнаётся по процессу, а не по адресу (apps_linux.go).
		return nil, nil

	default:
		return nil, fmt.Errorf("неизвестный тип правила: %s", rule.Type)
	}
}
