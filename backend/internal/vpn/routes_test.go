package vpn

import (
	"net"
	"strings"
	"testing"

	"github.com/user/amnezia-web-client/internal/iproute"

	"github.com/user/amnezia-web-client/internal/config"
	"github.com/user/amnezia-web-client/internal/firewall"
)

// Ради этого правила и собираются в пакет: сотня маршрутов — один запуск ip,
// а не сотня fork+exec подряд внутри обработчика HTTP.
func TestRulesGoOutInOneBatch(t *testing.T) {
	ip := &fakeIP{}
	m := managerWith(ip)

	routing := &config.RoutingConfig{
		Mode: config.RoutingModeVPNList,
		Rules: []config.RoutingRule{
			{ID: "1", Type: config.RuleTypeIP, Value: "1.1.1.1", Enabled: true},
			{ID: "2", Type: config.RuleTypeCIDR, Value: "10.0.0.0/8", Enabled: true},
			{ID: "3", Type: config.RuleTypeIP, Value: "8.8.8.8", Enabled: true},
		},
	}

	if err := m.configureRouting(&config.AmneziaWGConfig{}, routing); err != nil {
		t.Fatalf("настройка не удалась: %v", err)
	}

	if len(ip.addBatches) != 1 {
		t.Fatalf("запусков ip: %d, ожидался один", len(ip.addBatches))
	}

	want := []string{"1.1.1.1/32 dev awg0", "10.0.0.0/8 dev awg0", "8.8.8.8/32 dev awg0"}
	got := ip.joinedAdded()
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("маршруты %v, ожидались %v", got, want)
	}

	// Учёт ведётся по-прежнему: без него пересборка не снимет свои записи.
	// Маршруты правил лежат отдельно от устройства туннеля — их правит
	// пользователь посреди работы.
	if len(m.ruleRoutes) != 3 {
		t.Errorf("в учёте %d маршрутов, ожидалось 3", len(m.ruleRoutes))
	}
}

// Два имени вполне могут жить на одном адресе. Повторный "ip route add" на
// него всё равно отказал бы, а в пакете такой отказ ещё и пришлось бы
// разбирать по номеру строки.
func TestRepeatedAddressesGoOutOnce(t *testing.T) {
	ip := &fakeIP{}
	m := managerWith(ip)

	routing := &config.RoutingConfig{
		Mode: config.RoutingModeVPNList,
		Rules: []config.RoutingRule{
			{ID: "1", Type: config.RuleTypeIP, Value: "1.1.1.1", Enabled: true},
			{ID: "2", Type: config.RuleTypeCIDR, Value: "1.1.1.1/32", Enabled: true},
			{ID: "3", Type: config.RuleTypeIP, Value: "1.1.1.1", Enabled: true},
		},
	}

	if err := m.configureRouting(&config.AmneziaWGConfig{}, routing); err != nil {
		t.Fatalf("настройка не удалась: %v", err)
	}

	if got := ip.joinedAdded(); len(got) != 1 {
		t.Errorf("маршруты %v, ожидался один", got)
	}
}

// Выключенное правило маршрута не получает — и место в пакете не занимает.
func TestDisabledRulesAreSkipped(t *testing.T) {
	ip := &fakeIP{}
	m := managerWith(ip)

	routing := &config.RoutingConfig{
		Mode: config.RoutingModeVPNList,
		Rules: []config.RoutingRule{
			{ID: "1", Type: config.RuleTypeIP, Value: "1.1.1.1", Enabled: true},
			{ID: "2", Type: config.RuleTypeIP, Value: "8.8.8.8"},
		},
	}

	if err := m.configureRouting(&config.AmneziaWGConfig{}, routing); err != nil {
		t.Fatalf("настройка не удалась: %v", err)
	}

	if got := ip.joinedAdded(); len(got) != 1 || got[0] != "1.1.1.1/32 dev awg0" {
		t.Errorf("маршруты %v, ожидался только один — от включённого правила", got)
	}
}

