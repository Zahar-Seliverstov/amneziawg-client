package routing

import (
	"testing"

	"github.com/user/amnezia-web-client/internal/config"
)

func rule(t config.RuleType, value string) config.RoutingRule {
	return config.RoutingRule{Type: t, Value: value, Enabled: true}
}

func TestMatcherDomainCoversSubdomains(t *testing.T) {
	m := NewMatcher([]config.RoutingRule{rule(config.RuleTypeDomain, "google.com")})

	for _, name := range []string{"google.com", "www.google.com", "a.b.google.com", "GOOGLE.COM", "google.com."} {
		if !m.Match(name) {
			t.Errorf("%q должно попадать под правило google.com", name)
		}
	}

	// Похожее имя не должно совпадать: notgoogle.com — чужой домен.
	for _, name := range []string{"notgoogle.com", "google.com.evil.net", "com"} {
		if m.Match(name) {
			t.Errorf("%q не должно попадать под правило google.com", name)
		}
	}
}

func TestMatcherZone(t *testing.T) {
	m := NewMatcher([]config.RoutingRule{rule(config.RuleTypeZone, ".ru")})

	for _, name := range []string{"yandex.ru", "www.yandex.ru", "a.b.c.ru"} {
		if !m.Match(name) {
			t.Errorf("%q должно попадать под зону .ru", name)
		}
	}

	for _, name := range []string{"google.com", "ru", "ru.com"} {
		if m.Match(name) {
			t.Errorf("%q не должно попадать под зону .ru", name)
		}
	}
}

// Зону пользователь может записать и без ведущей точки — приложение обязано
// понять оба варианта одинаково.
func TestMatcherZoneWithoutLeadingDot(t *testing.T) {
	m := NewMatcher([]config.RoutingRule{rule(config.RuleTypeZone, "ru")})
	if !m.Match("yandex.ru") {
		t.Error("зона, записанная без точки, должна работать так же")
	}
}

func TestMatcherIgnoresDisabledAndOtherTypes(t *testing.T) {
	m := NewMatcher([]config.RoutingRule{
		{Type: config.RuleTypeDomain, Value: "google.com", Enabled: false},
		rule(config.RuleTypeIP, "1.1.1.1"),
		rule(config.RuleTypeCIDR, "10.0.0.0/8"),
	})

	if !m.Empty() {
		t.Error("правил по именам нет — сопоставитель должен быть пустым")
	}
	if m.Match("google.com") {
		t.Error("выключенное правило не должно срабатывать")
	}
}

func TestMatcherNilIsSafe(t *testing.T) {
	var m *Matcher
	if !m.Empty() || m.Match("google.com") {
		t.Error("нулевой сопоставитель должен быть безопасен и ни с чем не совпадать")
	}
}
