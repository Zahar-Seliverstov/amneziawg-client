package vpn

import (
	"errors"
	"net/netip"
	"strings"
	"testing"

	"github.com/user/amnezia-web-client/internal/config"
	"github.com/user/amnezia-web-client/internal/iproute"
	"github.com/user/amnezia-web-client/internal/tunnel"
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

	if err := m.configureRouting(&tunnel.Spec{}, routing); err != nil {
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

// Адрес, уже заведённый правилом, посредник берёт под учёт, но не снимает.
func TestTunnelInstallerSkipsForeignRoute(t *testing.T) {
	ip := &existsIP{}
	inst := newTunnelInstaller(ip, func(p string) []string { return []string{p, "dev", "awg0"} })
	p := netip.MustParsePrefix("1.2.3.4/32")

	if err := inst.Add(p); err != nil {
		t.Fatalf("File exists принят за ошибку: %v", err)
	}
	if err := inst.Remove(p); err != nil {
		t.Fatal(err)
	}
	if len(ip.deleted) != 0 {
		t.Errorf("снят чужой маршрут: %v", ip.deleted)
	}
}

type existsIP struct{ fakeIP }

func (e *existsIP) AddRoute(args ...string) error {
	return errors.Join(iproute.ErrExists, errors.New("RTNETLINK answers: File exists"))
}
