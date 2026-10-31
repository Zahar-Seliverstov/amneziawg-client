package api

import (
	"errors"
	"net"
	"os"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
)

// icmpPing шлёт серию эхо-запросов и возвращает наименьшее время оборота.
// Нужен сырой сокет, то есть права root или администратора — они у службы
// есть. Ошибка значит «замерить так не вышло», и вызывающий пробует TCP.
func icmpPing(ip net.IP) (time.Duration, error) {
	network, listen, request, reply, proto := "ip4:icmp", "0.0.0.0", icmp.Type(ipv4.ICMPTypeEcho), icmp.Type(ipv4.ICMPTypeEchoReply), 1
	if ip.To4() == nil {
		network, listen, request, reply, proto = "ip6:ipv6-icmp", "::", ipv6.ICMPTypeEchoRequest, ipv6.ICMPTypeEchoReply, 58
	}

	conn, err := icmp.ListenPacket(network, listen)
	if err != nil {
		return 0, err
	}
	defer conn.Close()

	// Идентификатор отличает наши ответы от чужих: сырой сокет видит все
	// входящие ICMP системы, в том числе ответы обычной утилиты ping.
	id := os.Getpid() & 0xffff
	dst := &net.IPAddr{IP: ip}

	var best time.Duration
	for seq := 1; seq <= pingAttempts; seq++ {
		rtt, err := icmpEcho(conn, dst, request, reply, proto, id, seq)
		if err != nil {
			// Молчит: эхо закрыто или сырой сокет не принимает ответы
			// (бывает на Windows). Остальные попытки ждали бы так же зря —
			// пусть вызывающий сразу переходит к TCP.
			break
		}
		if best == 0 || rtt < best {
			best = rtt
		}
	}
	if best == 0 {
		return 0, errors.New("нет эхо-ответа")
	}
	return best, nil
}

// icmpEcho отправляет один запрос и ждёт ответ с тем же id и seq.
func icmpEcho(conn *icmp.PacketConn, dst *net.IPAddr, request, reply icmp.Type, proto, id, seq int) (time.Duration, error) {
	msg := icmp.Message{
		Type: request,
		Body: &icmp.Echo{ID: id, Seq: seq, Data: []byte("awg-client")},
	}
	wire, err := msg.Marshal(nil)
	if err != nil {
		return 0, err
	}

	start := time.Now()
	deadline := start.Add(pingTimeout)
	if err := conn.SetReadDeadline(deadline); err != nil {
		return 0, err
	}
	if _, err := conn.WriteTo(wire, dst); err != nil {
		return 0, err
	}

	buf := make([]byte, 1500)
	for {
		n, peer, err := conn.ReadFrom(buf)
		if err != nil {
			return 0, err // Истёк срок или сокет закрыт.
		}
		if !matchesEcho(buf[:n], peer, dst, reply, proto, id, seq) {
			continue
		}
		return time.Since(start), nil
	}
}

// matchesEcho сообщает, что пакет — ответ именно на наш запрос.
func matchesEcho(packet []byte, peer net.Addr, dst *net.IPAddr, reply icmp.Type, proto, id, seq int) bool {
	from, ok := peer.(*net.IPAddr)
	if !ok || !from.IP.Equal(dst.IP) {
		return false
	}
	msg, err := icmp.ParseMessage(proto, packet)
	if err != nil || msg.Type != reply {
		return false
	}
	echo, ok := msg.Body.(*icmp.Echo)
	return ok && echo.ID == id && echo.Seq == seq
}
