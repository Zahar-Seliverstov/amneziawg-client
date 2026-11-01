package vpn

import (
	"errors"
	"log"
	"net/netip"

	"github.com/user/amnezia-web-client/internal/iproute"
	"github.com/user/amnezia-web-client/internal/tunnel"
)

// routeDNSServers заводит серверы имён из конфига в туннель. Вызывать под
// routeMu. Адреса локальной сети и петли пропускаются: к ним туннель не ведёт.
func (m *Manager) routeDNSServers(spec *tunnel.Spec) {
	for _, server := range parseDNSServers(spec.DNS) {
		addr := server.Addr().Unmap()
		if addr.IsLoopback() || addr.IsPrivate() || addr.IsLinkLocalUnicast() {
			continue
		}
		prefix := netip.PrefixFrom(addr, addr.BitLen()).String()
		if err := m.addRoute(m.tunnelRoute(prefix)...); err != nil && !errors.Is(err, iproute.ErrExists) {
			log.Printf("Warning: failed to route DNS server %s via tunnel: %v", addr, err)
		}
	}
}
