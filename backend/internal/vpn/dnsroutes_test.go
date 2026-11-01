package vpn

import (
	"strings"
	"testing"

	"github.com/user/amnezia-web-client/internal/tunnel"
)

// В режиме «только список» серверы имён из конфига идут через туннель:
// иначе провайдер подменяет ответы о заблокированных именах.
func TestRouteDNSServersViaTunnel(t *testing.T) {
	ip := &fakeIP{}
	m := managerWith(ip)

	cfg := &tunnel.Spec{}
	cfg.DNS = []string{"1.1.1.1", "192.168.0.1", "127.0.0.53", "2606:4700:4700::1111"}
	m.routeDNSServers(cfg)

	got := strings.Join(ipJoined(ip.added), "|")
	for _, want := range []string{"1.1.1.1/32", "2606:4700:4700::1111/128"} {
		if !strings.Contains(got, want) {
			t.Errorf("нет маршрута %s: %v", want, ip.added)
		}
	}
	for _, skip := range []string{"192.168.0.1", "127.0.0.53"} {
		if strings.Contains(got, skip) {
			t.Errorf("локальный сервер %s заведён в туннель: %v", skip, ip.added)
		}
	}
	if len(m.installedRoutes) != 2 {
		t.Errorf("на учёте %d маршрутов, ожидалось 2", len(m.installedRoutes))
	}
}
