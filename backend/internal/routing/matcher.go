// Package routing содержит правила разделения трафика и их разбор.
//
// Пакет намеренно не умеет ничего делать с системой: он только отвечает на
// вопросы о правилах. Всё, что трогает таблицу маршрутизации, живёт в vpn.
package routing

import (
	"net/netip"
	"strings"

	"github.com/user/amnezia-web-client/internal/config"
)

// Matcher отвечает на вопрос «покрыто ли доменное имя правилами».
//
// Нужен маршрутизации по доменам и зонам: адреса таких правил заранее
// неизвестны, и решение принимается в момент ответа DNS.
//
// Нулевое значение пригодно к использованию и не совпадает ни с чем.
type Matcher struct {
	// domains — правила типа "domain". Совпадает само имя и его поддомены:
	// правило google.com должно покрывать и www.google.com, иначе
	// пользователю пришлось бы перечислять поддомены руками.
	domains []string

	// zones — правила типа "zone", приведённые к суффиксу без точки.
	zones []string
}

// NewMatcher собирает сопоставитель из правил. Правила других типов и
// выключенные правила игнорируются.
func NewMatcher(rules []config.RoutingRule) *Matcher {
	m := &Matcher{}

	for _, rule := range rules {
		if !rule.Enabled {
			continue
		}

		value := normalize(rule.Value)
		if value == "" {
			continue
		}

		switch rule.Type {
		case config.RuleTypeDomain:
			m.domains = append(m.domains, value)
		case config.RuleTypeZone:
			m.zones = append(m.zones, value)
		}
	}

	return m
}

// Empty сообщает, что сопоставлять не с чем. Позволяет вызывающему не
// поднимать DNS-посредника, когда правил по именам нет вовсе.
func (m *Matcher) Empty() bool {
	return m == nil || (len(m.domains) == 0 && len(m.zones) == 0)
}

// Match сообщает, подпадает ли имя хотя бы под одно правило.
func (m *Matcher) Match(name string) bool {
	if m == nil {
		return false
	}

	name = normalize(name)
	if name == "" {
		return false
	}

	for _, domain := range m.domains {
		if name == domain || strings.HasSuffix(name, "."+domain) {
			return true
		}
	}

	for _, zone := range m.zones {
		// Зона .ru покрывает yandex.ru, но не сам "ru" — доменом верхнего
		// уровня никто не пользуется напрямую, а совпадение по нему дало бы
		// ложные срабатывания на однословных внутренних именах.
		if strings.HasSuffix(name, "."+zone) {
			return true
		}
	}

	return false
}

// normalize приводит имя или значение правила к общему виду: нижний регистр,
// без завершающей точки и без ведущей — зону пользователь пишет как ".ru",
// а в имени из DNS её нет.
func normalize(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	value = strings.TrimSuffix(value, ".")
	value = strings.TrimPrefix(value, ".")
	return value
}

// PrefixOf возвращает адрес в виде одноадресного префикса — /32 для IPv4 и
// /128 для IPv6. Именно в таком виде адреса попадают в таблицу маршрутизации.
func PrefixOf(addr netip.Addr) netip.Prefix {
	return netip.PrefixFrom(addr, addr.BitLen())
}
