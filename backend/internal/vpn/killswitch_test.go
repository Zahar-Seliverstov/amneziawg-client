package vpn

import (
	"strings"
	"testing"

	"github.com/user/amnezia-web-client/internal/config"
)

func peerWith(allowed ...string) config.AmneziaWGConfig {
	return config.AmneziaWGConfig{Peers: []config.PeerConfig{{AllowedIPs: allowed}}}
}

// Условия применимости обязаны повторять configureRouting. Разъехавшись, они
// дали бы худший из возможных исходов: блокировку, которая режет трафик,
// выведенный мимо туннеля намеренно.
func TestKillSwitchApplicable(t *testing.T) {
	full := peerWith("0.0.0.0/0")
	split := peerWith("10.0.0.0/8", "192.168.0.0/16")

	enabled := []config.RoutingRule{{ID: "r1", Type: config.RuleTypeIP, Value: "1.1.1.1", Enabled: true}}
	disabled := []config.RoutingRule{{ID: "r1", Type: config.RuleTypeIP, Value: "1.1.1.1"}}

	cases := []struct {
		name string
		cfg  config.AmneziaWGConfig
		rc   *config.RoutingConfig
		want bool
	}{
		{
			name: "правил нет, весь трафик в туннель",
			cfg:  full, rc: nil, want: true,
		},
		{
			name: "правил нет, но туннель частичный",
			cfg:  split, rc: nil, want: false,
		},
		{
			name: "пустой список правил и весь трафик в туннель",
			cfg:  full,
			rc:   &config.RoutingConfig{Mode: config.RoutingModeVPNList},
			want: true,
		},
		{
			name: "только список через VPN — остальное идёт напрямую по выбору пользователя",
			cfg:  full,
			rc:   &config.RoutingConfig{Mode: config.RoutingModeVPNList, Rules: enabled},
			want: false,
		},
		{
			name: "всё через VPN без исключений",
			cfg:  full,
			rc:   &config.RoutingConfig{Mode: config.RoutingModeDirectList},
			want: true,
		},
		{
			name: "всё через VPN, но есть действующее исключение",
			cfg:  full,
			rc:   &config.RoutingConfig{Mode: config.RoutingModeDirectList, Rules: enabled},
			want: false,
		},
		{
			name: "исключение выключено — обходить туннель нечему",
			cfg:  full,
			rc:   &config.RoutingConfig{Mode: config.RoutingModeDirectList, Rules: disabled},
			want: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, reason := killSwitchApplicable(&tc.cfg, tc.rc)

			if got != tc.want {
				t.Fatalf("применимость %v, ожидалась %v (причина: %q)", got, tc.want, reason)
			}
			// Отказ без объяснения — худший вид: пользователь включил защиту,
			// она не работает, и почему — неизвестно.
			if !got && strings.TrimSpace(reason) == "" {
				t.Error("блокировка неприменима, но причина не названа")
			}
			if got && reason != "" {
				t.Errorf("блокировка применима, но названа причина отказа: %q", reason)
			}
		})
	}
}

// Полный туннель может быть объявлен и через IPv6.
func TestKillSwitchApplicableIPv6DefaultRoute(t *testing.T) {
	cfg := peerWith("::/0")
	if ok, reason := killSwitchApplicable(&cfg, nil); !ok {
		t.Errorf("маршрут по умолчанию IPv6 не признан полным туннелем: %s", reason)
	}
}

// Без подсистемы блокировки настройка обязана честно сообщать, что не
// работает: молчаливое «включено» опаснее выключенного.
func TestKillSwitchStateWithoutDriver(t *testing.T) {
	m := NewManager()
	m.firewallDriver = nil
	m.firewallErr = errNoFirewall{}
	m.killSwitchOn = true

	state := m.KillSwitchState()
	if state.Available {
		t.Error("блокировка объявлена доступной без подсистемы")
	}
	if state.Reason == "" {
		t.Error("недоступность не объяснена")
	}
	if state.Active {
		t.Error("блокировка объявлена действующей, хотя ставить её нечем")
	}
}

type errNoFirewall struct{}

func (errNoFirewall) Error() string { return "в системе нет ни nft, ни iptables" }
