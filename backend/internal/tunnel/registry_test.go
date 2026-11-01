package tunnel

import (
	"errors"
	"strings"
	"testing"
)

type fakeConfig struct{ proto string }

func (c fakeConfig) Protocol() string { return c.proto }
func (c fakeConfig) Spec() Spec       { return Spec{} }

type fakeProtocol struct{ id, marker string }

func (p fakeProtocol) ID() string            { return p.id }
func (p fakeProtocol) Title() string         { return p.id }
func (p fakeProtocol) Sniff(raw string) bool { return strings.Contains(raw, p.marker) }
func (p fakeProtocol) Parse(string) (Config, error) {
	return fakeConfig{p.id}, nil
}

type fakeEngine struct{ id, proto string }

func (e fakeEngine) ID() string                            { return e.id }
func (e fakeEngine) Supports(c Config) bool                { return c.Protocol() == e.proto }
func (e fakeEngine) Start(Config, Options) (Device, error) { return nil, nil }

func testRegistry() *Registry {
	return NewRegistry(
		[]Protocol{fakeProtocol{"a", "[A]"}, fakeProtocol{"b", "[B]"}, fakeProtocol{"ab", "[A]"}},
		[]Engine{fakeEngine{"ядро-b", "b"}, fakeEngine{"ядро-a", "a"}, fakeEngine{"ещё-a", "a"}},
	)
}

func TestParseDetectsFormat(t *testing.T) {
	r := testRegistry()

	// Подходят двое — берётся первый зарегистрированный.
	if cfg, err := r.Parse("", "[A]"); err != nil || cfg.Protocol() != "a" {
		t.Errorf("распознано %v, %v", cfg, err)
	}
	if cfg, err := r.Parse("", "[B]"); err != nil || cfg.Protocol() != "b" {
		t.Errorf("распознано %v, %v", cfg, err)
	}
	if _, err := r.Parse("", "непонятно"); !errors.Is(err, ErrUnknownFormat) {
		t.Errorf("неизвестный формат: %v", err)
	}
}

// Сохранённый идентификатор протокола главнее распознавания по тексту.
func TestParseByID(t *testing.T) {
	r := testRegistry()

	if cfg, err := r.Parse("b", "[A]"); err != nil || cfg.Protocol() != "b" {
		t.Errorf("по идентификатору: %v, %v", cfg, err)
	}
	if _, err := r.Parse("нет-такого", "[A]"); err == nil || !strings.Contains(err.Error(), "нет-такого") {
		t.Errorf("неизвестный протокол: %v", err)
	}
}

func TestEngineSelection(t *testing.T) {
	r := testRegistry()

	if e, err := r.Engine(fakeConfig{"a"}); err != nil || e.ID() != "ядро-a" {
		t.Errorf("для a выбрано %v, %v", e, err)
	}
	if _, err := r.Engine(fakeConfig{"c"}); err == nil || !strings.Contains(err.Error(), `"c"`) {
		t.Errorf("без ядра: %v", err)
	}
}

func TestProtocolsIsCopy(t *testing.T) {
	r := testRegistry()
	list := r.Protocols()
	list[0] = nil
	if r.Protocols()[0] == nil {
		t.Error("Protocols отдал внутренний срез")
	}
}

func TestSuggestName(t *testing.T) {
	cases := []struct {
		spec Spec
		want string
	}{
		{Spec{Endpoints: []string{"vpn.example.com:51820"}}, "vpn.example.com"},
		{Spec{Endpoints: []string{"[2001:db8::1]:51820"}}, "2001:db8::1"},
		{Spec{Endpoints: []string{""}, Addresses: []string{"10.8.0.2/32"}}, "10.8.0.2"},
		{Spec{}, "Конфигурация"},
	}
	for _, c := range cases {
		if got := SuggestName(c.spec); got != c.want {
			t.Errorf("%+v: %q, ожидалось %q", c.spec, got, c.want)
		}
	}
}

func TestHost(t *testing.T) {
	for in, want := range map[string]string{"1.2.3.4": "1.2.3.4", "a.b:1": "a.b", "[::1]": "::1", " x:2 ": "x"} {
		if got := Host(in); got != want {
			t.Errorf("%q: %q, ожидалось %q", in, got, want)
		}
	}
}
