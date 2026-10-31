package api

import (
	"net"
	"testing"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
)

func echoReply(t *testing.T, id, seq int) []byte {
	t.Helper()
	msg := icmp.Message{Type: ipv4.ICMPTypeEchoReply, Body: &icmp.Echo{ID: id, Seq: seq}}
	b, err := msg.Marshal(nil)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// Сырой сокет видит все ICMP системы: брать можно только ответ от нашего
// сервера на наш запрос.
func TestMatchesEchoTakesOnlyOurReply(t *testing.T) {
	server := &net.IPAddr{IP: net.ParseIP("203.0.113.10")}
	other := &net.IPAddr{IP: net.ParseIP("1.1.1.1")}
	reply := icmp.Type(ipv4.ICMPTypeEchoReply)

	cases := []struct {
		name   string
		packet []byte
		peer   net.Addr
		want   bool
	}{
		{"наш ответ", echoReply(t, 7, 2), server, true},
		{"чужой сервер", echoReply(t, 7, 2), other, false},
		{"чужой идентификатор", echoReply(t, 8, 2), server, false},
		{"прошлая попытка", echoReply(t, 7, 1), server, false},
		{"мусор", []byte{1, 2}, server, false},
	}
	for _, c := range cases {
		if got := matchesEcho(c.packet, c.peer, server, reply, 1, 7, 2); got != c.want {
			t.Errorf("%s: %v, ожидалось %v", c.name, got, c.want)
		}
	}
}
