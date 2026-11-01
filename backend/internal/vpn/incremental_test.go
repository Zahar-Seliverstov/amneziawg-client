package vpn

import (
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/user/amnezia-web-client/internal/config"
	"github.com/user/amnezia-web-client/internal/routing"
	"github.com/user/amnezia-web-client/internal/tunnel"
)

func rule(t config.RuleType, value string) config.RoutingRule {
	return config.RoutingRule{ID: value, Type: t, Value: value, Enabled: true}
}

func routingWith(mode config.RoutingMode, rules ...config.RoutingRule) *config.RoutingConfig {
	return &config.RoutingConfig{Mode: mode, Rules: rules}
}

// Полная пересборка гасит посредника DNS и стоит пользователю всех выученных
// маршрутов. Она уместна там, где меняется устройство маршрутизации, а не её
// содержание, — и только там.
func TestFullRebuildOnlyWhenLayoutChanges(t *testing.T) {
	vpnList := config.RoutingModeVPNList
	direct := config.RoutingModeDirectList

	cases := []struct {
		name        string
		old, now    *config.RoutingConfig
		incremental bool
	}{
		{
			name: "первое применение — сравнивать не с чем",
			old:  nil,
			now:  routingWith(vpnList, rule(config.RuleTypeIP, "1.1.1.1")),
		},
		{
			name: "смена режима: маршруты ведут в другую сторону",
			old:  routingWith(vpnList, rule(config.RuleTypeIP, "1.1.1.1")),
			now:  routingWith(direct, rule(config.RuleTypeIP, "1.1.1.1")),
		},
		{
			name: "правил не было вовсе — маршрутизация шла по AllowedIPs",
			old:  routingWith(vpnList),
			now:  routingWith(vpnList, rule(config.RuleTypeIP, "1.1.1.1")),
		},
		{
			name: "удалили последнее правило — возврат к AllowedIPs",
			old:  routingWith(vpnList, rule(config.RuleTypeIP, "1.1.1.1")),
			now:  routingWith(vpnList),
		},
		{
			name: "появилось первое правило по имени — посредник DNS должен подняться",
			old:  routingWith(vpnList, rule(config.RuleTypeIP, "1.1.1.1")),
			now: routingWith(vpnList,
				rule(config.RuleTypeIP, "1.1.1.1"),
				rule(config.RuleTypeDomain, "example.org")),
		},
		{
			name: "исчезло последнее правило по имени — посредник должен остановиться",
			old: routingWith(vpnList,
				rule(config.RuleTypeIP, "1.1.1.1"),
				rule(config.RuleTypeDomain, "example.org")),
			now: routingWith(vpnList, rule(config.RuleTypeIP, "1.1.1.1")),
		},
		{
			name: "добавили адрес — правка",
			old:  routingWith(vpnList, rule(config.RuleTypeIP, "1.1.1.1")),
			now: routingWith(vpnList,
				rule(config.RuleTypeIP, "1.1.1.1"),
				rule(config.RuleTypeCIDR, "10.0.0.0/8")),
			incremental: true,
		},
		{
			name: "добавили имя к уже имеющимся — правка",
			old:  routingWith(vpnList, rule(config.RuleTypeDomain, "example.org")),
			now: routingWith(vpnList,
				rule(config.RuleTypeDomain, "example.org"),
				rule(config.RuleTypeDomain, "example.com")),
			incremental: true,
		},
		{
			name: "удалили одно имя из нескольких — правка",
			old: routingWith(vpnList,
				rule(config.RuleTypeDomain, "example.org"),
				rule(config.RuleTypeZone, ".ru")),
			now:         routingWith(vpnList, rule(config.RuleTypeZone, ".ru")),
			incremental: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, ok := planRoutingDelta(tc.old, tc.now); ok != tc.incremental {
				t.Errorf("правка=%v, ожидалось %v", ok, tc.incremental)
			}
		})
	}
}

// Выключение правила равносильно его удалению: маршрута у выключенного нет.
func TestDisablingRuleCountsAsRemoval(t *testing.T) {
	old := routingWith(config.RoutingModeVPNList,
		rule(config.RuleTypeIP, "1.1.1.1"),
		rule(config.RuleTypeDomain, "example.org"))

	off := old.Clone()
	off.Rules[0].Enabled = false

	delta, ok := planRoutingDelta(old, &off)
	if !ok {
		t.Fatal("выключение правила потребовало полной пересборки")
	}
	if len(delta.added) != 0 {
		t.Errorf("что-то добавилось: %+v", delta.added)
	}
	if !delta.removed["ip:1.1.1.1"] {
		t.Errorf("выключенное правило не считается исчезнувшим: %+v", delta.removed)
	}
	if delta.names {
		t.Error("правила по именам не менялись, а сопоставитель собрались подменять")
	}
}

// Регистр не в счёт: «EXAMPLE.ORG» и «example.org» — одно правило, и пересобирать
// из-за такой правки нечего.
func TestCaseOnlyChangeIsNotAChange(t *testing.T) {
	old := routingWith(config.RoutingModeVPNList, rule(config.RuleTypeDomain, "example.org"))
	now := routingWith(config.RoutingModeVPNList, rule(config.RuleTypeDomain, "EXAMPLE.ORG"))

	delta, ok := planRoutingDelta(old, now)
	if !ok {
		t.Fatal("смена регистра потребовала полной пересборки")
	}
	if !delta.empty() {
		t.Errorf("смена регистра принята за правку: +%v -%v", delta.added, delta.removed)
	}
}

