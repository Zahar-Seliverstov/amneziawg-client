// Package rulesource определяет, из какого источника правило маршрутизации.
//
// Список правил быстро превращается в кашу: git.dtel.ru, bitrix.dtel.ru и
// 144.123.122.123 — на вид три разные записи, а на деле одна и та же
// организация. Источник — это то общее, по чему их можно свести вместе:
// регистрируемый домен.
//
// Для имён ответ известен сразу, для адресов — только у DNS: адрес относят к
// источнику, если его отдаёт какое-то из правил-имён или если он сам
// разрешается обратно в имя этого источника. Обращения к DNS кэшируются:
// список правил перечитывается после каждой правки, и спрашивать одно и то
// же по кругу незачем.
package rulesource

import (
	"context"
	"net"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/user/amnezia-web-client/internal/config"
)

// Lookup — то, что умеет спрашивать DNS. Интерфейс, а не *net.Resolver,
// потому что иначе тесты зависели бы от чужих серверов имён и от того, что
// сегодня отвечает конкретное имя.
type Lookup interface {
	// LookupHost возвращает адреса имени.
	LookupHost(ctx context.Context, host string) ([]string, error)
	// LookupAddr возвращает имена адреса (обратная запись PTR).
	LookupAddr(ctx context.Context, addr string) ([]string, error)
}

const (
	// lookupTimeout — предел на один вопрос к DNS. Ответ нужен, пока
	// пользователь смотрит на список: лучше показать правила без группировки,
	// чем задержать весь список ради одного молчащего сервера.
	lookupTimeout = 2 * time.Second

	// cacheTTL — сколько держим ответ. Адреса меняются медленнее, чем
	// пользователь правит список.
	cacheTTL = 10 * time.Minute

	// parallel — сколько вопросов задаём разом. Правил бывают десятки, а
	// каждый вопрос идёт по сети.
	parallel = 8

	// overall — предел на весь ответ. Полсотни адресов при молчащем DNS
	// растянулись бы на десятки секунд, а список правил всё это время стоял
	// бы без группировки. Что успели — то и отдаём, остальное подтянется при
	// следующем обновлении списка: ответы кэшируются.
	overall = 5 * time.Second
)

// Resolver отвечает, к какому источнику относится каждое правило.
// Безопасен для одновременного использования.
type Resolver struct {
	lookup Lookup
	now    func() time.Time

	mu    sync.Mutex
	cache map[string]entry
}

type entry struct {
	names []string
	at    time.Time
}

// New создаёт определитель поверх системного резолвера.
func New() *Resolver {
	return NewWith(defaultLookup{})
}

// NewWith создаёт определитель поверх заданного способа спрашивать DNS.
func NewWith(lookup Lookup) *Resolver {
	return &Resolver{lookup: lookup, now: time.Now, cache: map[string]entry{}}
}

type defaultLookup struct{}

func (defaultLookup) LookupHost(ctx context.Context, host string) ([]string, error) {
	return net.DefaultResolver.LookupHost(ctx, host)
}

func (defaultLookup) LookupAddr(ctx context.Context, addr string) ([]string, error) {
	return net.DefaultResolver.LookupAddr(ctx, addr)
}

// Sources сопоставляет идентификатору правила его источник. Правила, для
// которых источник неизвестен, в ответе отсутствуют: «не знаю» и «ни с чем не
// связано» для интерфейса — одно и то же, а придумывать источник нельзя.
func (r *Resolver) Sources(ctx context.Context, rules []config.RoutingRule) map[string]string {
	sources := make(map[string]string, len(rules))

	// Имена дают источник сразу, без сети.
	for _, rule := range rules {
		switch rule.Type {
		case config.RuleTypeDomain:
			if src := RegistrableDomain(rule.Value); src != "" {
				sources[rule.ID] = src
			}
		case config.RuleTypeZone:
			if src := strings.Trim(strings.ToLower(rule.Value), "."); src != "" {
				sources[rule.ID] = src
			}
		}
	}

	addresses := addressRules(rules)
	if len(addresses) == 0 {
		// Спрашивать DNS не о чем: сводить с именами нечего, а сами имена
		// разобраны выше. Список из одних имён — обычное дело, и гонять
		// ради него запросы в сеть незачем.
		return sources
	}

	ctx, cancel := context.WithTimeout(ctx, overall)
	defer cancel()

	// Адреса правил-имён: по ним адрес относится к источнику точно, а не по
	// догадке из обратной записи.
	byAddr := r.addressesOfNames(ctx, rules)

	var mu sync.Mutex
	r.eachInParallel(addresses, func(rule config.RoutingRule) {
		src := r.sourceOfAddress(ctx, rule, byAddr)
		if src == "" {
			return
		}

		mu.Lock()
		sources[rule.ID] = src
		mu.Unlock()
	})

	return sources
}

