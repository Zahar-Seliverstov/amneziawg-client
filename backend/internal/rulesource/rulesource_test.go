package rulesource

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/user/amnezia-web-client/internal/config"
)

// fakeLookup отвечает по заранее заданной таблице и считает вопросы.
type fakeLookup struct {
	hosts map[string][]string
	addrs map[string][]string

	mu    sync.Mutex
	calls map[string]int
}

func (f *fakeLookup) LookupHost(ctx context.Context, host string) ([]string, error) {
	f.count("host:" + host)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if got, ok := f.hosts[host]; ok {
		return got, nil
	}
	return nil, errors.New("нет такого имени")
}

func (f *fakeLookup) LookupAddr(ctx context.Context, addr string) ([]string, error) {
	f.count("addr:" + addr)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if got, ok := f.addrs[addr]; ok {
		return got, nil
	}
	return nil, errors.New("нет обратной записи")
}

func (f *fakeLookup) count(key string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.calls == nil {
		f.calls = map[string]int{}
	}
	f.calls[key]++
}

func (f *fakeLookup) times(key string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[key]
}

func rule(id, ruleType, value string) config.RoutingRule {
	return config.RoutingRule{ID: id, Type: ruleType, Value: value, Enabled: true}
}

func TestRegistrableDomain(t *testing.T) {
	cases := map[string]string{
		"git.dtel.ru":         "dtel.ru",
		"bitrix.dtel.ru":      "dtel.ru",
		"dtel.ru":             "dtel.ru",
		"DTEL.RU.":            "dtel.ru",
		"a.b.c.example.com":   "example.com",
		"www.bbc.co.uk":       "bbc.co.uk",
		"bbc.co.uk":           "bbc.co.uk",
		"co.uk":               "co.uk",
		"localhost":           "localhost",
		"  mail.google.com  ": "google.com",
		"":                    "",
	}

	for name, want := range cases {
		if got := RegistrableDomain(name); got != want {
			t.Errorf("RegistrableDomain(%q) = %q, ожидалось %q", name, got, want)
		}
	}
}

// Имена сводятся в группу без единого вопроса к DNS: ответ виден по самому
// имени.
func TestSourcesOfNamesNeedNoDNS(t *testing.T) {
	lookup := &fakeLookup{}
	r := NewWith(lookup)

	got := r.Sources(context.Background(), []config.RoutingRule{
		rule("1", config.RuleTypeDomain, "git.dtel.ru"),
		rule("2", config.RuleTypeDomain, "bitrix.dtel.ru"),
		rule("3", config.RuleTypeZone, ".ru"),
	})

	if got["1"] != "dtel.ru" || got["2"] != "dtel.ru" {
		t.Errorf("имена не сошлись в один источник: %v", got)
	}
	if got["3"] != "ru" {
		t.Errorf("зона .ru дала источник %q", got["3"])
	}
}

// Список из одних имён не должен порождать ни одного запроса в сеть:
// сводить адреса с именами нечего, а сами имена разбираются без DNS.
func TestNamesOnlyListAsksNothing(t *testing.T) {
	lookup := &fakeLookup{hosts: map[string][]string{"git.dtel.ru": {"144.123.122.123"}}}
	r := NewWith(lookup)

	r.Sources(context.Background(), []config.RoutingRule{
		rule("1", config.RuleTypeDomain, "git.dtel.ru"),
		rule("2", config.RuleTypeDomain, "bitrix.dtel.ru"),
	})

	if n := lookup.times("host:git.dtel.ru"); n != 0 {
		t.Errorf("зря сходили в DNS %d раз", n)
	}
}

// Адрес относится к источнику, если его отдаёт правило-имя.
func TestAddressJoinsGroupByForwardLookup(t *testing.T) {
	lookup := &fakeLookup{
		hosts: map[string][]string{"git.dtel.ru": {"144.123.122.123"}},
	}
	r := NewWith(lookup)

	got := r.Sources(context.Background(), []config.RoutingRule{
		rule("1", config.RuleTypeDomain, "git.dtel.ru"),
		rule("2", config.RuleTypeIP, "144.123.122.123"),
		rule("3", config.RuleTypeCIDR, "144.123.122.0/24"),
	})

	for _, id := range []string{"1", "2", "3"} {
		if got[id] != "dtel.ru" {
			t.Errorf("правило %s отнесено к %q вместо dtel.ru", id, got[id])
		}
	}

	// Обратную запись при этом не спрашивали: прямой ответ надёжнее.
	if n := lookup.times("addr:144.123.122.123"); n != 0 {
		t.Errorf("зря спросили обратную запись %d раз", n)
	}
}

// Одиночный адрес, которого нет ни у одного правила-имени, опознаётся по
// обратной записи.
func TestAddressJoinsGroupByReverseLookup(t *testing.T) {
	lookup := &fakeLookup{
		addrs: map[string][]string{"144.123.122.123": {"test.dtel.ru."}},
	}
	r := NewWith(lookup)

	got := r.Sources(context.Background(), []config.RoutingRule{
		rule("1", config.RuleTypeIP, "144.123.122.123"),
	})

	if got["1"] != "dtel.ru" {
		t.Errorf("обратная запись не сработала: %v", got)
	}
}

// Молчание DNS — не ошибка: правило просто остаётся без источника и будет
// показано само по себе.
func TestUnknownAddressHasNoSource(t *testing.T) {
	r := NewWith(&fakeLookup{})

	got := r.Sources(context.Background(), []config.RoutingRule{
		rule("1", config.RuleTypeIP, "203.0.113.7"),
		rule("2", config.RuleTypeIP, "мусор"),
	})

	if len(got) != 0 {
		t.Errorf("источник придуман на пустом месте: %v", got)
	}
}

// Список перечитывается после каждой правки, поэтому один и тот же вопрос
// не должен уходить в сеть снова.
func TestLookupsAreCached(t *testing.T) {
	lookup := &fakeLookup{
		hosts: map[string][]string{"dtel.ru": {"144.123.122.123"}},
		addrs: map[string][]string{"203.0.113.7": {"cdn.example.com"}},
	}
	r := NewWith(lookup)

	rules := []config.RoutingRule{
		rule("1", config.RuleTypeDomain, "dtel.ru"),
		rule("2", config.RuleTypeIP, "203.0.113.7"),
	}

	for i := 0; i < 3; i++ {
		r.Sources(context.Background(), rules)
	}

	if n := lookup.times("host:dtel.ru"); n != 1 {
		t.Errorf("имя спрошено %d раз вместо одного", n)
	}
	if n := lookup.times("addr:203.0.113.7"); n != 1 {
		t.Errorf("адрес спрошен %d раз вместо одного", n)
	}
}

// Обрыв по нашему собственному сроку не должен закрывать имя от группировки
// на десять минут: за него отвечает не DNS.
func TestOwnCancelIsNotCached(t *testing.T) {
	lookup := &fakeLookup{addrs: map[string][]string{"203.0.113.7": {"cdn.example.com"}}}
	r := NewWith(lookup)

	rules := []config.RoutingRule{rule("1", config.RuleTypeIP, "203.0.113.7")}

	stopped, cancel := context.WithCancel(context.Background())
	cancel()

	if got := r.Sources(stopped, rules); len(got) != 0 {
		t.Fatalf("на оборванном запросе что-то нашлось: %v", got)
	}

	got := r.Sources(context.Background(), rules)
	if got["1"] != "example.com" {
		t.Errorf("после обрыва источник больше не ищется: %v", got)
	}
}
