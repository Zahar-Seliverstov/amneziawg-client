package vpn

import (
	"errors"
	"net/netip"
	"testing"

	"github.com/user/amnezia-web-client/internal/config"
	"github.com/user/amnezia-web-client/internal/geodata"
)

type fakeGeo map[string][]netip.Prefix

func (f fakeGeo) IPs(code string) ([]netip.Prefix, error) { return f[code], nil }
func (f fakeGeo) Sites(string) ([]geodata.Domain, error)  { return nil, nil }

// Набор geoip в режиме исключений ведётся путём по умолчанию одной пачкой —
// без опроса ядра о каждой подсети. IPv6 без адреса IPv6 у туннеля не ставится.
func TestGeoIPBypassIsOneBatch(t *testing.T) {
	ask := errors.New("ядро о подсетях набора спрашивать не должны")
	ip := &fakeIP{def: homeGateway(), pathErr: map[string]error{"5.0.0.0": ask, "31.0.0.0": ask}}
	m := managerWith(ip)
	m.SetGeo(fakeGeo{"ru": {
		netip.MustParsePrefix("5.0.0.0/8"),
		netip.MustParsePrefix("31.0.0.0/16"),
		netip.MustParsePrefix("2a00::/12"),
	}})

	rules := []config.RoutingRule{rule(config.RuleTypeGeoIP, "ru")}
	m.installRuleRoutes(config.RoutingModeDirectList, m.resolveRules(rules, nil, false))

	got := ipJoined(ip.added)
	want := []string{"5.0.0.0/8 via 192.168.0.1 dev wlan0", "31.0.0.0/16 via 192.168.0.1 dev wlan0"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("поставлено %v, ждали %v", got, want)
	}
	if len(ip.addBatches) != 1 {
		t.Errorf("запусков ip: %d, ждали один", len(ip.addBatches))
	}

	// Снимается набор по своему ключу, как любое правило.
	m.dropRuleRoutes(map[string]bool{"geoip:ru": true})
	if len(ip.deleted) != 2 || len(m.ruleRoutes) != 0 {
		t.Errorf("снято %v, в учёте осталось %d", ip.deleted, len(m.ruleRoutes))
	}
}

// Подсеть, вписанная отдельным правилом, ведётся по-прежнему через опрос ядра,
// даже если она же есть в наборе: у неё может быть свой путь.
func TestGeoIPExplicitRuleKeepsKernelPath(t *testing.T) {
	ip := &fakeIP{def: homeGateway()}
	m := managerWith(ip)
	m.SetGeo(fakeGeo{"ru": {netip.MustParsePrefix("5.0.0.0/8")}})

	addrs := m.resolveRules([]config.RoutingRule{
		rule(config.RuleTypeGeoIP, "ru"),
		rule(config.RuleTypeCIDR, "5.0.0.0/8"),
	}, nil, false)

	if addrs.bulk["5.0.0.0/8"] {
		t.Error("подсеть отдельного правила попала в пачку набора")
	}
	if len(addrs.sources["5.0.0.0/8"]) != 2 {
		t.Errorf("источники подсети: %v", addrs.sources["5.0.0.0/8"])
	}
}
