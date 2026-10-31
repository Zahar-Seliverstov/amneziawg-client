package routing

import (
	"testing"

	"github.com/user/amnezia-web-client/internal/config"
	"github.com/user/amnezia-web-client/internal/geodata"
)

func rule(t config.RuleType, value string) config.RoutingRule {
	return config.RoutingRule{Type: t, Value: value, Enabled: true}
}

func TestMatcherDomainCoversSubdomains(t *testing.T) {
	m := NewMatcher([]config.RoutingRule{rule(config.RuleTypeDomain, "google.com")}, nil)

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
	m := NewMatcher([]config.RoutingRule{rule(config.RuleTypeZone, ".ru")}, nil)

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
	m := NewMatcher([]config.RoutingRule{rule(config.RuleTypeZone, "ru")}, nil)
	if !m.Match("yandex.ru") {
		t.Error("зона, записанная без точки, должна работать так же")
	}
}

func TestMatcherIgnoresDisabledAndOtherTypes(t *testing.T) {
	m := NewMatcher([]config.RoutingRule{
		{Type: config.RuleTypeDomain, Value: "google.com", Enabled: false},
		rule(config.RuleTypeIP, "1.1.1.1"),
		rule(config.RuleTypeCIDR, "10.0.0.0/8"),
	}, nil)

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

type fakeSites map[string][]geodata.Domain

func (f fakeSites) Sites(tag string) ([]geodata.Domain, error) { return f[tag], nil }

func TestMatcherKinds(t *testing.T) {
	sites := fakeSites{"youtube": {
		{Type: geodata.DomainSuffix, Value: "youtube.com"},
		{Type: geodata.DomainFull, Value: "youtu.be"},
		{Type: geodata.DomainKeyword, Value: "ytimg"},
		{Type: geodata.DomainRegexp, Value: `^r\d+---sn-`},
	}}

	m := NewMatcher([]config.RoutingRule{
		rule(config.RuleTypeGeoSite, "youtube"),
		rule(config.RuleTypeFull, "exact.example.org"),
		rule(config.RuleTypeKeyword, "torrent"),
		rule(config.RuleTypeRegexp, `^ads\d*\.`),
	}, sites)

	for _, name := range []string{
		"m.youtube.com", "youtu.be", "i.ytimg.com", "r3---sn-abc.googlevideo.com",
		"exact.example.org", "rutorrent.org", "ads2.example.com",
	} {
		if !m.Match(name) {
			t.Errorf("%q должно совпасть", name)
		}
	}

	for _, name := range []string{"www.youtu.be", "sub.exact.example.org", "example.org", "bads.example.com"} {
		if m.Match(name) {
			t.Errorf("%q не должно совпасть", name)
		}
	}
}

// Без базы правило geosite просто ничего не покрывает — и не роняет сборку.
func TestMatcherGeoSiteWithoutBase(t *testing.T) {
	m := NewMatcher([]config.RoutingRule{rule(config.RuleTypeGeoSite, "youtube")}, nil)
	if !m.Empty() || m.Match("youtube.com") {
		t.Error("без базы geosite не должен совпадать ни с чем")
	}
}
