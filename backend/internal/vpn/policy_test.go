package vpn

import (
	"reflect"
	"strings"
	"testing"

	"github.com/user/amnezia-web-client/internal/config"
)

// fakePolicy подменяет маршрутизацию по политикам: запоминает, что от неё
// просили, и ничего не трогает в системе.
type fakePolicy struct {
	ups       []bool
	downs     int
	apps      appSet
	viaTunnel bool
}

func (f *fakePolicy) Table() []string    { return []string{"table", "51821"} }
func (f *fakePolicy) TunnelMark() uint32 { return 0x51820 }
func (f *fakePolicy) Up(full bool) error { f.ups = append(f.ups, full); return nil }
func (f *fakePolicy) Down()              { f.downs++ }
func (f *fakePolicy) SetApps(names appSet, viaTunnel bool, ifname string, v6 bool) error {
	f.apps, f.viaTunnel = names, viaTunnel
	return nil
}

func policyManager(ip *fakeIP) (*Manager, *fakePolicy) {
	p := &fakePolicy{}
	m := managerWith(ip)
	m.policy = p
	return m, p
}

func fullConfig() *config.AmneziaWGConfig {
	return &config.AmneziaWGConfig{
		Interface: config.InterfaceConfig{Address: []string{"10.8.1.18/32"}},
		Peers:     []config.PeerConfig{{Endpoint: "203.0.113.7:51820", AllowedIPs: []string{"0.0.0.0/0"}}},
	}
}

// Главное ради чего всё затевалось: «мимо VPN» не означает «к домашнему
// роутеру». Исключение выводится из нашей таблицы маршрутом throw, и дальше
// пакет ведёт система — тем путём, каким вела бы без туннеля, хоть вторым
// VPN. Шлюза в маршруте быть не должно.
func TestPolicyBypassIsThrowNotGateway(t *testing.T) {
	ip := &fakeIP{def: homeGateway()}
	m, _ := policyManager(ip)

	args, needed, err := m.bypassRoute("93.184.216.34/32")
	if err != nil || !needed {
		t.Fatalf("bypassRoute: needed=%v err=%v", needed, err)
	}

	want := []string{"throw", "93.184.216.34/32", "table", "51821"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("маршрут исключения %v, ожидали %v", args, want)
	}
}

// Режим «всё, кроме списка»: маршрут по умолчанию в нашей таблице, исключения
// — throw одной пачкой, сервер VPN к роутеру не прибивается (его пакеты несут
// метку туннеля и идут системной таблицей сами).
func TestPolicyDirectListRouting(t *testing.T) {
	ip := &fakeIP{def: homeGateway()}
	m, p := policyManager(ip)

	rc := routingWith(config.RoutingModeDirectList,
		rule(config.RuleTypeIP, "1.2.3.4"),
		rule(config.RuleTypeCIDR, "10.20.0.0/16"),
	)

	if err := m.configureRouting(fullConfig(), rc); err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(p.ups, []bool{true}) {
		t.Fatalf("правила выбора таблиц: %v, ожидали один Up(full=true)", p.ups)
	}

	added := strings.Join(ip.joinedAdded(), "\n")
	for _, want := range []string{
		"0.0.0.0/0 dev awg0 table 51821",
		"throw 1.2.3.4/32 table 51821",
		"throw 10.20.0.0/16 table 51821",
	} {
		if !strings.Contains(added, want) {
			t.Errorf("нет маршрута %q среди:\n%s", want, added)
		}
	}
	if strings.Contains(added, "via") || strings.Contains(added, "203.0.113.7") {
		t.Errorf("маршрут через шлюз или пришпиленный сервер при политиках:\n%s", added)
	}
	if strings.Contains(added, "0.0.0.0/1") {
		t.Errorf("пара /1 не нужна при своей таблице:\n%s", added)
	}
}

// Режим «только список»: перечисленное — в нашу таблицу, точные системные
// маршруты перебивать нечем, поэтому full=false.
func TestPolicyVPNListRouting(t *testing.T) {
	ip := &fakeIP{}
	m, p := policyManager(ip)

	rc := routingWith(config.RoutingModeVPNList, rule(config.RuleTypeIP, "1.2.3.4"))
	if err := m.configureRouting(fullConfig(), rc); err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(p.ups, []bool{false}) {
		t.Fatalf("правила выбора таблиц: %v, ожидали Up(full=false)", p.ups)
	}
	if got := strings.Join(ip.joinedAdded(), "\n"); got != "1.2.3.4/32 dev awg0 table 51821" {
		t.Fatalf("маршруты:\n%s", got)
	}
}

// app:minecraft — не адрес: ни маршрута, ни ошибки резолва, только набор
// программ. Куда их вести, решает режим.
func TestPolicyAppsFollowMode(t *testing.T) {
	for _, tc := range []struct {
		mode      config.RoutingMode
		viaTunnel bool
	}{
		{config.RoutingModeDirectList, false},
		{config.RoutingModeVPNList, true},
	} {
		ip := &fakeIP{}
		m, p := policyManager(ip)

		rc := routingWith(tc.mode, rule(config.RuleTypeApp, "minecraft"), rule(config.RuleTypeApp, "vesktop/udp"))
		if err := m.configureRouting(fullConfig(), rc); err != nil {
			t.Fatal(err)
		}

		want := appSet{all: []string{"minecraft"}, udp: []string{"vesktop"}}
		if !reflect.DeepEqual(p.apps, want) || p.viaTunnel != tc.viaTunnel {
			t.Errorf("%s: программы %v viaTunnel=%v", tc.mode, p.apps, p.viaTunnel)
		}
		for _, args := range ip.added {
			if strings.Contains(strings.Join(args, " "), "minecraft") {
				t.Errorf("%s: правило программы дало маршрут %v", tc.mode, args)
			}
		}
	}
}

// Метка туннеля уходит в ядро: без неё маршрут по умолчанию в нашей таблице
// увёл бы в туннель и пакеты к самому серверу.
func TestPolicyUAPICarriesTunnelMark(t *testing.T) {
	m, _ := policyManager(&fakeIP{})
	cfg := fullConfig()
	cfg.Interface.PrivateKey = "YAnz5TF+lXXJte14tji3zlMNq+hd2rYUIgJBgB3fBmk="

	if uapi := m.buildUAPIConfig(cfg); !strings.Contains(uapi, "fwmark=333856\n") {
		t.Fatalf("в UAPI нет метки туннеля:\n%s", uapi)
	}

	// Без политик — как раньше, без метки.
	plain := managerWith(&fakeIP{})
	if uapi := plain.buildUAPIConfig(cfg); strings.Contains(uapi, "fwmark") {
		t.Fatalf("метка без маршрутизации по политикам:\n%s", uapi)
	}
}
