// Package routing содержит правила разделения трафика и их разбор.
//
// Пакет намеренно не умеет ничего делать с системой: он только отвечает на
// вопросы о правилах. Всё, что трогает таблицу маршрутизации, живёт в vpn.
package routing

import (
	"log"
	"net/netip"
	"regexp"
	"strings"

	"github.com/user/amnezia-web-client/internal/config"
	"github.com/user/amnezia-web-client/internal/geodata"
)

// Sites достаёт записи категории geosite. nil — базы нет, и правила geosite
// ни с чем не совпадают.
type Sites interface {
	Sites(tag string) ([]geodata.Domain, error)
}

// Matcher отвечает на вопрос «покрыто ли доменное имя правилами».
//
// Нужен маршрутизации по именам: адреса таких правил заранее неизвестны, и
// решение принимается в момент ответа DNS.
//
// Имена лежат в множествах, а не в списке: geosite:ru-blocked — это сотни
// тысяч записей, а спрашивают сопоставитель на каждый ответ DNS. Проверка
// стоит столько, сколько частей в имени, а не сколько правил в списке.
//
// Нулевое значение пригодно к использованию и не совпадает ни с чем.
type Matcher struct {
	// suffixes — имя и все его поддомены: правило google.com покрывает и
	// www.google.com, иначе поддомены пришлось бы перечислять руками.
	suffixes map[string]struct{}

	// zones — только поддомены: зона .ru покрывает yandex.ru, но не сам
	// «ru».
	zones map[string]struct{}

	// full — ровно эти имена.
	full map[string]struct{}

	keywords []string
	regexps  []*regexp.Regexp
}

// NewMatcher собирает сопоставитель из правил. Правила по адресам и
// выключенные правила игнорируются. sites может быть nil.
func NewMatcher(rules []config.RoutingRule, sites Sites) *Matcher {
	m := &Matcher{}

	for _, rule := range rules {
		if !rule.Enabled {
			continue
		}

		switch rule.Type {
		case config.RuleTypeDomain:
			m.addSuffix(rule.Value)
		case config.RuleTypeZone:
			if v := normalize(rule.Value); v != "" {
				m.zones = addTo(m.zones, v)
			}
		case config.RuleTypeFull:
			if v := normalize(rule.Value); v != "" {
				m.full = addTo(m.full, v)
			}
		case config.RuleTypeKeyword:
			m.addKeyword(rule.Value)
		case config.RuleTypeRegexp:
			m.addRegexp(rule.Value)
		case config.RuleTypeGeoSite:
			m.addSites(rule.Value, sites)
		}
	}

	return m
}

func (m *Matcher) addSuffix(value string) {
	if v := normalize(value); v != "" {
		m.suffixes = addTo(m.suffixes, v)
	}
}

func (m *Matcher) addKeyword(value string) {
	if v := strings.ToLower(strings.TrimSpace(value)); v != "" {
		m.keywords = append(m.keywords, v)
	}
}

func (m *Matcher) addRegexp(value string) {
	re, err := regexp.Compile(value)
	if err != nil {
		// Выражения из базы пишутся под движок v2ray, и часть из них Go
		// не понимает. Пропускаем одно, а не всю категорию.
		return
	}
	m.regexps = append(m.regexps, re)
}

func (m *Matcher) addSites(tag string, sites Sites) {
	if sites == nil {
		return
	}

	domains, err := sites.Sites(tag)
	if err != nil {
		log.Printf("Правило geosite:%s не действует: %v", tag, err)
		return
	}

	for _, d := range domains {
		switch d.Type {
		case geodata.DomainSuffix:
			m.addSuffix(d.Value)
		case geodata.DomainFull:
			if v := normalize(d.Value); v != "" {
				m.full = addTo(m.full, v)
			}
		case geodata.DomainKeyword:
			m.addKeyword(d.Value)
		case geodata.DomainRegexp:
			m.addRegexp(d.Value)
		}
	}
}

func addTo(set map[string]struct{}, v string) map[string]struct{} {
	if set == nil {
		set = make(map[string]struct{})
	}
	set[v] = struct{}{}
	return set
}

// Empty сообщает, что сопоставлять не с чем. Позволяет вызывающему не
// поднимать DNS-посредника, когда правил по именам нет вовсе.
func (m *Matcher) Empty() bool {
	return m == nil || (len(m.suffixes) == 0 && len(m.zones) == 0 && len(m.full) == 0 &&
		len(m.keywords) == 0 && len(m.regexps) == 0)
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

	if _, ok := m.full[name]; ok {
		return true
	}

	// Перебираем само имя и все его хвосты: www.google.com, google.com, com.
	for i := 0; ; {
		suffix := name[i:]
		if _, ok := m.suffixes[suffix]; ok {
			return true
		}
		// Зона совпадает только с хвостом, а не с именем целиком: доменом
		// верхнего уровня никто не пользуется напрямую, а совпадение по нему
		// дало бы ложные срабатывания на однословных внутренних именах.
		if i > 0 {
			if _, ok := m.zones[suffix]; ok {
				return true
			}
		}

		dot := strings.IndexByte(suffix, '.')
		if dot < 0 {
			break
		}
		i += dot + 1
	}

	for _, keyword := range m.keywords {
		if strings.Contains(name, keyword) {
			return true
		}
	}

	for _, re := range m.regexps {
		if re.MatchString(name) {
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
