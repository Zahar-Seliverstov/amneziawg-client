package config

import "testing"

func TestRoutingRuleValidate(t *testing.T) {
	cases := []struct {
		name  string
		rule  RoutingRule
		want  string // ожидаемое значение после нормализации; "" — отказ
		valid bool
	}{
		{"адрес", RoutingRule{Type: RuleTypeIP, Value: " 1.1.1.1 "}, "1.1.1.1", true},
		{"адрес IPv6", RoutingRule{Type: RuleTypeIP, Value: "2001:db8::1"}, "2001:db8::1", true},
		{"не адрес", RoutingRule{Type: RuleTypeIP, Value: "1.1.1"}, "", false},
		{"подсеть", RoutingRule{Type: RuleTypeCIDR, Value: "10.0.0.0/8"}, "10.0.0.0/8", true},
		{"подсеть без маски", RoutingRule{Type: RuleTypeCIDR, Value: "10.0.0.0"}, "", false},
		{"домен", RoutingRule{Type: RuleTypeDomain, Value: "Example.COM"}, "Example.COM", true},
		{"домен со схемой", RoutingRule{Type: RuleTypeDomain, Value: "https://example.com"}, "", false},
		{"домен с пробелом", RoutingRule{Type: RuleTypeDomain, Value: "exa mple.com"}, "", false},
		{"домен с пустой частью", RoutingRule{Type: RuleTypeDomain, Value: "example..com"}, "", false},
		{"домен с дефисом по краю", RoutingRule{Type: RuleTypeDomain, Value: "-example.com"}, "", false},
		{"домен на кириллице", RoutingRule{Type: RuleTypeDomain, Value: "почта.рф"}, "почта.рф", true},
		{"зона", RoutingRule{Type: RuleTypeZone, Value: ".ru"}, ".ru", true},
		{"пустое значение", RoutingRule{Type: RuleTypeIP, Value: "   "}, "", false},
		{"неизвестный тип", RoutingRule{Type: "magic", Value: "1.1.1.1"}, "", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rule := tc.rule
			err := rule.Validate()

			if tc.valid && err != nil {
				t.Fatalf("правило отвергнуто: %v", err)
			}
			if !tc.valid {
				if err == nil {
					t.Fatal("непригодное правило принято")
				}
				return
			}
			if rule.Value != tc.want {
				t.Errorf("значение %q, ожидалось %q", rule.Value, tc.want)
			}
		})
	}
}

// Один и тот же файл правил приходит и из интерфейса, и загрузкой с диска:
// проверка обязана чинить идентификаторы, а не молча пропускать мусор.
func TestRoutingConfigValidate(t *testing.T) {
	routing := RoutingConfig{
		Mode: RoutingModeVPNList,
		Rules: []RoutingRule{
			{Type: RuleTypeIP, Value: "1.1.1.1"},
			{ID: "dup", Type: RuleTypeCIDR, Value: "10.0.0.0/8"},
			{ID: "dup", Type: RuleTypeDomain, Value: "example.com"},
		},
	}

	if err := routing.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}

	seen := map[string]bool{}
	for _, rule := range routing.Rules {
		if rule.ID == "" {
			t.Error("правило осталось без идентификатора")
		}
		if seen[rule.ID] {
			t.Errorf("идентификатор %q повторяется: удаление одного правила убрало бы соседнее", rule.ID)
		}
		seen[rule.ID] = true
	}
}

func TestRoutingConfigValidateRejectsBadInput(t *testing.T) {
	bad := RoutingConfig{Mode: "странный"}
	if err := bad.Validate(); err == nil {
		t.Error("неизвестный режим принят")
	}

	withBadRule := RoutingConfig{
		Mode:  RoutingModeDirectList,
		Rules: []RoutingRule{{Type: RuleTypeCIDR, Value: "не подсеть"}},
	}
	if err := withBadRule.Validate(); err == nil {
		t.Error("непригодное правило принято")
	}
}

func TestRoutingConfigCloneIsIndependent(t *testing.T) {
	original := RoutingConfig{
		Mode:  RoutingModeVPNList,
		Rules: []RoutingRule{{ID: "r1", Type: RuleTypeIP, Value: "1.1.1.1", Enabled: true}},
	}

	clone := original.Clone()
	clone.Rules[0].Value = "9.9.9.9"

	if original.Rules[0].Value != "1.1.1.1" {
		t.Error("копия делит массив правил с оригиналом")
	}
}

func TestAmneziaConfigCloneIsIndependent(t *testing.T) {
	original := AmneziaWGConfig{
		Interface: InterfaceConfig{Address: []string{"10.8.0.2/32"}, DNS: []string{"10.8.0.1"}},
		Peers:     []PeerConfig{{AllowedIPs: []string{"0.0.0.0/0"}}},
	}

	clone := original.Clone()
	clone.Interface.Address[0] = "192.0.2.1/32"
	clone.Interface.DNS[0] = "8.8.8.8"
	clone.Peers[0].AllowedIPs[0] = "192.0.2.0/24"

	if original.Interface.Address[0] != "10.8.0.2/32" ||
		original.Interface.DNS[0] != "10.8.0.1" ||
		original.Peers[0].AllowedIPs[0] != "0.0.0.0/0" {
		t.Error("копия делит массивы с оригиналом")
	}
}