// addressesOfNames разрешает правила-имена и возвращает «адрес → источник».
func (r *Resolver) addressesOfNames(ctx context.Context, rules []config.RoutingRule) map[netip.Addr]string {
	byAddr := map[netip.Addr]string{}
	var mu sync.Mutex

	r.eachInParallel(nameRules(rules), func(rule config.RoutingRule) {
		src := RegistrableDomain(rule.Value)
		if src == "" {
			return
		}

		for _, host := range r.resolve(ctx, "host:"+strings.ToLower(rule.Value), func(c context.Context) ([]string, error) {
			return r.lookup.LookupHost(c, rule.Value)
		}) {
			addr, err := netip.ParseAddr(host)
			if err != nil {
				continue
			}

			mu.Lock()
			// Первый победил: если один адрес отдают два имени, менять
			// принадлежность при каждом обновлении списка нельзя — группы
			// прыгали бы у пользователя на глазах.
			if _, taken := byAddr[addr]; !taken {
				byAddr[addr] = src
			}
			mu.Unlock()
		}
	})

	return byAddr
}

// sourceOfAddress отвечает за одно правило с адресом или подсетью.
//
// byAddr к этому моменту собран целиком и дальше только читается, поэтому
// замок ему не нужен.
func (r *Resolver) sourceOfAddress(ctx context.Context, rule config.RoutingRule, byAddr map[netip.Addr]string) string {
	if rule.Type == config.RuleTypeCIDR {
		prefix, err := netip.ParsePrefix(rule.Value)
		if err != nil {
			return ""
		}

		// У подсети обратной записи нет, поэтому единственный признак —
		// адрес какого-то из правил-имён внутри неё.
		for addr, src := range byAddr {
			if prefix.Contains(addr) {
				return src
			}
		}
		return ""
	}

	addr, err := netip.ParseAddr(rule.Value)
	if err != nil {
		return ""
	}

	src, known := byAddr[addr]
	if known {
		return src
	}

	// Обратная запись — догадка послабее: имя в ней ставит владелец адреса, и
	// оно может не иметь отношения к сайту. Но чаще всего имеет, а другого
	// признака у одиночного адреса нет.
	for _, name := range r.resolve(ctx, "addr:"+addr.String(), func(c context.Context) ([]string, error) {
		return r.lookup.LookupAddr(c, addr.String())
	}) {
		if src := RegistrableDomain(name); src != "" {
			return src
		}
	}

	return ""
}

// resolve спрашивает DNS через кэш.
//
// Отказ и молчание сервера запоминаются наравне с ответом: иначе несуществующее
// имя опрашивалось бы заново при каждой перерисовке списка. А вот обрыв по
// нашему собственному сроку не запоминается — за него отвечает не DNS, и
// закрывать из-за него имя от группировки на десять минут неправильно.
func (r *Resolver) resolve(parent context.Context, key string, ask func(context.Context) ([]string, error)) []string {
	r.mu.Lock()
	if e, ok := r.cache[key]; ok && r.now().Sub(e.at) < cacheTTL {
		r.mu.Unlock()
		return e.names
	}
	r.mu.Unlock()

	ctx, cancel := context.WithTimeout(parent, lookupTimeout)
	defer cancel()

	names, err := ask(ctx)
	if err != nil {
		if parent.Err() != nil {
			return nil
		}
		names = nil
	}

	r.mu.Lock()
	r.cache[key] = entry{names: names, at: r.now()}
	r.mu.Unlock()

	return names
}

func (r *Resolver) eachInParallel(rules []config.RoutingRule, do func(config.RoutingRule)) {
	if len(rules) == 0 {
		return
	}

	var wg sync.WaitGroup
	slots := make(chan struct{}, parallel)

	for _, rule := range rules {
		wg.Add(1)
		slots <- struct{}{}

		go func(rule config.RoutingRule) {
			defer wg.Done()
			defer func() { <-slots }()
			do(rule)
		}(rule)
	}

	wg.Wait()
}

func nameRules(rules []config.RoutingRule) []config.RoutingRule {
	var out []config.RoutingRule
	for _, rule := range rules {
		if rule.Type == config.RuleTypeDomain {
			out = append(out, rule)
		}
	}
	return out
}

func addressRules(rules []config.RoutingRule) []config.RoutingRule {
	var out []config.RoutingRule
	for _, rule := range rules {
		if rule.Type == config.RuleTypeIP || rule.Type == config.RuleTypeCIDR {
			out = append(out, rule)
		}
	}
	return out
}
