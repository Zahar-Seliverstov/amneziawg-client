package appsteer

// Фильтры перехвата на языке WinDivert. Здесь, а не в файле Windows, чтобы
// проверять их тестами где угодно.

import (
	"fmt"
	"net/netip"
	"strings"
)

// Options — параметры перехвата.
type Options struct {
	Config
	// Exclude — серверы VPN: зашифрованный трафик самого туннеля не
	// перехватывается вовсе, он идёт мимо нас без задержки.
	Exclude []netip.AddrPort
}

// outboundFilter — исходящие пакеты с адреса From, кроме трафика туннеля и
// петли. При DropIPv6 — ещё и начала соединений по IPv6.
func outboundFilter(o Options) string {
	f := fmt.Sprintf("outbound and !loopback and ((ip and ip.SrcAddr == %s and (tcp or udp)%s)", o.From, exclude(o.Exclude, "Dst"))
	if o.DropIPv6 {
		f += " or (ipv6 and ((tcp and tcp.Syn and !tcp.Ack) or udp))"
	}
	return f + ")"
}

// inboundFilter — входящие пакеты на адрес To, кроме трафика туннеля.
func inboundFilter(o Options) string {
	return fmt.Sprintf("inbound and !loopback and ip and ip.DstAddr == %s and (tcp or udp)%s", o.To, exclude(o.Exclude, "Src"))
}

// exclude — условие «не к серверу VPN» (dir = Dst) или «не от него» (Src).
func exclude(servers []netip.AddrPort, dir string) string {
	var b strings.Builder
	for _, srv := range servers {
		if srv.Addr().Is4() {
			fmt.Fprintf(&b, " and !(ip.%sAddr == %s and udp.%sPort == %d)", dir, srv.Addr(), dir, srv.Port())
		}
	}
	return b.String()
}
