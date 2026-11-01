package appsteer

import (
	"net/netip"
	"testing"
)

func TestFilters(t *testing.T) {
	o := Options{
		Config:  Config{From: home, To: tun, DropIPv6: true},
		Exclude: []netip.AddrPort{netip.MustParseAddrPort("203.0.113.9:51820"), netip.MustParseAddrPort("[2001:db8::9]:1")},
	}
	out := "outbound and !loopback and ((ip and ip.SrcAddr == 192.168.0.124 and (tcp or udp)" +
		" and !(ip.DstAddr == 203.0.113.9 and udp.DstPort == 51820))" +
		" or (ipv6 and ((tcp and tcp.Syn and !tcp.Ack) or udp)))"
	if got := outboundFilter(o); got != out {
		t.Errorf("исходящий:\n%s\nожидался:\n%s", got, out)
	}
	in := "inbound and !loopback and ip and ip.DstAddr == 10.8.1.13 and (tcp or udp)" +
		" and !(ip.SrcAddr == 203.0.113.9 and udp.SrcPort == 51820)"
	if got := inboundFilter(o); got != in {
		t.Errorf("входящий:\n%s\nожидался:\n%s", got, in)
	}
}
