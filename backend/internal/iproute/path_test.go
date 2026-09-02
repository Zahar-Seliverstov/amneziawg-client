package iproute

import (
	"errors"
	"strings"
	"testing"
)

func TestParsePath(t *testing.T) {
	cases := []struct {
		name   string
		output string
		want   Path
	}{
		{
			name:   "через шлюз другого туннеля",
			output: "192.168.96.28 via 192.168.35.1 dev tun0 src 192.168.35.38 uid 1000 \n    cache \n",
			want:   Path{Gateway: "192.168.35.1", Device: "tun0"},
		},
		{
			name:   "по линку, без шлюза",
			output: "192.168.0.5 dev wlan0 src 192.168.0.124 uid 1000 \n    cache \n",
			want:   Path{Device: "wlan0"},
		},
		{
			name:   "адрес забрал туннель",
			output: "1.1.1.1 dev awg0 src 10.8.1.18 uid 1000 \n    cache \n",
			want:   Path{Device: "awg0"},
		},
		{
			name:   "собственный адрес машины",
			output: "local 127.0.0.1 dev lo src 127.0.0.1 uid 1000 \n    cache <local> \n",
			want:   Path{Device: "lo", Local: true},
		},
		{
			name:   "IPv6",
			output: "2001:db8::1 from :: via fe80::1 dev wlan0 proto ra src 2001:db8::99 metric 1024 pref medium\n",
			want:   Path{Gateway: "fe80::1", Device: "wlan0"},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := parsePath(c.output)
			if err != nil {
				t.Fatalf("разбор не удался: %v", err)
			}
			if got != c.want {
				t.Errorf("получили %+v, ждали %+v", got, c.want)
			}
		})
	}
}

// Недостижимый адрес — это не путь «куда-нибудь»: маршрут по нему ставить
// нельзя, и молчаливый Path{} увёл бы трафик неизвестно куда.
func TestParsePathRejectsUnreachable(t *testing.T) {
	_, err := parsePath("unreachable 10.0.0.1 dev lo src 127.0.0.1 uid 1000\n")
	if !errors.Is(err, ErrUnreachable) {
		t.Fatalf("получили %v, ждали ErrUnreachable", err)
	}
}

func TestParsePathRejectsAnswerWithoutDevice(t *testing.T) {
	for _, output := range []string{"", "   \n", "1.2.3.4 via 10.0.0.1 src 10.0.0.2\n"} {
		if _, err := parsePath(output); err == nil {
			t.Errorf("ответ %q принят, а должен быть отвергнут", output)
		}
	}
}

func TestParseDefaultPath(t *testing.T) {
	// Несколько маршрутов по умолчанию — обычное дело, когда включены и
	// кабель, и Wi-Fi. Ядро печатает их в порядке предпочтения.
	output := "default via 192.168.0.1 dev wlan0 proto dhcp src 192.168.0.124 metric 600 \n" +
		"default via 10.0.0.1 dev enp4s0 proto dhcp metric 100 \n"

	got, err := parseDefaultPath(output)
	if err != nil {
		t.Fatalf("разбор не удался: %v", err)
	}
	if want := (Path{Gateway: "192.168.0.1", Device: "wlan0"}); got != want {
		t.Errorf("получили %+v, ждали %+v", got, want)
	}
}

// Маршрут по умолчанию без шлюза бывает на точка-точка (модем, ppp). Раньше
// такой маршрут считался отсутствующим, и исключения на таких машинах не
// ставились вовсе.
func TestParseDefaultPathWithoutGateway(t *testing.T) {
	got, err := parseDefaultPath("default dev ppp0 scope link \n")
	if err != nil {
		t.Fatalf("разбор не удался: %v", err)
	}
	if want := (Path{Device: "ppp0"}); got != want {
		t.Errorf("получили %+v, ждали %+v", got, want)
	}
}

func TestParseDefaultPathWhenThereIsNone(t *testing.T) {
	if _, err := parseDefaultPath("10.0.0.0/8 dev eth0 scope link \n"); err == nil {
		t.Error("отсутствие маршрута по умолчанию должно быть ошибкой")
	}
}

func TestRouteArgs(t *testing.T) {
	got := Path{Gateway: "192.168.0.1", Device: "wlan0"}.RouteArgs("1.2.3.4/32")
	if want := "1.2.3.4/32 via 192.168.0.1 dev wlan0"; strings.Join(got, " ") != want {
		t.Errorf("получили %q, ждали %q", strings.Join(got, " "), want)
	}

	got = Path{Device: "ppp0"}.RouteArgs("1.2.3.4/32")
	if want := "1.2.3.4/32 dev ppp0"; strings.Join(got, " ") != want {
		t.Errorf("получили %q, ждали %q", strings.Join(got, " "), want)
	}
}

func TestHost(t *testing.T) {
	cases := map[string]string{
		"192.168.96.28/32": "192.168.96.28",
		"10.0.0.0/8":       "10.0.0.0",
		"1.1.1.1":          "1.1.1.1",
		"2001:db8::/32":    "2001:db8::",
	}
	for in, want := range cases {
		if got := Host(in); got != want {
			t.Errorf("Host(%q) = %q, ждали %q", in, got, want)
		}
	}
}

func TestIsIPv6(t *testing.T) {
	cases := map[string]bool{
		"1.2.3.4": false, "10.0.0.0/8": false,
		"2001:db8::1": true, "2001:db8::/32": true, "fe80::1": true,
	}
	for in, want := range cases {
		if got := isIPv6(in); got != want {
			t.Errorf("isIPv6(%q) = %v, ждали %v", in, got, want)
		}
	}
}