// Снятие тоже идёт пакетом, но порядок обязан остаться обратным: маршрут мог
// опираться на другой, поставленный раньше.
func TestFlushGoesOutInOneReversedBatch(t *testing.T) {
	ip := &fakeIP{}
	m := managerWith(ip)
	m.installedRoutes = [][]string{
		{"1.1.1.1/32", "dev", "awg0"},
		{"8.8.8.8/32", "dev", "awg0"},
		{"10.0.0.0/8", "dev", "awg0"},
	}

	m.flushRoutes()

	if len(ip.delBatches) != 1 {
		t.Fatalf("запусков ip: %d, ожидался один", len(ip.delBatches))
	}
	if len(m.installedRoutes) != 0 {
		t.Errorf("учёт не обнулён: %v", m.installedRoutes)
	}

	want := []string{"10.0.0.0/8 dev awg0", "8.8.8.8/32 dev awg0", "1.1.1.1/32 dev awg0"}
	var got []string
	for _, args := range ip.delBatches[0] {
		got = append(got, strings.Join(args, " "))
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("снималось %v, ожидалось %v", got, want)
	}
}

func TestFlushOfNothingDoesNotRunIP(t *testing.T) {
	ip := &fakeIP{}
	managerWith(ip).flushRoutes()

	if len(ip.delBatches) != 0 || len(ip.deleted) != 0 {
		t.Error("на пустой учёт запускали ip")
	}
}

// Регрессия. Адрес сервера спрашивается у системного резолвера, а пересборка
// правил на живом туннеле начинается с остановки нашего же посредника DNS, на
// который resolv.conf в этот момент и указывает. Запрос уходил в мёртвый
// сокет, и в режиме «всё через VPN, кроме списка» настройка туннеля
// отказывала целиком: правку одного правила пользователь оплачивал пропавшим
// интернетом. Выясненный при подключении адрес держится всё соединение.
func TestEndpointAddressIsRememberedForTheConnection(t *testing.T) {
	ip := &fakeIP{
		paths: map[string]iproute.Path{"203.0.113.10": homeGateway()},
		def:   homeGateway(),
	}
	m := managerWith(ip)

	// Имя заведомо неразрешимое: .invalid зарезервирован ровно для этого.
	cfg := &config.AmneziaWGConfig{
		Interface: config.InterfaceConfig{Address: []string{"10.8.1.18/32"}},
		Peers:     []config.PeerConfig{{Endpoint: "сервера-нет.invalid:51820"}},
	}

	// Ровно то, что случается при подключении: адрес выяснен и запомнен.
	// Спроси клиент имя заново — .invalid не разрешается ни у кого, и
	// настройка отказала бы.
	m.endpointAddrs = map[string][]net.IP{"сервера-нет.invalid": {net.ParseIP("203.0.113.10")}}

	if err := m.setupDefaultVPNRoute(cfg); err != nil {
		t.Fatalf("запомненный адрес сервера не пригодился: %v", err)
	}

	if !containsRoute(ip.joinedAdded(), "203.0.113.10/32") {
		t.Errorf("адрес сервера не выведен мимо туннеля: %v", ip.joinedAdded())
	}
}

// Endpoint чаще всего записан адресом. Спрашивать о нём кого бы то ни было
// незачем — и молчащий резолвер такому соединению не помеха.
func TestLiteralEndpointIsNotResolved(t *testing.T) {
	ip := &fakeIP{
		paths: map[string]iproute.Path{"203.0.113.10": homeGateway()},
		def:   homeGateway(),
	}
	m := managerWith(ip)

	cfg := &config.AmneziaWGConfig{
		Interface: config.InterfaceConfig{Address: []string{"10.8.1.18/32"}},
		Peers:     []config.PeerConfig{{Endpoint: "203.0.113.10:51820"}},
	}

	if err := m.setupDefaultVPNRoute(cfg); err != nil {
		t.Fatalf("настройка не удалась: %v", err)
	}

	if len(m.endpointAddrs["203.0.113.10"]) != 1 {
		t.Errorf("адрес сервера не запомнен: %v", m.endpointAddrs)
	}
}

func containsRoute(routes []string, prefix string) bool {
	for _, route := range routes {
		if strings.HasPrefix(route, prefix+" ") {
			return true
		}
	}
	return false
}

