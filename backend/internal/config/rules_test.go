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

func TestParseRuleValuePrefixes(t *testing.T) {
	cases := []struct {
		in, typ, value string
	}{
		{"geoip:RU", RuleTypeGeoIP, "ru"},
		{"geosite:geolocation-!cn", RuleTypeGeoSite, "geolocation-!cn"},
		{"geosite:google@cn", RuleTypeGeoSite, "google@cn"},
		{"full:WWW.google.com", RuleTypeFull, "www.google.com"},
		{"keyword:goog", RuleTypeKeyword, "goog"},
		{`regexp:^ads\.`, RuleTypeRegexp, `^ads\.`},
		{"domain:localhost", RuleTypeDomain, "localhost"},
		// Адрес IPv6 тоже с двоеточием, но приставкой не считается.
		{"2001:db8::1", RuleTypeIP, "2001:db8::1"},
		{".ru", RuleTypeZone, ".ru"},
	}

	for _, tc := range cases {
		rule, err := ParseRuleValue(tc.in)
		if err != nil {
			t.Errorf("%q: %v", tc.in, err)
			continue
		}
		if rule.Type != tc.typ || rule.Value != tc.value {
			t.Errorf("%q → %s:%s, ждали %s:%s", tc.in, rule.Type, rule.Value, tc.typ, tc.value)
		}
	}

	for _, bad := range []string{"geoip:ru@cn", "geoip:", "regexp:(", "keyword:a b", "geosite:Ю"} {
		if _, err := ParseRuleValue(bad); err == nil {
			t.Errorf("%q принято", bad)
		}
	}
}

// app: — программа по имени. Регистр не важен, путь и пробелы не годятся:
// сравнивается с именем процесса, и путь в правиле никогда бы не совпал.
func TestParseAppRule(t *testing.T) {
	rule, err := ParseRuleValue("app:Minecraft")
	if err != nil {
		t.Fatal(err)
	}
	if rule.Type != RuleTypeApp || rule.Value != "minecraft" {
		t.Fatalf("app:Minecraft разобрано как %s:%s", rule.Type, rule.Value)
	}

	rule, err = ParseRuleValue("app:Vesktop/UDP")
	if err != nil {
		t.Fatal(err)
	}
	if name, udp := SplitApp(rule.Value); name != "vesktop" || !udp {
		t.Fatalf("app:Vesktop/UDP разобрано как %q (udp=%v)", name, udp)
	}

	for _, bad := range []string{"app:/usr/bin/java", "app:my game", "app:x", "app:", "app:vesktop/tcp", "app:/udp", "app:x/udp"} {
		if _, err := ParseRuleValue(bad); err == nil {
			t.Errorf("%q принято, а не должно", bad)
		}
	}
}

// Опечатка в адресе не должна молча становиться доменом, который никогда
// не разрешится: правило «работает», а трафик идёт не туда.
func TestParseRuleValueMalformedAddress(t *testing.T) {
	for _, value := range []string{"1.2.3.999", "1.2.3.4.5", "01.2.3.4"} {
		if r, err := ParseRuleValue(value); err == nil {
			t.Errorf("%q принят как %s %q", value, r.Type, r.Value)
		}
	}

	r, err := ParseRuleValue("1.1.1.1.")
	if err != nil || r.Type != RuleTypeIP || r.Value != "1.1.1.1" {
		t.Errorf("1.1.1.1. → %s %q, %v; ожидался адрес 1.1.1.1", r.Type, r.Value, err)
	}

	// Цифры в начале имени — это нормально.
	if r, err := ParseRuleValue("4pda.to"); err != nil || r.Type != RuleTypeDomain {
		t.Errorf("4pda.to → %s, %v", r.Type, err)
	}
}
