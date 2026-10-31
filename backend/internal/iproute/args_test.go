package iproute

import (
	"net/netip"
	"testing"
)

func TestParseRouteArgs(t *testing.T) {
	cases := []struct {
		args []string
		want routeSpec
	}{
		{[]string{"0.0.0.0/1", "dev", "awg0"},
			routeSpec{Prefix: netip.MustParsePrefix("0.0.0.0/1"), Device: "awg0"}},
		{[]string{"1.2.3.4/32", "via", "192.168.1.1", "dev", "Ethernet 2"},
			routeSpec{Prefix: netip.MustParsePrefix("1.2.3.4/32"), Gateway: netip.MustParseAddr("192.168.1.1"), Device: "Ethernet 2"}},
		// Адрес без маски — маршрут на один адрес, как у ip.
		{[]string{"2001:db8::1", "dev", "awg0"},
			routeSpec{Prefix: netip.MustParsePrefix("2001:db8::1/128"), Device: "awg0"}},
		// Биты за маской отбрасываются: Windows такой префикс не примет.
		{[]string{"10.1.2.3/8", "dev", "awg0"},
			routeSpec{Prefix: netip.MustParsePrefix("10.0.0.0/8"), Device: "awg0"}},
	}

	for _, c := range cases {
		got, err := parseRouteArgs(c.args)
		if err != nil {
			t.Errorf("%v: %v", c.args, err)
			continue
		}
		if got != c.want {
			t.Errorf("%v: получили %+v, ожидали %+v", c.args, got, c.want)
		}
	}
}

func TestParseRouteArgsRejects(t *testing.T) {
	for _, args := range [][]string{
		nil,
		{"не-адрес", "dev", "awg0"},
		{"1.2.3.4/32", "dev"},
		{"1.2.3.4/32", "metric", "5"},
		{"1.2.3.4/32", "via", "fe80::1"},
	} {
		if _, err := parseRouteArgs(args); err == nil {
			t.Errorf("%v: ожидалась ошибка", args)
		}
	}
}

func TestFormatRoute(t *testing.T) {
	spec := routeSpec{
		Prefix:  netip.MustParsePrefix("1.2.3.4/32"),
		Gateway: netip.MustParseAddr("192.168.1.1"),
		Device:  "Wi-Fi",
	}
	if got, want := formatRoute(spec), "1.2.3.4/32 via 192.168.1.1 dev Wi-Fi"; got != want {
		t.Errorf("получили %q, ожидали %q", got, want)
	}

	// Шлюз 0.0.0.0 — так Windows описывает маршрут по линку.
	spec.Gateway = netip.IPv4Unspecified()
	if got, want := formatRoute(spec), "1.2.3.4/32 dev Wi-Fi"; got != want {
		t.Errorf("получили %q, ожидали %q", got, want)
	}
}