// Ради этого всё и делалось: правка списка не должна стоить пользователю
// маршрутов, которые посредник DNS уже выучил для остальных правил.
func TestDeltaKeepsLearnedRoutesOfSurvivingNames(t *testing.T) {
	ip := &fakeIP{}
	m := managerWith(ip)
	m.notify = make(chan ConnectionStatus, 8)

	// Посредник уже поработал: под каждое имя выучен свой адрес.
	installer := newTunnelInstaller(ip, m.tunnelRoute)
	set := routing.NewDynamicSet(installer)
	set.Observe("example.org", []netip.Addr{netip.MustParseAddr("1.2.3.4")}, time.Hour, time.Now())
	set.Observe("example.com", []netip.Addr{netip.MustParseAddr("5.6.7.8")}, time.Hour, time.Now())
	m.dynamicRoutes = set

	old := routingWith(config.RoutingModeVPNList,
		rule(config.RuleTypeDomain, "example.org"),
		rule(config.RuleTypeDomain, "example.com"))
	now := routingWith(config.RoutingModeVPNList, rule(config.RuleTypeDomain, "example.org"))

	delta, ok := planRoutingDelta(old, now)
	if !ok {
		t.Fatal("удаление одного имени из двух потребовало пересборки")
	}

	m.applyRoutingDelta(&tunnel.Spec{}, now, delta)

	if set.Len() != 1 {
		t.Errorf("на учёте %d маршрутов, ожидался один", set.Len())
	}

	// Ушёл ровно адрес удалённого имени.
	deleted := strings.Join(ipJoined(ip.deleted), "|")
	if !strings.Contains(deleted, "5.6.7.8/32") {
		t.Errorf("адрес удалённого имени не снят: %v", ip.deleted)
	}
	if strings.Contains(deleted, "1.2.3.4/32") {
		t.Errorf("снят адрес имени, которое осталось в списке: %v", ip.deleted)
	}

	// И сопоставитель под работающим посредником уже новый.
	if m.nameMatcher.Load().Match("example.com") {
		t.Error("посредник всё ещё считает удалённое имя своим")
	}
	if !m.nameMatcher.Load().Match("example.org") {
		t.Error("посредник перестал узнавать оставшееся имя")
	}
}

// Добавленный адрес ставится сам по себе, а уже стоящие маршруты не трогаются
// вовсе: ни снятия, ни повторной установки.
func TestDeltaAddsOnlyTheNewAddress(t *testing.T) {
	ip := &fakeIP{}
	m := managerWith(ip)
	m.notify = make(chan ConnectionStatus, 8)
	m.rememberRuleRoute("1.1.1.1/32", []string{"1.1.1.1/32", "dev", "awg0"},
		map[string]bool{"ip:1.1.1.1": true})

	old := routingWith(config.RoutingModeVPNList, rule(config.RuleTypeIP, "1.1.1.1"))
	now := routingWith(config.RoutingModeVPNList,
		rule(config.RuleTypeIP, "1.1.1.1"),
		rule(config.RuleTypeCIDR, "10.0.0.0/8"))

	delta, ok := planRoutingDelta(old, now)
	if !ok {
		t.Fatal("добавление подсети потребовало пересборки")
	}

	m.applyRoutingDelta(&tunnel.Spec{}, now, delta)

	if got := ip.joinedAdded(); len(got) != 1 || got[0] != "10.0.0.0/8 dev awg0" {
		t.Errorf("поставлено %v, ожидался только новый маршрут", got)
	}
	if len(ip.deleted) != 0 {
		t.Errorf("снимались маршруты: %v", ip.deleted)
	}
	if len(m.ruleRoutes) != 2 {
		t.Errorf("в учёте %d маршрутов, ожидалось 2", len(m.ruleRoutes))
	}
}

// Адрес, за который держатся два правила, переживает удаление одного из них.
func TestDeltaKeepsAddressHeldByAnotherRule(t *testing.T) {
	ip := &fakeIP{}
	m := managerWith(ip)
	m.notify = make(chan ConnectionStatus, 8)
	m.rememberRuleRoute("1.1.1.1/32", []string{"1.1.1.1/32", "dev", "awg0"},
		map[string]bool{"ip:1.1.1.1": true, "cidr:1.1.1.1/32": true})

	old := routingWith(config.RoutingModeVPNList,
		rule(config.RuleTypeIP, "1.1.1.1"),
		rule(config.RuleTypeCIDR, "1.1.1.1/32"))
	now := routingWith(config.RoutingModeVPNList, rule(config.RuleTypeCIDR, "1.1.1.1/32"))

	delta, _ := planRoutingDelta(old, now)
	m.applyRoutingDelta(&tunnel.Spec{}, now, delta)

	if len(ip.deleted) != 0 {
		t.Errorf("маршрут снят, хотя его держит второе правило: %v", ip.deleted)
	}
	if len(m.ruleRoutes) != 1 {
		t.Errorf("маршрут забыт: %v", m.ruleRoutes)
	}
}

func ipJoined(routes [][]string) []string {
	out := make([]string, 0, len(routes))
	for _, args := range routes {
		out = append(out, strings.Join(args, " "))
	}
	return out
}
