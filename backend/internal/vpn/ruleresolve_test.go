package vpn

import (
	"testing"

	"github.com/user/amnezia-web-client/internal/config"
	"github.com/user/amnezia-web-client/internal/tunnel"
)

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

	if err := m.configureRouting(&tunnel.Spec{}, routing); err != nil {
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

	if err := m.configureRouting(&tunnel.Spec{}, routing); err != nil {
		t.Fatalf("настройка не удалась: %v", err)
	}

	if got := ip.joinedAdded(); len(got) != 1 || got[0] != "1.1.1.1/32 dev awg0" {
		t.Errorf("маршруты %v, ожидался только один — от включённого правила", got)
	}
}