// fakeFirewall считает, сколько раз блокировку ставили и снимали. Driver
// встроен ради неэкспортируемого метода: реализовать интерфейс снаружи
// пакета firewall иначе нельзя.
type fakeFirewall struct {
	firewall.Driver
	applied int
	cleared int
}

func (f *fakeFirewall) Name() string               { return "тест" }
func (f *fakeFirewall) Apply(firewall.Rules) error { f.applied++; return nil }
func (f *fakeFirewall) Clear() error               { f.cleared++; return nil }

// Переприменение всего набора nft/iptables шло на каждую правку правил вместе
// с пересборкой маршрутов. От списка блокировка зависит не целиком, а через
// один признак — имеет ли она смысл при этой маршрутизации, — и пока он тот
// же, трогать firewall незачем.
func TestKillSwitchNotReappliedWhenDecisionUnchanged(t *testing.T) {
	fw := &fakeFirewall{}
	m := managerWith(&fakeIP{})
	m.firewallDriver = fw
	m.killSwitchOn = true

	cfg := peerWith("0.0.0.0/0")
	rc := &config.RoutingConfig{Mode: config.RoutingModeDirectList}

	m.syncKillSwitch(&cfg, rc)
	if fw.applied != 1 {
		t.Fatalf("блокировка ставилась %d раз, ожидался один", fw.applied)
	}

	// Правило добавили, но решение то же: исключений по-прежнему нет.
	rc = &config.RoutingConfig{
		Mode:  config.RoutingModeDirectList,
		Rules: []config.RoutingRule{{ID: "1", Type: config.RuleTypeIP, Value: "1.1.1.1"}},
	}
	m.syncKillSwitch(&cfg, rc)

	if fw.applied != 1 {
		t.Errorf("блокировка переприменена зря: ставилась %d раз", fw.applied)
	}
	if fw.cleared != 0 {
		t.Errorf("блокировка снималась %d раз, не должна была", fw.cleared)
	}
}

// А когда решение действительно меняется — включилось действующее исключение,
// и блокировка обрезала бы ровно его, — она обязана уйти.
func TestKillSwitchFollowsDecisionChange(t *testing.T) {
	fw := &fakeFirewall{}
	m := managerWith(&fakeIP{})
	m.firewallDriver = fw
	m.killSwitchOn = true

	cfg := peerWith("0.0.0.0/0")

	m.syncKillSwitch(&cfg, &config.RoutingConfig{Mode: config.RoutingModeDirectList})
	if fw.applied != 1 {
		t.Fatalf("блокировка не поставлена")
	}

	m.syncKillSwitch(&cfg, &config.RoutingConfig{
		Mode:  config.RoutingModeDirectList,
		Rules: []config.RoutingRule{{ID: "1", Type: config.RuleTypeIP, Value: "1.1.1.1", Enabled: true}},
	})
	if fw.cleared != 1 {
		t.Errorf("блокировка не снята при появлении действующего исключения")
	}

	// Исключение убрали — блокировка возвращается.
	m.syncKillSwitch(&cfg, &config.RoutingConfig{Mode: config.RoutingModeDirectList})
	if fw.applied != 2 {
		t.Errorf("блокировка не вернулась: ставилась %d раз", fw.applied)
	}
}

// Разбор системы забывает применённое решение: интерфейс исчезает вместе с
// правилами firewall, и следующее соединение обязано поставить их заново.
func TestKillSwitchIsReappliedAfterTeardown(t *testing.T) {
	fw := &fakeFirewall{}
	m := managerWith(&fakeIP{})
	m.firewallDriver = fw
	m.killSwitchOn = true

	cfg := peerWith("0.0.0.0/0")
	rc := &config.RoutingConfig{Mode: config.RoutingModeDirectList}

	m.syncKillSwitch(&cfg, rc)
	m.forgetKillSwitch()
	m.syncKillSwitch(&cfg, rc)

	if fw.applied != 2 {
		t.Errorf("блокировка ставилась %d раз, ожидалось два", fw.applied)
	}
}
